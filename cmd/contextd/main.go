package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/vanta-conduit/internal/config"
	"github.com/hollis-labs/vanta-conduit/internal/contextapi"
	"github.com/hollis-labs/vanta-conduit/internal/contextcli"
	"github.com/hollis-labs/vanta-conduit/internal/contextpolicy"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
	"github.com/hollis-labs/vanta-conduit/internal/knowledge"
	"github.com/hollis-labs/vanta-conduit/internal/mcpadapter"
	cplugin "github.com/hollis-labs/vanta-conduit/internal/plugin"
	"github.com/hollis-labs/vanta-conduit/internal/webui"
	feotel "github.com/hollis-labs/go-otel"
	"github.com/hollis-labs/go-otel/propagation"
	_ "modernc.org/sqlite"
)

type serveConfig struct {
	Addr              string
	ManagedAuth       bool
	StaticToken       string
	EnableMetrics     bool
	EnableRequestLogs bool
	RequestLogMode    string
	ShutdownTimeout   time.Duration
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr *os.File) int {
	shutdown, err := feotel.Init(ctx, feotel.WithServiceName("conduit"))
	if err != nil {
		log.Printf("warning: OTel init failed: %v", err)
	} else {
		defer shutdown(ctx)
	}

	root := os.Getenv("CONTEXTD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			_, _ = stderr.WriteString("error: cannot determine home directory: " + err.Error() + "\n")
			return 1
		}
		root = filepath.Join(home, ".conduit")
	}

	conduitCfg, cfgErr := config.Load(filepath.Join(root, "config.yaml"))
	if cfgErr != nil {
		log.Printf("warning: config load failed: %v (using defaults)", cfgErr)
		conduitCfg = config.Defaults()
	}

	store, err := contextstore.Open(ctx, contextstore.Config{
		RootDir:    root,
		RecordsDir: filepath.Join(root, "data", "records"),
		DBPath:     filepath.Join(root, "data", "index", "context.db"),
	})
	if err != nil {
		_, _ = stderr.WriteString("error: " + err.Error() + "\n")
		return 1
	}
	defer store.Close()

	if len(args) > 0 && args[0] == "serve" {
		cfg, err := parseServeArgs(args[1:])
		if err != nil {
			_, _ = stderr.WriteString("error: " + err.Error() + "\n")
			return 1
		}
		return runServe(ctx, store, stderr, cfg, root, conduitCfg)
	}

	if len(args) > 0 && args[0] == "plugin" {
		return contextcli.RunPluginCmd(args[1:], stdout, stderr)
	}

	if len(args) > 0 && args[0] == "mcp" {
		token, err := parseMCPArgs(args[1:])
		if err != nil {
			_, _ = stderr.WriteString("error: " + err.Error() + "\n")
			return 1
		}
		return runMCP(ctx, store, stderr, token, root, conduitCfg)
	}

	if len(args) > 0 && args[0] == "backfill-embeddings" {
		return runBackfill(ctx, store, conduitCfg, args[1:], stdout, stderr)
	}

	cli := &contextcli.CLI{
		Store:  store,
		Policy: contextpolicy.New(),
		Stdout: stdout,
		Stderr: stderr,
	}
	return cli.Run(ctx, args)
}

func parseServeArgs(args []string) (serveConfig, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", ":8089", "listen address")
	managed := fs.Bool("managed-auth", false, "enable managed token auth")
	static := fs.String("static-token", "", "static bearer token (legacy mode)")
	metrics := fs.Bool("metrics", false, "enable /v1/metrics endpoint")
	requestLogs := fs.Bool("request-logs", false, "enable structured request logs")
	requestLogMode := fs.String("request-log-mode", "redacted", "request log mode: redacted|full")
	shutdownTimeout := fs.Duration("shutdown-timeout", 5*time.Second, "graceful shutdown timeout")
	if err := fs.Parse(args); err != nil {
		return serveConfig{}, err
	}
	if *managed && strings.TrimSpace(*static) != "" {
		return serveConfig{}, fmt.Errorf("managed-auth and static-token are mutually exclusive")
	}
	mode := strings.ToLower(strings.TrimSpace(*requestLogMode))
	if mode != "redacted" && mode != "full" {
		return serveConfig{}, fmt.Errorf("request-log-mode must be one of: redacted, full")
	}
	return serveConfig{
		Addr:              *addr,
		ManagedAuth:       *managed,
		StaticToken:       strings.TrimSpace(*static),
		EnableMetrics:     *metrics,
		EnableRequestLogs: *requestLogs,
		RequestLogMode:    mode,
		ShutdownTimeout:   *shutdownTimeout,
	}, nil
}

func parseMCPArgs(args []string) (string, error) {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	token := fs.String("token", "", "capability token for mutating MCP tools")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	return strings.TrimSpace(*token), nil
}

