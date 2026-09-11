// Package config loads the service configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	homerun "github.com/stuttgart-things/homerun-library/v4"
	"github.com/stuttgart-things/homerun-library/v4/routing"
	"k8s.io/apimachinery/pkg/labels"
)

// Environment variables read by Load.
const (
	EnvHTTPPort            = "HTTP_PORT"
	EnvNamespace           = "NAMESPACE"
	EnvPodNamespace        = "POD_NAMESPACE"
	EnvLabelSelector       = "LABEL_SELECTOR"
	EnvKubeconfig          = "KUBECONFIG"
	EnvCacheTTL            = "CACHE_TTL"
	EnvMustReactSeverities = "MUST_REACT_SEVERITIES"
)

// DefaultLabelSelector selects every homerun2 component: all of their KCL
// deployments set app.kubernetes.io/part-of=homerun2.
const DefaultLabelSelector = "app.kubernetes.io/part-of=homerun2"

// serviceAccountNamespaceFile holds the pod's namespace when running in a
// cluster.
const serviceAccountNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// Config is the service configuration, loaded once at startup.
type Config struct {
	// HTTPPort is the port the web UI, API and health endpoint listen on.
	HTTPPort string
	// Namespace is the namespace whose Deployments and ConfigMaps are read.
	Namespace string
	// NamespaceSource says where Namespace came from, for the startup log.
	NamespaceSource string
	// LabelSelector selects the homerun2 Deployments.
	LabelSelector string
	// Kubeconfig is the kubeconfig path for running outside a cluster. Empty
	// means in-cluster configuration.
	Kubeconfig string
	// CacheTTL is how long a snapshot of the namespace is reused. Zero
	// rebuilds it on every request.
	CacheTTL time.Duration
	// MustReactSeverities are the severities every catcher with rules is
	// expected to react to. Empty disables that check.
	MustReactSeverities []string
}

// Load reads the configuration from the process environment.
func Load() (Config, error) {
	return LoadFrom(os.LookupEnv, os.ReadFile)
}

// LoadFrom reads the configuration through lookup and readFile, so tests need
// neither a real environment nor a service account token.
//
// Every invalid value is reported, not only the first: fixing one variable per
// restart is slow.
func LoadFrom(
	lookup func(string) (string, bool),
	readFile func(string) ([]byte, error),
) (Config, error) {
	get := func(key, fallback string) string {
		if v, ok := lookup(key); ok {
			return v
		}
		return fallback
	}

	var errs []error
	cfg := Config{
		HTTPPort:      get(EnvHTTPPort, "8080"),
		LabelSelector: get(EnvLabelSelector, DefaultLabelSelector),
		Kubeconfig:    get(EnvKubeconfig, ""),
	}

	if port, err := strconv.Atoi(cfg.HTTPPort); err != nil || port < 1 || port > 65535 {
		errs = append(errs, fmt.Errorf("%s %q: not a port number", EnvHTTPPort, cfg.HTTPPort))
	}

	if _, err := labels.Parse(cfg.LabelSelector); err != nil {
		errs = append(errs, fmt.Errorf("%s %q: %w", EnvLabelSelector, cfg.LabelSelector, err))
	}

	ttl := get(EnvCacheTTL, "10s")
	if d, err := time.ParseDuration(strings.TrimSpace(ttl)); err != nil {
		errs = append(errs, fmt.Errorf("%s %q: %w", EnvCacheTTL, ttl, err))
	} else if d < 0 {
		errs = append(errs, fmt.Errorf("%s %q: must not be negative", EnvCacheTTL, ttl))
	} else {
		cfg.CacheTTL = d
	}

	severities, err := parseSeverities(get(EnvMustReactSeverities, "error,critical"))
	if err != nil {
		errs = append(errs, err)
	}
	cfg.MustReactSeverities = severities

	cfg.Namespace, cfg.NamespaceSource, err = resolveNamespace(lookup, readFile)
	if err != nil {
		errs = append(errs, err)
	}

	return cfg, errors.Join(errs...)
}

// parseSeverities parses a comma-separated severity list. Every entry must be
// one of routing.Severities; a typo would otherwise silently check nothing.
func parseSeverities(v string) ([]string, error) {
	var out []string
	for _, s := range strings.Split(v, ",") {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if !slices.Contains(routing.Severities, s) {
			return nil, fmt.Errorf("%s: %q is not one of %s", EnvMustReactSeverities, s, strings.Join(routing.Severities, ", "))
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out, nil
}

// resolveNamespace picks the namespace to read: NAMESPACE, then POD_NAMESPACE
// (the downward API), then the service account's namespace file. There is no
// default - reading the wrong namespace would show a confidently wrong
// picture.
func resolveNamespace(
	lookup func(string) (string, bool),
	readFile func(string) ([]byte, error),
) (namespace, source string, err error) {
	for _, key := range []string{EnvNamespace, EnvPodNamespace} {
		if v, ok := lookup(key); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), key, nil
		}
	}
	if data, readErr := readFile(serviceAccountNamespaceFile); readErr == nil {
		if ns := strings.TrimSpace(string(data)); ns != "" {
			return ns, "service account", nil
		}
	}
	return "", "", fmt.Errorf("cannot determine the namespace to read: set %s", EnvNamespace)
}

// SetupLogging configures slog as the default logger based on LOG_FORMAT and
// LOG_LEVEL, and routes homerun-library's records through it.
func SetupLogging() {
	format := strings.ToLower(homerun.GetEnv("LOG_FORMAT", "json"))

	// debug, info, warn, error (any case); anything else is info.
	var level slog.Level
	if err := level.UnmarshalText([]byte(homerun.GetEnv("LOG_LEVEL", "info"))); err != nil {
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	slog.SetDefault(slog.New(handler))
	homerun.SetLogger(slog.Default())
}
