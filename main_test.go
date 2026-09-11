package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-config-viewer/internal/config"
	"github.com/stuttgart-things/homerun2-config-viewer/internal/handlers"
)

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func TestServe_HealthzAndShutdown(t *testing.T) {
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, ln, newMux(handlers.BuildInfo{Version: "test"}, nil)) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned %v after a clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the context was canceled")
	}
}

func TestServe_FinishesRequestsInFlight(t *testing.T) {
	ln := listen(t)
	started := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "done")
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, ln, mux) }()

	result := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			result <- "error: " + err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		result <- string(b)
	}()

	<-started
	cancel() // SIGTERM while the request is being handled

	if got := <-result; got != "done" {
		t.Errorf("in-flight request got %q, want it to finish", got)
	}
	if err := <-done; err != nil {
		t.Errorf("serve returned %v", err)
	}
}

func TestRun_ListenError(t *testing.T) {
	ln := listen(t)
	defer func() { _ = ln.Close() }()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	// Something already listens on 127.0.0.1:port; binding :port fails on
	// most systems. Skip where the kernel allows the overlap.
	var lc net.ListenConfig
	probe, err := lc.Listen(context.Background(), "tcp", ":"+port)
	if err == nil {
		_ = probe.Close()
		t.Skip("this system allows binding :port next to 127.0.0.1:port")
	}

	if err := run(context.Background(), cfgWithPort(port), handlers.BuildInfo{}); err == nil {
		t.Error("expected a listen error")
	}
}

func cfgWithPort(port string) config.Config {
	return config.Config{HTTPPort: port}
}

func TestNewAPI_ReportsAnUnavailableClient(t *testing.T) {
	cfg := config.Config{Namespace: "homerun2", Kubeconfig: filepath.Join(t.TempDir(), "missing"), CacheTTL: time.Second}
	mux := newMux(handlers.BuildInfo{}, newAPI(cfg))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/components", http.NoBody))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "load kubeconfig") {
		t.Errorf("API without a client: %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz must not depend on the Kubernetes client: %d", rec.Code)
	}
}