// createEmbedder builds an Embedder from config. Returns nil if the provider
// is unsupported or the required API key is missing. Provider-specific
// credential handling is delegated to the provider constructor (e.g.,
// NewOpenAI reads OPENAI_API_KEY from the environment).
// createSynthesisProvider builds the go-providers Provider used by
// /v1/synthesis/ask. Returns nil (and the route degrades to 503) when:
//   - no synthesis.provider is configured
//   - the provider name is unsupported
//   - the provider's API key env var is empty
//
// Mirrors the createEmbedder shape: silent nil on missing creds, log on
// the fallback so operators see why the feature is off.
func createSynthesisProvider(cfg config.Config, stderr *os.File) provider.Provider {
	name := strings.ToLower(strings.TrimSpace(cfg.Synthesis.Provider))
	if name == "" {
		return nil
	}
	envFor := map[string]string{
		"openai":    "OPENAI_API_KEY",
		"anthropic": "ANTHROPIC_API_KEY",
		"gemini":    "GOOGLE_API_KEY",
		"mistral":   "MISTRAL_API_KEY",
	}
	if env, ok := envFor[name]; ok && os.Getenv(env) == "" {
		_, _ = stderr.WriteString("warning: synthesis.provider=" + name + " but " + env + " not set — /v1/synthesis/ask disabled\n")
		return nil
	}
	switch name {
	case "openai":
		return provider.NewOpenAI()
	case "anthropic":
		return provider.NewAnthropic()
	case "gemini":
		return provider.NewGemini()
	case "mistral":
		return provider.NewMistral()
	default:
		_, _ = stderr.WriteString("warning: synthesis.provider=" + name + " is not supported — /v1/synthesis/ask disabled\n")
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

func createEmbedder(cfg config.Config) provider.Embedder {
	if cfg.Embedding.Provider != "openai" {
		return nil
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Printf("warning: embedding.provider=openai but OPENAI_API_KEY not set — embedding disabled, falling back to BM25-only recall")
		return nil
	}
	return provider.NewOpenAI()
}

func runMCP(ctx context.Context, store *contextstore.Store, stderr *os.File, token string, root string, conduitCfg config.Config) int {
	_, _ = stderr.WriteString("Vanta Conduit MCP adapter starting (stdio)\n")

	mem, err := setupMemorySubsystem(ctx, store, stderr, root, conduitCfg)
	if err != nil {
		_, _ = stderr.WriteString("error: " + err.Error() + "\n")
		return 1
	}
	defer mem.Close()

	adapter := mcpadapter.New(store, token)
	adapter.MemoryStore = mem.Store
	adapter.KnowledgeStore = knowledge.New(mem.Store)
	if err := adapter.Run(ctx); err != nil {
		_, _ = stderr.WriteString("error: " + err.Error() + "\n")
		return 1
	}
	return 0
}

func runServe(ctx context.Context, store *contextstore.Store, stderr *os.File, cfg serveConfig, root string, conduitCfg config.Config) int {
	mem, err := setupMemorySubsystem(ctx, store, stderr, root, conduitCfg)
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

	// Wire LLM-backed synthesis if config + credentials are present. Failure
	// to construct the provider is non-fatal — the route just stays 503.
	if synth := createSynthesisProvider(conduitCfg, stderr); synth != nil {
		srv.SynthesisProvider = synth
		srv.SynthesisConfig = conduitCfg.Synthesis
		srv.ModelsDev = createModelsDevClient(ctx, stderr)
	}

	if cfg.ManagedAuth {
		ok, err := store.HasActiveAuthTokens(ctx)
		if err != nil {
			_, _ = stderr.WriteString("error: " + err.Error() + "\n")
			return 1
		}
		if !ok {
			_, _ = stderr.WriteString("error: managed auth requires at least one active token (run `contextd context token issue ...` first)\n")
			return 1
		}
	}

	// Initialise plugin host and discover plugins.
	pluginLogger := cplugin.NewLogger("conduit-plugin")
	pluginHost := cplugin.NewHost(http.NewServeMux(), pluginLogger)
	pluginHost.RegisterService("store", store)

	pluginsDir := "./plugins"
	if d := os.Getenv("CONDUIT_PLUGINS_DIR"); d != "" {
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

	httpServer := &http.Server{Addr: cfg.Addr, Handler: propagation.HTTPMiddleware(mux)}
	_, _ = stderr.WriteString("Vanta Conduit — Content Memory Service\n")
	_, _ = stderr.WriteString("  API: http://" + cfg.Addr + "/v1/\n")
	_, _ = stderr.WriteString("  UI:  http://" + cfg.Addr + "/\n")
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		_, _ = stderr.WriteString("conduit shutdown requested\n")
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
		_, _ = stderr.WriteString("conduit shutdown complete\n")
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			_, _ = stderr.WriteString("error: " + err.Error() + "\n")
			return 1
		}
	}
	return 0
}
