// homerun2-config-viewer shows which homerun2 alert triggers what in which
// catcher, from the Deployments and ConfigMaps of one namespace.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/stuttgart-things/homerun2-config-viewer/internal/banner"
	"github.com/stuttgart-things/homerun2-config-viewer/internal/config"
	"github.com/stuttgart-things/homerun2-config-viewer/internal/handlers"
)

// Set by ldflags at build time (see .ko.yaml).
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 15 * time.Second
)

func main() {
	banner.Show()
	config.SetupLogging()

	slog.Info("starting homerun2-config-viewer",
		"version", version,
		"commit", commit,
		"date", date,
		"go", runtime.Version(),
	)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	slog.Info("configuration loaded",
		"namespace", cfg.Namespace,
		"namespace_from", cfg.NamespaceSource,
		"label_selector", cfg.LabelSelector,
		"cache_ttl", cfg.CacheTTL.String(),
		"must_react_severities", cfg.MustReactSeverities,
		"in_cluster", cfg.Kubeconfig == "",
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	err = run(ctx, cfg, handlers.BuildInfo{Version: version, Commit: commit, Date: date})
	stop()
	if err != nil {
		slog.Error("http server failed", "error", err)
		os.Exit(1)
	}
	slog.Info("stopped")
}

// run serves the HTTP endpoints on cfg.HTTPPort until ctx is done.
func run(ctx context.Context, cfg config.Config, info handlers.BuildInfo) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", ":"+cfg.HTTPPort)
	if err != nil {
		return fmt.Errorf("listen on :%s: %w", cfg.HTTPPort, err)
	}
	return serve(ctx, ln, newMux(info))
}

func newMux(info handlers.BuildInfo) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handlers.NewHealthHandler(info))
	return mux
}

// serve serves handler on ln until ctx is done, then shuts down gracefully:
// requests in flight get up to shutdownTimeout to finish.
func serve(ctx context.Context, ln net.Listener, handler http.Handler) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	slog.Info("http server listening", "addr", ln.Addr().String())

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
