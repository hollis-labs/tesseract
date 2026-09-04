package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/hollis-labs/go-apppaths/paths"
	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	"github.com/hollis-labs/go-modelsdev/modelsdev"
	feotel "github.com/hollis-labs/go-otel"
	"github.com/hollis-labs/go-otel/propagation"
	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextapi"
	"github.com/hollis-labs/tesseract/internal/contextcli"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	llmanthropic "github.com/hollis-labs/tesseract/internal/llm/anthropic"
	llmopenai "github.com/hollis-labs/tesseract/internal/llm/openai"
	cplugin "github.com/hollis-labs/tesseract/internal/plugin"
	"github.com/hollis-labs/tesseract/internal/webui"
	_ "modernc.org/sqlite"
)

// defaultServeAddr binds loopback only. Tesseract is a local-first content
// memory service: the default listener must not be reachable from the network,
// because the default configuration has no authentication. Widening the bind
// address is an explicit act, and one that requires a token mode — see
// validateExposure.
const defaultServeAddr = "127.0.0.1:8089"

type serveConfig struct {
	Addr        string
	ManagedAuth bool
	StaticToken string
	// AllowUnauthenticatedRemote opts out of the refusal to serve an
	// unauthenticated listener on a non-loopback address.
	AllowUnauthenticatedRemote bool
	EnableMetrics              bool
	EnableRequestLogs          bool
	RequestLogMode             string
	ShutdownTimeout            time.Duration
}

// authConfigured reports whether a token mode is in force. Both modes make the
// API server demand credentials on every route except readiness and metrics.
func (c serveConfig) authConfigured() bool {
	return c.ManagedAuth || strings.TrimSpace(c.StaticToken) != ""
}

// isLoopbackAddr reports whether a listen address reaches only the loopback
// interface.
//
// The cases that matter:
//   - "127.0.0.1:8089", "[::1]:8089", "localhost:8089" — loopback.
//   - ":8089" and "" — an empty host means every interface, so NOT loopback.
//     This is the case the old default fell into.
//   - anything else, including a hostname we would have to resolve — treated
//     as non-loopback. Resolution is deliberately not attempted: a DNS answer
//     is not a property of this machine, and guessing wrong here fails open.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// No port separator (e.g. "localhost"). Treat the whole value as the
		// host; ListenAndServe will reject it later if it is malformed.
		host = addr
	}
	host = strings.TrimSpace(strings.Trim(strings.TrimSpace(host), "[]"))
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// validateExposure refuses to start an unauthenticated listener that is
// reachable from outside this machine. Loopback without a token stays fully
// supported — that is the local-first dev experience — and so does any bind
// address once a token mode is configured. Only the combination of a
// network-reachable address and no credentials is rejected, and even that can
// be overridden explicitly.
func validateExposure(cfg serveConfig) error {
	if cfg.authConfigured() || cfg.AllowUnauthenticatedRemote || isLoopbackAddr(cfg.Addr) {
		return nil
	}
	return fmt.Errorf(
		"refusing to serve %s: address is reachable from the network and no authentication is configured.\n"+
			"  Every route, including /v1/admin/* and every stored record, would be readable by anyone who can reach this port.\n"+
			"  Choose one:\n"+
			"    --addr %s                 bind loopback only (default)\n"+
			"    --managed-auth                       require store-backed capability tokens\n"+
			"    --static-token <token>               require a single shared bearer token\n"+
			"    --allow-unauthenticated-remote       serve anyway, unauthenticated (not recommended)",
		cfg.Addr, defaultServeAddr)
}

// version is the release identity stamped in at build time:
//
//	go build -ldflags "-X main.version=v0.9.0" ./cmd/tesseract
//
// It is deliberately empty by default. A literal here would rot the moment a
// release shipped without someone remembering to bump it — which is exactly
// what happened to the "0.9.0" hardcoded in the MCP handshake — so an
// unstamped build reports what the toolchain recorded instead. See
// buildVersion.
var version string

