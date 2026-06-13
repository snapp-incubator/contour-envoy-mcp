package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/snapp-incubator/contour-envoy-mcp/internal/contour"
	"github.com/snapp-incubator/contour-envoy-mcp/internal/envoy"
	"github.com/snapp-incubator/contour-envoy-mcp/internal/k8s"
	"github.com/snapp-incubator/contour-envoy-mcp/internal/tools"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		transport           string
		addr                string
		kubeconfig          string
		kubeContext         string
		envoyAdminURL       string
		defaultIngressClass string
		contourNs           string
		logLevel            string
		showVersion         bool
	)

	flag.StringVar(&transport, "transport", "stdio", "Transport mode: stdio, streamable-http")
	flag.StringVar(&addr, "addr", ":8080", "Listen address for HTTP transport")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (defaults to in-cluster config)")
	flag.StringVar(&kubeContext, "context", "", "Kubernetes context to use from kubeconfig")
	flag.StringVar(&envoyAdminURL, "envoy-admin-url", "", "Direct Envoy admin API base URL (advanced; the admin listener is normally localhost-bound and reached via port-forward)")
	flag.StringVar(&defaultIngressClass, "default-ingress-class", "", "Default Envoy ingress class targeted when a tool call passes no ingress_class/pod/envoy_url (e.g. public)")
	flag.StringVar(&contourNs, "contour-namespace", "projectcontour", "Default namespace for Contour resources")
	flag.StringVar(&logLevel, "log-level", "info", "Log level: debug, info, warn, error")
	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("contour-envoy-mcp %s (commit: %s, built: %s)\n", version, commit, date)
		return nil
	}

	setupLogger(logLevel)
	slog.Info("starting", "service", "contour-envoy-mcp", "version", version, "transport", transport)

	// Root context cancelled on SIGINT/SIGTERM so both transports shut down
	// gracefully (OpenShift sends SIGTERM on pod termination).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	k8sClient, err := k8s.NewClient(kubeconfig, kubeContext)
	if err != nil {
		return fmt.Errorf("create kubernetes client: %w", err)
	}
	slog.Info("kubernetes client initialized")

	contourClient := contour.NewClient(k8sClient.DynamicClient(), contourNs)
	// k8sClient tunnels port-forwards to the localhost-bound Envoy admin
	// listener and Contour debug server inside the pods.
	contourClient.SetForwarder(k8sClient)
	envoyClient := envoy.NewAdminClient(envoyAdminURL, k8sClient)
	slog.Info("clients initialized", "contourNamespace", contourNs, "envoyAdminURL", envoyAdminURL, "defaultIngressClass", defaultIngressClass)

	mcpServer := server.NewMCPServer(
		"contour-envoy-mcp",
		version,
		server.WithToolCapabilities(true),
	)

	registry := tools.NewRegistry(contourClient, envoyClient, k8sClient, contourNs)
	registry.SetDefaultIngressClass(defaultIngressClass)
	if err := registry.RegisterAll(mcpServer); err != nil {
		return fmt.Errorf("register tools: %w", err)
	}
	slog.Info("tools registered", "count", registry.ToolCount())

	switch transport {
	case "stdio":
		return serveStdio(ctx, mcpServer)
	case "streamable-http":
		return serveHTTP(ctx, mcpServer, addr)
	default:
		return fmt.Errorf("unknown transport %q (use stdio or streamable-http)", transport)
	}
}

// setupLogger configures the default structured logger. Output goes to stderr
// because in stdio transport mode stdout is the MCP protocol channel and must
// not be polluted with log lines.
func setupLogger(level string) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	slog.SetDefault(slog.New(handler))
}

func serveStdio(ctx context.Context, mcpServer *server.MCPServer) error {
	slog.Info("serving on stdio")
	stdioServer := server.NewStdioServer(mcpServer)

	errCh := make(chan error, 1)
	go func() { errCh <- stdioServer.Listen(ctx, os.Stdin, os.Stdout) }()

	select {
	case <-ctx.Done():
		slog.Info("shutting down (signal received)")
		return nil
	case err := <-errCh:
		// EOF / context cancellation are normal stream terminations.
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
			return fmt.Errorf("stdio server: %w", err)
		}
		return nil
	}
}

func serveHTTP(ctx context.Context, mcpServer *server.MCPServer, addr string) error {
	streamable := server.NewStreamableHTTPServer(mcpServer)

	// health is a shared handler for startup/liveness/readiness probes. The
	// process is healthy as soon as the listener is serving, so a plain 200
	// suffices and never trips probes during Kubernetes startup.
	health := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", streamable)
	mux.HandleFunc("/healthz", health)
	mux.HandleFunc("/readyz", health)
	mux.HandleFunc("/livez", health)

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
		// ReadHeaderTimeout guards against slow-loris clients (gosec G114).
		// No Read/WriteTimeout: MCP streamable HTTP uses long-lived SSE
		// streams that fixed write deadlines would sever.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("serving", "addr", addr, "transport", "streamable-http", "healthPath", "/healthz")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		slog.Info("shutting down (signal received)")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	}
}
