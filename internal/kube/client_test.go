package kube

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const kubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://127.0.0.1:6443
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: not-a-real-token
`

func TestNewClientset_Kubeconfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := restConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "https://127.0.0.1:6443" {
		t.Errorf("host = %q", cfg.Host)
	}

	if _, err := NewClientset(path); err != nil {
		t.Errorf("NewClientset: %v", err)
	}
}

func TestNewClientset_Errors(t *testing.T) {
	if _, err := NewClientset(filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "load kubeconfig") {
		t.Errorf("missing kubeconfig: err = %v", err)
	}

	// Outside a cluster the in-cluster config is unavailable; the error must
	// say how to run locally.
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	if _, err := NewClientset(""); err == nil || !strings.Contains(err.Error(), "set KUBECONFIG") {
		t.Errorf("in-cluster outside a cluster: err = %v", err)
	}
}