// buildVersion reports the binary's version.
//
// Three cases, in order of trustworthiness:
//
//   - stamped with -ldflags -X main.version=... — a release build, use it;
//   - installed with `go install .../tesseract@vX.Y.Z` — the module version
//     the toolchain recorded is the real tag;
//   - built from a checkout — the module version is "(devel)" and the identity
//     that matters is the VCS revision the toolchain embedded.
func buildVersion() string {
	if v := strings.TrimSpace(version); v != "" {
		return v
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if v := strings.TrimSpace(info.Main.Version); v != "" && v != "(devel)" {
		return v
	}
	var revision string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if revision == "" {
		// No VCS stamp: built from a tarball, or with -buildvcs=false.
		return "devel"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if dirty {
		return "devel+" + revision + "-dirty"
	}
	return "devel+" + revision
}

// topLevelCommand is one entry in `tesseract --help`.
//
// The table is the single written-down list of top-level commands, and run()
// treats a name that is not in it as unknown. Before it existed there was no
// top-level help at all: a bare `tesseract` cold-booted the whole store and
// then printed a usage line for a binary called "context".
type topLevelCommand struct {
	// Name is the command as typed: `tesseract <Name>`.
	Name string
	// Summary is one line, lowercase, no trailing period.
	Summary string
	// Description is the paragraph `tesseract <Name> --help` opens with.
	Description string
	// FlagSet builds the command's real flagset. When it is set, `--help`
	// prints PrintDefaults() so the help and the parser cannot disagree.
	FlagSet func() *flag.FlagSet
	// Flags documents a command whose flagset is built in a file this one does
	// not own, so PrintDefaults is out of reach. Each entry is a
	// tab-separated "  -name arg\tdescription" row.
	// TestDocumentedFlagsMatchSource reads FlagsSource and fails when the flag
	// names defined there and the names documented here differ.
	Flags []string
	// FlagsSource is the file whose flag definitions Flags mirrors.
	FlagsSource string
	// Subcommands are the verbs the command dispatches on. SubcommandsSource
	// and SubcommandsFunc name the file and function whose dispatch switch
	// they mirror, for the same parity test.
	Subcommands       []string
	SubcommandsSource string
	SubcommandsFunc   string
}

// topLevelCommands returns every command `tesseract` dispatches, in the order
// `tesseract --help` lists them: the everyday ones first, then the one-shot
// maintenance operations.
func topLevelCommands() []topLevelCommand {
	return []topLevelCommand{
		{
			Name:        "serve",
			Summary:     "run the HTTP API and the embedded web UI",
			Description: "Serves the JSON API under /v1/ and the web UI at /. Binds loopback only by\n  default; a non-loopback address requires a token mode or an explicit opt-out.",
			FlagSet:     func() *flag.FlagSet { fs, _ := newServeFlagSet(); return fs },
		},
		{
			Name:        "mcp",
			Summary:     "run the MCP adapter over stdio",
			Description: "Speaks the Model Context Protocol on stdin/stdout, for an agent runtime that\n  launches tesseract as an MCP server.",
			FlagSet:     func() *flag.FlagSet { fs, _ := newMCPFlagSet(); return fs },
		},
		{
			Name:        "context",
			Summary:     "read, write and maintain records (see below)",
			Description: "Every record, memory and maintenance operation is a subcommand of `context`.",
		},
		{
			Name:        "path",
			Summary:     "print the resolved on-disk layout",
			Description: "Prints where this binary would read and write — the go-apppaths roots, the\n  active workspace, the database, config.yaml, records/ and queue.db. Resolves\n  without creating anything.",
		},
		{
			Name:              "plugin",
			Summary:           "list, install and manage plugins",
			Description:       "Manages the plugins discovered under ./plugins (or $TESSERACT_PLUGINS_DIR).",
			Subcommands:       []string{"list", "install", "uninstall", "disable", "enable"},
			SubcommandsSource: "../../internal/contextcli/plugin_cmd.go",
			SubcommandsFunc:   "RunPluginCmd",
		},
		{
			Name:        "backfill-embeddings",
			Summary:     "embed stored records that have no vector yet",
			Description: "Walks the store and embeds anything the recall index is missing. Requires an\n  embedding provider in config.yaml and its API key in the environment.",
			Flags: []string{
				"  -namespace ns\tonly backfill this namespace",
			},
			FlagsSource: "backfill.go",
		},
		{
			Name:        "migrate-namespaces",
			Summary:     "one-shot rewrite of legacy namespaces",
			Description: "Rewrites legacy namespaces into the current scheme, lifting repeated path\n  segments into project: tags. Plans only unless -apply is given.",
			Flags: []string{
				"  -db path\tSQLite store to migrate (default: the resolved layout DB)",
				"  -apply\twrite the changes instead of planning them",
				"  -json\temit the full plan as JSON instead of a summary",
				"  -project-threshold n\tmin occurrences before a segment is lifted as a project: tag",
			},
			FlagsSource: "migrate.go",
		},
		{
			Name:        "migrate-knowledge-kinds",
			Summary:     "one-shot normalization of knowledge facet kinds",
			Description: "Normalizes off-vocabulary knowledge facet_kind values in place. Plans only\n  unless -apply is given.",
			Flags: []string{
				"  -db path\tSQLite store to migrate (default: the resolved layout DB)",
				"  -apply\twrite the changes instead of planning them",
				"  -json\temit the full plan as JSON instead of a summary",
				"  -expect-rows n\trefuse to apply unless the fresh plan has exactly n rows",
				"  -expect-digest d\trefuse to apply unless the fresh plan digests to d",
			},
			FlagsSource: "migrate_kinds.go",
		},
		{
			Name:        "verify-pointers",
			Summary:     "resolve knowledge pointers and record what was found",
			Description: "Resolves the pointers on knowledge records and writes what it saw to the\n  verification log. Plans only unless -apply is given.",
			Flags: []string{
				"  -db path\tSQLite store to verify against (default: the resolved layout DB)",
				"  -apply\trecord the observations instead of planning them",
				"  -json\temit the full plan as JSON instead of a summary",
				"  -scope heads|all\tcurrent heads only, or every knowledge revision",
				"  -schemes list\tpointer schemes to resolve",
				"  -recheck-after d\tskip pointers verified more recently than this",
				"  -timeout d\tper-pointer HTTP timeout",
				"  -concurrency n\thow many pointers to resolve at once",
				"  -expect-rows n\trefuse to apply unless the fresh plan has exactly n rows",
				"  -expect-digest d\trefuse to apply unless the fresh plan digests to d",
			},
			FlagsSource: "verify_pointers.go",
		},
	}
}

// lookupTopLevelCommand finds a command by name.
func lookupTopLevelCommand(name string) (topLevelCommand, bool) {
	for _, cmd := range topLevelCommands() {
		if cmd.Name == name {
			return cmd, true
		}
	}
	return topLevelCommand{}, false
}

// isHelpToken reports whether an argument turns a command line into a question.
func isHelpToken(arg string) bool {
	switch arg {
	case "-h", "-help", "--help":
		return true
	}
	return false
}

// wantsHelp reports whether the arguments after a command name ask for help.
// The bare word "help" counts only in the first position, so a positional
// argument that happens to be "help" is still a value; "--" ends the scan for
// the same reason.
func wantsHelp(args []string) bool {
	for i, a := range args {
		if a == "--" {
			return false
		}
		if isHelpToken(a) || (i == 0 && a == "help") {
			return true
		}
	}
	return false
}

// printUsage prints the top-level help.
func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, "tesseract — local-first content memory service\n\n")
	_, _ = fmt.Fprint(w, "usage:\n  tesseract <command> [flags]\n  tesseract context <subcommand> [flags]\n\ncommands:\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, cmd := range topLevelCommands() {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\n", cmd.Name, cmd.Summary)
	}
	_, _ = fmt.Fprintf(tw, "  help\tprint this help\n")
	_, _ = fmt.Fprintf(tw, "  version\tprint the version\n")
	_ = tw.Flush()
	_, _ = fmt.Fprint(w, "\nEvery record, memory and maintenance operation is a `context` subcommand —\n"+
		"`tesseract put ...` is not a command, `tesseract context put ...` is.\n\ncontext subcommands:\n")
	contextcli.WriteCommands(w)
	_, _ = fmt.Fprint(w, "\nrun `tesseract <command> --help` for that command's flags.\n")
}

