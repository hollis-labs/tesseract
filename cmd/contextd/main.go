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

	"github.com/hollis-labs/cortex/internal/contextapi"
	"github.com/hollis-labs/cortex/internal/contextcli"
	"github.com/hollis-labs/cortex/internal/contextpolicy"
	"github.com/hollis-labs/cortex/internal/contextstore"
	"github.com/hollis-labs/cortex/internal/mcpadapter"
	"github.com/hollis-labs/cortex/internal/memory"
	cplugin "github.com/hollis-labs/cortex/internal/plugin"
	"github.com/hollis-labs/cortex/internal/webui"
	feotel "github.com/hollis-labs/otel"
	"github.com/hollis-labs/otel/propagation"
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
	shutdown, err := feotel.Init(ctx, feotel.WithServiceName("cortex"))
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
		root = filepath.Join(home, ".cortex")
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
		return runServe(ctx, store, stderr, cfg)
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
		return runMCP(ctx, store, stderr, token)
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

func runMCP(ctx context.Context, store *contextstore.Store, stderr *os.File, token string) int {
	_, _ = stderr.WriteString("Cortex MCP adapter starting (stdio)\n")

	// Memory subsystem (D-core). Shares contextstore's *sql.DB.
	memStore := memory.NewStore(store.DB(), nil, memory.NoopQueue{})

	// Start decay job.
	decayInterval := 1 * time.Hour
	if v := os.Getenv("CORTEX_MEMORY_DECAY_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			decayInterval = d
		} else {
			log.Print("warning: invalid CORTEX_MEMORY_DECAY_INTERVAL, using default 1h")
		}
	}
	decayJob := &memory.DecayJob{
		Store:    memStore,
		Interval: decayInterval,
		Logger:   log.Printf,
	}
	go decayJob.Run(ctx)

	adapter := mcpadapter.New(store, token)
	adapter.MemoryStore = memStore
	if err := adapter.Run(ctx); err != nil {
		_, _ = stderr.WriteString("error: " + err.Error() + "\n")
		return 1
	}
	return 0
}

func runServe(ctx context.Context, store *contextstore.Store, stderr *os.File, cfg serveConfig) int {
	srv := contextapi.NewServer(store, contextpolicy.New())
	srv.ManagedAuth = cfg.ManagedAuth
	srv.AuthToken = cfg.StaticToken
	srv.EnableMetrics = cfg.EnableMetrics
	srv.EnableRequestLogging = cfg.EnableRequestLogs
	srv.RequestLogMode = cfg.RequestLogMode
	srv.LogWriter = stderr

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
	pluginLogger := cplugin.NewLogger("cortex-plugin")
	pluginHost := cplugin.NewHost(http.NewServeMux(), pluginLogger)
	pluginHost.RegisterService("store", store)

	pluginsDir := "./plugins"
	if d := os.Getenv("CORTEX_PLUGINS_DIR"); d != "" {
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
	_, _ = stderr.WriteString("Cortex — Content Memory Service\n")
	_, _ = stderr.WriteString("  API: http://" + cfg.Addr + "/v1/\n")
	_, _ = stderr.WriteString("  UI:  http://" + cfg.Addr + "/\n")
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		_, _ = stderr.WriteString("cortex shutdown requested\n")
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
		_, _ = stderr.WriteString("cortex shutdown complete\n")
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			_, _ = stderr.WriteString("error: " + err.Error() + "\n")
			return 1
		}
	}
	return 0
}
