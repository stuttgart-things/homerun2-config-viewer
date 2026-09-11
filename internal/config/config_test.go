package config

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"
)

func env(vars map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	}
}

func noFile(string) ([]byte, error) { return nil, fs.ErrNotExist }

func TestLoadFrom_Defaults(t *testing.T) {
	cfg, err := LoadFrom(env(map[string]string{"NAMESPACE": "homerun2"}), noFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPPort != "8080" {
		t.Errorf("HTTPPort = %q", cfg.HTTPPort)
	}
	if cfg.LabelSelector != DefaultLabelSelector {
		t.Errorf("LabelSelector = %q", cfg.LabelSelector)
	}
	if cfg.CacheTTL != 10*time.Second {
		t.Errorf("CacheTTL = %v", cfg.CacheTTL)
	}
	if !slices.Equal(cfg.MustReactSeverities, []string{"error", "critical"}) {
		t.Errorf("MustReactSeverities = %v", cfg.MustReactSeverities)
	}
	if cfg.Kubeconfig != "" {
		t.Errorf("Kubeconfig = %q", cfg.Kubeconfig)
	}
}

func TestLoadFrom_Overrides(t *testing.T) {
	cfg, err := LoadFrom(env(map[string]string{
		"NAMESPACE":             "homerun2-test1",
		"HTTP_PORT":             "9090",
		"LABEL_SELECTOR":        "app.kubernetes.io/part-of=homerun2,team in (a,b)",
		"KUBECONFIG":            "/home/me/.kube/test1",
		"CACHE_TTL":             "0",
		"MUST_REACT_SEVERITIES": " Warning, error ,error,",
	}), noFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPPort != "9090" || cfg.Kubeconfig != "/home/me/.kube/test1" || cfg.CacheTTL != 0 {
		t.Errorf("got %+v", cfg)
	}
	if !slices.Equal(cfg.MustReactSeverities, []string{"warning", "error"}) {
		t.Errorf("MustReactSeverities = %v, want lower-cased and de-duplicated", cfg.MustReactSeverities)
	}
}

func TestLoadFrom_EmptySeveritiesDisableTheCheck(t *testing.T) {
	cfg, err := LoadFrom(env(map[string]string{"NAMESPACE": "ns", "MUST_REACT_SEVERITIES": ""}), noFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.MustReactSeverities) != 0 {
		t.Errorf("MustReactSeverities = %v", cfg.MustReactSeverities)
	}
}

func TestLoadFrom_Namespace(t *testing.T) {
	saFile := func(content string) func(string) ([]byte, error) {
		return func(path string) ([]byte, error) {
			if path != serviceAccountNamespaceFile {
				t.Errorf("read unexpected file %s", path)
			}
			return []byte(content), nil
		}
	}

	cases := []struct {
		name       string
		vars       map[string]string
		readFile   func(string) ([]byte, error)
		want, from string
	}{
		{"NAMESPACE wins", map[string]string{"NAMESPACE": "a", "POD_NAMESPACE": "b"}, saFile("c"), "a", "NAMESPACE"},
		{"POD_NAMESPACE next", map[string]string{"POD_NAMESPACE": "b"}, saFile("c"), "b", "POD_NAMESPACE"},
		{"blank NAMESPACE is skipped", map[string]string{"NAMESPACE": " ", "POD_NAMESPACE": "b"}, noFile, "b", "POD_NAMESPACE"},
		{"service account file", nil, saFile("c\n"), "c", "service account"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadFrom(env(tc.vars), tc.readFile)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Namespace != tc.want || cfg.NamespaceSource != tc.from {
				t.Errorf("namespace = %q from %q, want %q from %q", cfg.Namespace, cfg.NamespaceSource, tc.want, tc.from)
			}
		})
	}

	if _, err := LoadFrom(env(nil), noFile); err == nil || !strings.Contains(err.Error(), "set NAMESPACE") {
		t.Errorf("no namespace anywhere must be an error, got %v", err)
	}
}

func TestLoadFrom_Invalid(t *testing.T) {
	cases := map[string]map[string]string{
		"port not a number":   {"HTTP_PORT": "http"},
		"port out of range":   {"HTTP_PORT": "70000"},
		"bad label selector":  {"LABEL_SELECTOR": "app in (a"},
		"ttl not a duration":  {"CACHE_TTL": "10"},
		"negative ttl":        {"CACHE_TTL": "-1s"},
		"unknown severity":    {"MUST_REACT_SEVERITIES": "error,fatal"},
		"severity with typos": {"MUST_REACT_SEVERITIES": "critcal"},
	}
	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			vars["NAMESPACE"] = "ns"
			if _, err := LoadFrom(env(vars), noFile); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestLoadFrom_ReportsEveryError(t *testing.T) {
	_, err := LoadFrom(env(map[string]string{"HTTP_PORT": "x", "CACHE_TTL": "y"}), noFile)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"HTTP_PORT", "CACHE_TTL", "NAMESPACE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) || len(joined.Unwrap()) != 3 {
		t.Errorf("want three joined errors, got %v", err)
	}
}
