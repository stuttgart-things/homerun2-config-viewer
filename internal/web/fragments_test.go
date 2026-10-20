package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stuttgart-things/homerun2-config-viewer/internal/fixture"
)

func TestManifest(t *testing.T) {
	rec := get(t, newMuxAt(t, stubSnapshots{}, "/config"), "/ui/manifest.json")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var m Manifest
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m.Title != "Config Viewer" || m.Version != "1.2.3" || m.BasePath != "/config" || len(m.Fragments) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	if f := m.Fragments[0]; f.ID != "findings" || f.Title != "Findings" || f.URL != "/config/ui/fragment/findings" || f.RefreshSeconds < 2 {
		t.Errorf("fragment = %+v", f)
	}
}

// fragment fetches the findings fragment; it is always a 200, as htmx swaps
// nothing else.
func fragment(t *testing.T, snaps Snapshotter, basePath string) string {
	t.Helper()
	rec := get(t, newMuxAt(t, snaps, basePath), "/ui/fragment/findings")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, bare := range []string{"<html", "<head", "<script", "<body"} {
		if strings.Contains(body, bare) {
			t.Fatalf("a fragment is bare HTML, found %s:\n%s", bare, body)
		}
	}
	return body
}

func TestFindingsFragment(t *testing.T) {
	res, err := fixture.Mixup()
	body := fragment(t, of(t, res, err), "/config")

	contains(t, body,
		`<div class="hr-frag">`, "2 findings",
		`<span class="hr-sev hr-sev-error">unread-stream</span><span class="hr-title">`,
		`<span class="hr-sev hr-sev-info">scaled-to-zero</span>`,
		`<a class="hr-frag-more" href="/config/">Open viewer →</a>`,
	)
	lacks(t, body, "No findings", "more</li>")
	if strings.Index(body, "unread-stream") > strings.Index(body, "scaled-to-zero") {
		t.Error("the worst finding must come first")
	}
}

func TestFindingsFragment_CapsAndEscapes(t *testing.T) {
	core := fixture.Deployment("core", "consumer", "img/homerun2-core-catcher:1", 1, nil)
	res, err := fixture.Discover(
		core,
		fixture.Deployment("p0", "api", "img/homerun2-omni-pitcher:1", 1, map[string]string{"REDIS_STREAM": "<b>s0</b>"}),
		fixture.Deployment("p1", "api", "img/homerun2-omni-pitcher:1", 1, map[string]string{"REDIS_STREAM": "s1"}),
		fixture.Deployment("p2", "api", "img/homerun2-omni-pitcher:1", 1, map[string]string{"REDIS_STREAM": "s2"}),
		fixture.Deployment("p3", "api", "img/homerun2-omni-pitcher:1", 1, map[string]string{"REDIS_STREAM": "s3"}),
		fixture.Deployment("p4", "api", "img/homerun2-omni-pitcher:1", 1, map[string]string{"REDIS_STREAM": "s4"}),
		fixture.Deployment("p5", "api", "img/homerun2-omni-pitcher:1", 1, map[string]string{"REDIS_STREAM": "s5"}),
		fixture.Deployment("p6", "api", "img/homerun2-omni-pitcher:1", 1, map[string]string{"REDIS_STREAM": "s6"}),
		fixture.Deployment("p7", "api", "img/homerun2-omni-pitcher:1", 1, map[string]string{"REDIS_STREAM": "s7"}),
	)
	body := fragment(t, of(t, res, err), "")

	n := len(res.Findings([]string{"error", "critical"}))
	if n <= findingsFragmentLimit {
		t.Fatalf("fixture has %d findings, want more than %d", n, findingsFragmentLimit)
	}
	contains(t, body,
		fmt.Sprintf("%d findings", n),
		fmt.Sprintf(`<li class="hr-item hr-muted">+%d more</li>`, n-findingsFragmentLimit),
		"&lt;b&gt;s0&lt;/b&gt;", // stream names come from the cluster and are escaped
		`href="/"`,
	)
	lacks(t, body, "<b>s0</b>")
	if got := strings.Count(body, `<li class="hr-item"><span class="hr-sev`); got != findingsFragmentLimit {
		t.Errorf("%d findings shown, want %d", got, findingsFragmentLimit)
	}
}

func TestFindingsFragment_NoFindings(t *testing.T) {
	res, err := fixture.Discover(
		fixture.Deployment("omni", "api", "img/homerun2-omni-pitcher:1", 1, map[string]string{"REDIS_STREAM": "messages"}),
		fixture.Deployment("core", "consumer", "img/homerun2-core-catcher:1", 1, nil),
	)
	body := fragment(t, of(t, res, err), "/config")
	contains(t, body, "0 findings", `<li class="hr-item hr-muted">No findings</li>`, `href="/config/"`)
	lacks(t, body, "hr-sev", "more</li>")
}

func TestFindingsFragment_SnapshotError(t *testing.T) {
	body := fragment(t, stubSnapshots{err: errors.New(`deployments.apps is forbidden: <b>User</b>`)}, "/config")
	contains(t, body, "Cannot read the namespace: deployments.apps is forbidden", "&lt;b&gt;User&lt;/b&gt;", `href="/config/"`)
	lacks(t, body, "<b>User</b>", "No findings")
}

func TestHRSeverity(t *testing.T) {
	for level, want := range map[string]string{levelError: "error", levelWarn: "warning", levelInfo: "info", "": "info"} {
		if got := hrSeverity(level); got != want {
			t.Errorf("hrSeverity(%q) = %q, want %q", level, got, want)
		}
	}
}
