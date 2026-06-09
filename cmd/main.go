package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
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
	var (
		transport     string
		addr          string
		kubeconfig    string
		kubeContext   string
		envoyAdminURL string
		contourNs     string
		showVersion   bool
	)

	flag.StringVar(&transport, "transport", "stdio", "Transport mode: stdio, streamable-http")
	flag.StringVar(&addr, "addr", ":8080", "Listen address for HTTP transport")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (defaults to in-cluster config)")
	flag.StringVar(&kubeContext, "context", "", "Kubernetes context to use from kubeconfig")
	flag.StringVar(&envoyAdminURL, "envoy-admin-url", "", "Envoy admin API base URL (e.g. http://envoy.projectcontour:9001)")
	flag.StringVar(&contourNs, "contour-namespace", "projectcontour", "Default namespace for Contour resources")
	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("contour-envoy-mcp %s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	}

	log.Printf("contour-envoy-mcp %s starting (transport=%s)", version, transport)

	// Initialize Kubernetes client
	k8sClient, err := k8s.NewClient(kubeconfig, kubeContext)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}
	log.Println("Kubernetes client initialized")

	// Initialize Contour client
	contourClient := contour.NewClient(k8sClient.DynamicClient(), contourNs)
	log.Println("Contour client initialized")

	// Initialize Envoy admin client
	envoyClient := envoy.NewAdminClient(envoyAdminURL)
	if envoyAdminURL != "" {
		log.Printf("Envoy admin client initialized (url=%s)", envoyAdminURL)
	} else {
		log.Println("Envoy admin client initialized (no explicit URL, will use per-request overrides)")
	}

	// Create MCP server
	mcpServer := server.NewMCPServer(
		"contour-envoy-mcp",
		version,
		server.WithToolCapabilities(true),
	)

	// Register all tools
	registry := tools.NewRegistry(contourClient, envoyClient)
	if err := registry.RegisterAll(mcpServer); err != nil {
		log.Fatalf("Failed to register tools: %v", err)
	}
	log.Printf("Registered %d MCP tools", registry.ToolCount())

	// Start server with chosen transport
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	switch transport {
	case "stdio":
		serveStdio(mcpServer, ctx)
	case "streamable-http":
		serveHTTP(mcpServer, addr, ctx)
	default:
		log.Fatalf("Unknown transport: %s (use stdio or streamable-http)", transport)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("Shutting down...")
	cancel()
}

func serveStdio(mcpServer *server.MCPServer, ctx context.Context) {
	stdioServer := server.NewStdioServer(mcpServer)
	go func() {
		if err := stdioServer.Listen(ctx, os.Stdin, os.Stdout); err != nil {
			log.Fatalf("Stdio server error: %v", err)
		}
	}()
	log.Println("Serving on stdio")
}

func serveHTTP(mcpServer *server.MCPServer, addr string, ctx context.Context) {
	streamable := server.NewStreamableHTTPServer(mcpServer)

	// health is a shared handler for liveness/readiness probes. The process is
	// considered healthy as soon as the HTTP listener is serving, so a plain
	// 200 is sufficient and avoids tripping probes during Kubernetes startup.
	health := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok") //nolint:errcheck
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
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	go func() {
		log.Printf("Serving on %s (streamable HTTP, healthz on /healthz)", addr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()
}