// printCommandHelp prints one command's own help.
func printCommandHelp(w io.Writer, cmd topLevelCommand) {
	usage := "usage: tesseract " + cmd.Name
	if len(cmd.Subcommands) > 0 {
		usage += " <" + strings.Join(cmd.Subcommands, "|") + ">"
	}
	if cmd.FlagSet != nil || len(cmd.Flags) > 0 {
		usage += " [flags]"
	}
	_, _ = fmt.Fprintf(w, "%s\n\n  %s\n", usage, cmd.Description)

	switch {
	case cmd.FlagSet != nil:
		_, _ = fmt.Fprint(w, "\nflags:\n")
		fs := cmd.FlagSet()
		fs.SetOutput(w)
		fs.PrintDefaults()
	case len(cmd.Flags) > 0:
		_, _ = fmt.Fprint(w, "\nflags:\n")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, f := range cmd.Flags {
			_, _ = fmt.Fprintln(tw, f)
		}
		_ = tw.Flush()
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches the command line in two phases.
//
// Phase 1 answers questions about the binary — help, version, `path`, a
// command's flags, and an unrecognized command. None of it may write.
//
// Phase 2 is the cold boot: OTel, layout resolution (which materializes the
// XDG directories), config load, and contextstore.Open (which creates the
// records and index directories, the SQLite database, and runs every
// migration). It happens only once a command that needs a store is known.
//
// The split is the point. run() used to boot before it looked at the
// arguments, so a bare `tesseract` — the most natural first invocation there
// is — created the entire data layout and a database as a side effect, then
// failed with a usage line naming a binary called "context".
func run(ctx context.Context, args []string, stdout, stderr *os.File) int {
	cmd := ""
	var rest []string
	if len(args) > 0 {
		cmd = args[0]
		rest = args[1:]
	}

	switch cmd {
	case "", "help", "-h", "-help", "--help":
		printUsage(stdout)
		return 0
	case "version", "-version", "--version":
		_, _ = fmt.Fprintf(stdout, "tesseract %s\n", buildVersion())
		return 0
	}

	command, known := lookupTopLevelCommand(cmd)
	if !known {
		_, _ = fmt.Fprintf(stderr, "error: unknown command %q\n", cmd)
		_, _ = fmt.Fprintln(stderr, "run `tesseract --help` for the list of commands")
		return 1
	}

	if cmd == "context" {
		// contextcli answers its own help, down to an individual subcommand's
		// flags, and does it without a store for the same reason as above.
		if code, handled := contextcli.Help(ctx, stdout, stderr, rest); handled {
			return code
		}
	} else if wantsHelp(rest) {
		printCommandHelp(stdout, command)
		return 0
	}

	// `path` introspects the resolved layout without materializing directories
	// or opening the store — dispatch it before layout resolution.
	if cmd == "path" {
		return runPath(stdout, stderr)
	}

	// `plugin` works on the plugins directory alone. It takes no store, so it
	// no longer opens one on the way past.
	if cmd == "plugin" {
		return contextcli.RunPluginCmd(rest, stdout, stderr)
	}

	// ---- Phase 2: the cold boot. ----

	shutdown, err := feotel.Init(ctx, feotel.WithServiceName("tesseract"))
	if err != nil {
		log.Printf("warning: OTel init failed: %v", err)
	} else {
		defer shutdown(ctx)
	}

	layout, err := config.ResolveLayout()
	if err != nil {
		_, _ = stderr.WriteString("error: resolve layout: " + err.Error() + "\n")
		return 1
	}

	// The one-shot data operations open the DB file directly (potentially a
	// copy) and intentionally bypass contextstore.Open, so a run against a
	// temp copy doesn't materialize workspace layout state on it and a dry run
	// holds a read-only handle and nothing else.
	switch cmd {
	case "migrate-namespaces":
		return runMigrateNamespaces(ctx, layout.MainDB(), rest, stdout, stderr)
	case "migrate-knowledge-kinds":
		return runMigrateKnowledgeKinds(ctx, layout.MainDB(), rest, stdout, stderr)
	case "verify-pointers":
		return runVerifyPointers(ctx, layout.MainDB(), rest, stdout, stderr)
	}

	tesseractCfg, cfgErr := config.Load(filepath.Join(layout.ConfigDir(), "config.yaml"))
	if cfgErr != nil {
		log.Printf("warning: config load failed: %v (using defaults)", cfgErr)
		tesseractCfg = config.Defaults()
	}

	store, err := contextstore.Open(ctx, contextstore.Config{
		RootDir:    layout.DataDir(),
		RecordsDir: filepath.Join(layout.StateDir(), "records"),
		DBPath:     layout.MainDB(),
	})
	if err != nil {
		_, _ = stderr.WriteString("error: " + err.Error() + "\n")
		return 1
	}
	defer store.Close()

	switch cmd {
	case "serve":
		cfg, err := parseServeArgs(rest)
		if err != nil {
			_, _ = stderr.WriteString("error: " + err.Error() + "\n")
			return 1
		}
		return runServe(ctx, store, stderr, cfg, layout, tesseractCfg)
	case "mcp":
		token, err := parseMCPArgs(rest)
		if err != nil {
			_, _ = stderr.WriteString("error: " + err.Error() + "\n")
			return 1
		}
		return runMCP(ctx, store, stderr, token, layout, tesseractCfg)
	case "backfill-embeddings":
		return runBackfill(ctx, store, tesseractCfg, rest, stdout, stderr)
	}

	cli := &contextcli.CLI{
		Store:  store,
		Policy: contextpolicy.New(),
		Stdout: stdout,
		Stderr: stderr,
	}
	return cli.Run(ctx, args)
}

// serveFlags holds the pointers a serve flagset writes into.
//
// The flagset is built by newServeFlagSet rather than inline in parseServeArgs
// so `tesseract serve --help` prints exactly the flags the parser honors. A
// second, hand-written flag list in the help text would drift the first time a
// flag was added — the way the context usage string ended up naming 15 of 26
// subcommands.
type serveFlags struct {
	addr              *string
	managed           *bool
	static            *string
	allowUnauthRemote *bool
	metrics           *bool
	requestLogs       *bool
	requestLogMode    *string
	shutdownTimeout   *time.Duration
}

func newServeFlagSet() (*flag.FlagSet, *serveFlags) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	// Output is discarded while parsing: a parse failure is reported by the
	// caller with the same "error: " prefix every other failure uses. The help
	// path re-points the output at stdout before printing.
	fs.SetOutput(io.Discard)
	return fs, &serveFlags{
		addr:    fs.String("addr", defaultServeAddr, "listen address"),
		managed: fs.Bool("managed-auth", false, "enable managed token auth"),
		static:  fs.String("static-token", "", "static bearer token (legacy mode)"),
		allowUnauthRemote: fs.Bool("allow-unauthenticated-remote", false,
			"serve a non-loopback address without authentication (not recommended)"),
		metrics:         fs.Bool("metrics", false, "enable /v1/metrics endpoint"),
		requestLogs:     fs.Bool("request-logs", false, "enable structured request logs"),
		requestLogMode:  fs.String("request-log-mode", "redacted", "request log mode: redacted|full"),
		shutdownTimeout: fs.Duration("shutdown-timeout", 5*time.Second, "graceful shutdown timeout"),
	}
}

func parseServeArgs(args []string) (serveConfig, error) {
	fs, f := newServeFlagSet()
	if err := fs.Parse(args); err != nil {
		return serveConfig{}, err
	}
	if *f.managed && strings.TrimSpace(*f.static) != "" {
		return serveConfig{}, fmt.Errorf("managed-auth and static-token are mutually exclusive")
	}
	mode := strings.ToLower(strings.TrimSpace(*f.requestLogMode))
	if mode != "redacted" && mode != "full" {
		return serveConfig{}, fmt.Errorf("request-log-mode must be one of: redacted, full")
	}
	cfg := serveConfig{
		Addr:                       strings.TrimSpace(*f.addr),
		ManagedAuth:                *f.managed,
		StaticToken:                strings.TrimSpace(*f.static),
		AllowUnauthenticatedRemote: *f.allowUnauthRemote,
		EnableMetrics:              *f.metrics,
		EnableRequestLogs:          *f.requestLogs,
		RequestLogMode:             mode,
		ShutdownTimeout:            *f.shutdownTimeout,
	}
	if err := validateExposure(cfg); err != nil {
		return serveConfig{}, err
	}
	return cfg, nil
}

// newMCPFlagSet builds the mcp flagset. Split out for the same reason as
// newServeFlagSet: `tesseract mcp --help` prints the parser's own flags.
func newMCPFlagSet() (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs, fs.String("token", "", "capability token for mutating MCP tools")
}

func parseMCPArgs(args []string) (string, error) {
	fs, token := newMCPFlagSet()
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	return strings.TrimSpace(*token), nil
}

// createSynthesisProvider builds the LLM Provider used by /v1/synthesis/ask.
// Returns nil (and the route degrades to 503) when:
//   - no synthesis.provider is configured
//   - the provider name is unsupported
//   - the provider's API key env var is empty
//
// Supported providers: openai (openai-go SDK), anthropic (anthropic-sdk-go).
// Other vendors are not wired in this binary; consumers can inject their
// own llmcontracts.Provider implementation by mutating srv.SynthesisProvider
// before serve.
func createSynthesisProvider(cfg config.Config, stderr *os.File) llmcontracts.Provider {
	name := strings.ToLower(strings.TrimSpace(cfg.Synthesis.Provider))
	if name == "" {
		return nil
	}
	envFor := map[string]string{
		"openai":    "OPENAI_API_KEY",
		"anthropic": "ANTHROPIC_API_KEY",
	}
	env, supported := envFor[name]
	if !supported {
		_, _ = stderr.WriteString("warning: synthesis.provider=" + name + " is not supported — /v1/synthesis/ask disabled\n")
		return nil
	}
	if os.Getenv(env) == "" {
		_, _ = stderr.WriteString("warning: synthesis.provider=" + name + " but " + env + " not set — /v1/synthesis/ask disabled\n")
		return nil
	}
	switch name {
	case "openai":
		return llmopenai.New("")
	case "anthropic":
		return llmanthropic.New("")
	default:
		// Unreachable — envFor lookup above already filtered.
		return nil
	}
}

// createModelsDevClient builds the go-modelsdev cache used to look up
// per-model pricing for synthesis cost reporting. Returns nil if the initial
// refresh fails (the route still works, cost fields just stay null).
func createModelsDevClient(ctx context.Context, stderr *os.File) *modelsdev.Client {
	c := modelsdev.New()
	if err := c.Refresh(ctx); err != nil {
		_, _ = stderr.WriteString("warning: go-modelsdev refresh failed (" + err.Error() + ") — synthesis cost reporting disabled\n")
		return nil
	}
	c.StartRefresher(ctx)
	return c
}

// createEmbedder builds an embedcontracts.Embedder from config. Returns nil
// when the configured provider is unsupported or the API key is missing.
// Currently only "openai" is supported. Provider name is normalized
// (lowercased + trimmed) to mirror createSynthesisProvider.
func createEmbedder(cfg config.Config) embedcontracts.Embedder {
	if strings.ToLower(strings.TrimSpace(cfg.Embedding.Provider)) != "openai" {
		return nil
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Printf("warning: embedding.provider=openai but OPENAI_API_KEY not set — embedding disabled, falling back to BM25-only recall")
		return nil
	}
	return llmopenai.New("")
}

func runMCP(ctx context.Context, store *contextstore.Store, stderr *os.File, token string, layout paths.Layout, tesseractCfg config.Config) int {
	_, _ = stderr.WriteString("Tesseract MCP adapter starting (stdio)\n")

	mem, err := setupMemorySubsystem(ctx, store, stderr, layout, tesseractCfg)
	if err != nil {
		_, _ = stderr.WriteString("error: " + err.Error() + "\n")
		return 1
	}
	defer mem.Close()

	adapter := newMCPAdapter(store, token, mem, tesseractCfg, stderr)
	if err := adapter.Run(ctx); err != nil {
		_, _ = stderr.WriteString("error: " + err.Error() + "\n")
		return 1
	}
	return 0
}

// httpServerTimeouts groups the request boundaries applied to the daemon's
// listener. Grouped rather than inlined so tests can drive the same
// constructor with short values and assert the behaviour, not just the field.
type httpServerTimeouts struct {
	ReadHeader     time.Duration
	Read           time.Duration
	Idle           time.Duration
	MaxHeaderBytes int
}

// defaultHTTPServerTimeouts are the production boundaries.
//
// ReadHeader is the Slowloris fix: without it, a client that opens a
// connection and dribbles header bytes holds a goroutine and a file descriptor
// indefinitely, and a few hundred such connections are enough to stop the
// daemon answering.
//
// Read bounds the whole request including the body. It is generous because the
// largest legitimate body is a 100-item /v1/context/bulk-ingest batch capped at
// contextapi's 10 MiB, which is fast on any working link but slow on a bad one.
//
// Idle bounds a kept-alive connection between requests — the MCP adapter and
// the web UI both hold connections open across long stretches of user
// think-time, so this is comfortably longer than Read.
func defaultHTTPServerTimeouts() httpServerTimeouts {
	return httpServerTimeouts{
		ReadHeader:     10 * time.Second,
		Read:           60 * time.Second,
		Idle:           120 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MiB, vs net/http's 1 MB default — explicit, not inherited
	}
}

// newHTTPServer builds the daemon's listener with explicit request boundaries.
//
// WriteTimeout is deliberately NOT set, and must not be added. It is an
// absolute deadline measured from the start of the request read, and several
// routes make synchronous outbound calls or unbounded database work on the
// request path, so any value large enough to be safe for them is too large to
// be a useful defence:
//
//   - POST /v1/synthesis/ask — a full LLM completion, unbounded by us
//   - POST /v1/memory/recall, GET /v1/recall, POST /v1/tesseract/lookup —
//     synchronous embedding of the query
//   - POST /v1/memory/write, POST /v1/knowledge/write — dedup embeds inline
//   - POST /v1/context/bulk-ingest, /v1/maintenance/compact,
//     /v1/maintenance/trim, /v1/context/consistency/repair,
//     /v1/admin/queue/backfill — unbounded DB work
//
// A blanket WriteTimeout truncates those responses mid-flight and surfaces to
// the caller as a connection reset with no error body. The slow-client attack
// it would otherwise mitigate is covered by ReadHeaderTimeout and ReadTimeout
// on the way in; per-route write deadlines belong with the routes that can
// bound their own work, not on the server.
func newHTTPServer(addr string, handler http.Handler, t httpServerTimeouts) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: t.ReadHeader,
		ReadTimeout:       t.Read,
		IdleTimeout:       t.Idle,
		MaxHeaderBytes:    t.MaxHeaderBytes,
	}
}

