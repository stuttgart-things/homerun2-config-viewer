//go:build cluster

package discovery

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-config-viewer/internal/config"
	"github.com/stuttgart-things/homerun2-config-viewer/internal/kube"
)

// TestDiscover_Cluster runs discovery read-only against a real cluster and
// writes what it found as JSON to DISCOVERY_OUT, or logs it:
//
//	KUBECONFIG=~/.kube/homerun2-test1 NAMESPACE=homerun2 \
//	  go test -tags cluster -run TestDiscover_Cluster -v ./internal/discovery/
func TestDiscover_Cluster(t *testing.T) {
	kubeconfig, namespace := os.Getenv("KUBECONFIG"), os.Getenv("NAMESPACE")
	if kubeconfig == "" || namespace == "" {
		t.Skip("set KUBECONFIG and NAMESPACE")
	}

	client, err := kube.NewClientset(kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	d := &Discoverer{Client: client, Namespace: namespace, LabelSelector: config.DefaultLabelSelector}
	res, err := d.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Components) == 0 {
		t.Fatalf("no Deployments matched %s in %s", config.DefaultLabelSelector, namespace)
	}

	out, err := json.MarshalIndent(struct {
		*Result
		Streams []string `json:"streams"`
	}{res, res.Streams()}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("DISCOVERY_OUT"); path != "" {
		if err := os.WriteFile(path, out, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Logf("%s", out)
}