func runServe(ctx context.Context, store *contextstore.Store, stderr *os.File, cfg serveConfig, layout paths.Layout, tesseractCfg config.Config) int {
	mem, err := setupMemorySubsystem(ctx, store, stderr, layout, tesseractCfg)
	if err != nil {
		_, _ = stderr.WriteString("error: " + err.Error() + "\n")
		return 1
	}
	defer mem.Close()

	srv := contextapi.NewServer(store, contextpolicy.New())
	srv.MemoryStore = mem.Store
	srv.KnowledgeStore = knowledge.New(mem.Store)
	srv.ManagedAuth = cfg.ManagedAuth
	srv.AuthToken = cfg.StaticToken
	srv.EnableMetrics = cfg.EnableMetrics
	srv.EnableRequestLogging = cfg.EnableRequestLogs
	srv.RequestLogMode = cfg.RequestLogMode
	srv.LogWriter = stderr
	srv.Layout = layout
	srv.ConfigFile = filepath.Join(layout.ConfigDir(), "config.yaml")
	srv.QueueDBPath = mem.QueueDBPath
	srv.QueueDB = mem.queueDB
	srv.RuntimeConfig = tesseractCfg

	// Wire LLM-backed synthesis if config + credentials are present. Failure
	// to construct the provider is non-fatal — the route just stays 503.
	if synth := createSynthesisProvider(tesseractCfg, stderr); synth != nil {
		srv.SynthesisProvider = synth
		srv.SynthesisConfig = tesseractCfg.Synthesis
		srv.ModelsDev = createModelsDevClient(ctx, stderr)
	}

	if cfg.ManagedAuth {
		ok, err := store.HasActiveAuthTokens(ctx)
		if err != nil {
			_, _ = stderr.WriteString("error: " + err.Error() + "\n")
			return 1
		}
		if !ok {
			_, _ = stderr.WriteString("error: managed auth requires at least one active token (run `tesseract context token issue ...` first)\n")
			return 1
		}
	}

	// Initialise plugin host and discover plugins.
	pluginLogger := cplugin.NewLogger("tesseract-plugin")
	pluginHost := cplugin.NewHost(http.NewServeMux(), pluginLogger)
	pluginHost.RegisterService("store", store)

	pluginsDir := "./plugins"
	if d := os.Getenv("TESSERACT_PLUGINS_DIR"); d != "" {
		pluginsDir = d
	}
	discovered, discoverErr := cplugin.DiscoverPlugins(pluginsDir)
	if discoverErr != nil {
		_, _ = stderr.WriteString("warning: plugin discovery failed: " + discoverErr.Error() + "\n")
	} else if len(discovered) > 0 {
		loaded, loadErrs := cplugin.LoadDiscovered(pluginHost, discovered)
		for _, e := range loadErrs {
			_, _ = stderr.WriteString("warning: plugin load error: " + e.Error() + "\n")
		}
		_, _ = stderr.WriteString(fmt.Sprintf("loaded %d plugin(s)\n", len(loaded)))
	}
	defer pluginHost.Shutdown()

	// Multiplex: /v1/* → API server, everything else → embedded UI
	mux := http.NewServeMux()
	mux.Handle("/v1/", srv)
	mux.Handle("/", webui.Handler())

	httpServer := newHTTPServer(cfg.Addr, propagation.HTTPMiddleware(mux), defaultHTTPServerTimeouts())
	_, _ = stderr.WriteString("Tesseract — Content Memory Service\n")
	_, _ = stderr.WriteString("  API: http://" + cfg.Addr + "/v1/\n")
	_, _ = stderr.WriteString("  UI:  http://" + cfg.Addr + "/\n")
	if cfg.AllowUnauthenticatedRemote && !cfg.authConfigured() && !isLoopbackAddr(cfg.Addr) {
		_, _ = stderr.WriteString(
			"\n" +
				"  !! WARNING: serving " + cfg.Addr + " with NO authentication.\n" +
				"  !! Every route — records, memory, knowledge, audit, and /v1/admin/* —\n" +
				"  !! is readable and writable by anyone who can reach this port.\n" +
				"  !! Configure --managed-auth or --static-token, or bind " + defaultServeAddr + ".\n\n")
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		_, _ = stderr.WriteString("tesseract shutdown requested\n")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			_, _ = stderr.WriteString("error: graceful shutdown failed: " + err.Error() + "\n")
			return 1
		}
		if err := <-errCh; err != nil && err != http.ErrServerClosed {
			_, _ = stderr.WriteString("error: " + err.Error() + "\n")
			return 1
		}
		_, _ = stderr.WriteString("tesseract shutdown complete\n")
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			_, _ = stderr.WriteString("error: " + err.Error() + "\n")
			return 1
		}
	}
	return 0
}
