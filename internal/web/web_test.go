package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-config-viewer/internal/discovery"
	"github.com/stuttgart-things/homerun2-config-viewer/internal/fixture"
	"github.com/stuttgart-things/homerun2-config-viewer/internal/handlers"
	"github.com/stuttgart-things/homerun2-config-viewer/internal/snapshot"
)

var takenAt = time.Date(2026, 9, 11, 14, 0, 0, 0, time.UTC)

type stubSnapshots struct {
	snap snapshot.Snapshot
	err  error
}

func (s stubSnapshots) Get(context.Context) (snapshot.Snapshot, error) { return s.snap, s.err }

func of(t *testing.T, res *discovery.Result, err error) stubSnapshots {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return stubSnapshots{snap: snapshot.Snapshot{Result: res, TakenAt: takenAt}}
}

func newMux(t *testing.T, snaps Snapshotter) *http.ServeMux {
	t.Helper()
	s, err := New(snaps, Options{
		Namespace:           fixture.Namespace,
		LabelSelector:       fixture.LabelSelector,
		MustReactSeverities: []string{"error", "critical"},
		Build:               handlers.BuildInfo{Version: "1.2.3", Commit: "0123456789abcdef", Date: "2026-09-11"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.now = func() time.Time { return takenAt.Add(42 * time.Second) }
	mux := http.NewServeMux()
	s.Register(mux)
	return mux
}

func mixupMux(t *testing.T) *http.ServeMux {
	t.Helper()
	res, err := fixture.Mixup()
	return newMux(t, of(t, res, err))
}

func get(t *testing.T, mux http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, http.NoBody))
	return rec
}

// postDryRun submits the dry-run form, as htmx or as a plain form post.
func postDryRun(t *testing.T, mux http.Handler, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/dryrun", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func htmlOf(t *testing.T, rec *httptest.ResponseRecorder, status int) string {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d\n%s", rec.Code, status, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	return rec.Body.String()
}

func contains(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("page does not contain %q", w)
		}
	}
}

func lacks(t *testing.T, body string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(body, u) {
			t.Errorf("page contains %q", u)
		}
	}
}

func TestOverview(t *testing.T) {
	body := htmlOf(t, get(t, mixupMux(t), "/"), http.StatusOK)

	contains(t, body,
		"<title>Overview", `aria-current="page">Overview`,
		"Snapshot of <strong>homerun2</strong>", "taken 42s ago",
		// findings, the worst first
		"Findings <small>(2)</small>", "unread-stream", "which no catcher reads", "scaled-to-zero",
		// streams: homerun is read by nobody
		`<tr class="unread">`, "nobody reads this stream", `href="/matrix?stream=messages"`, `href="/dryrun?stream=homerun"`,
		// components
		"<strong>light</strong>", `profile-ok`, "light-profile / profile.yaml", "<strong>stopped</strong>",
		// footer: short commit
		"1.2.3", "0123456", "2026-09-11",
	)
	lacks(t, body, "0123456789abcdef", "Cannot read the namespace")

	if strings.Index(body, "unread-stream") > strings.Index(body, "scaled-to-zero") {
		t.Error("unread-stream must be listed before scaled-to-zero")
	}
}

func TestOverview_NoFindings(t *testing.T) {
	res, err := fixture.Discover(
		fixture.Deployment("omni", "api", "img/homerun2-omni-pitcher:1", 1, map[string]string{"REDIS_STREAM": "messages"}),
		fixture.Deployment("core", "consumer", "img/homerun2-core-catcher:1", 1, nil),
	)
	body := htmlOf(t, get(t, newMux(t, of(t, res, err)), "/"), http.StatusOK)
	contains(t, body, "No findings", "reacts to error, critical")
	lacks(t, body, `class="unread"`)
}

func TestOverview_Routes(t *testing.T) {
	res, err := fixture.Discover(
		fixture.Deployment("omni", "api", "img/homerun2-omni-pitcher:1", 1,
			map[string]string{"ROUTES_CONFIG": "/config/routes.yaml"}, fixture.ProfileVolume("omni-routes", false)),
		fixture.ConfigMap("omni-routes", map[string]string{"routes.yaml": `streams: [messages, tabletennis]
default_stream: messages
routes:
  - match: {system: tabletennis}
    stream: tabletennis
`}),
		fixture.Deployment("core", "consumer", "img/homerun2-core-catcher:1", 1, nil),
	)
	body := htmlOf(t, get(t, newMux(t, of(t, res, err)), "/"), http.StatusOK)
	contains(t, body,
		// streams: which rule sends omni-pitcher where
		"omni <small>(no rule matches)</small>", "omni <small>(rule 1: system contains &#34;tabletennis&#34;)</small>",
		// components: the routing file and its rules
		"routes ok", "omni-routes / routes.yaml",
		"<code>tabletennis</code> <small>if system contains &#34;tabletennis&#34;</small>", "<code>messages</code> <small>otherwise</small>",
	)
}

func TestOverview_HTTPPitchers(t *testing.T) {
	res, err := fixture.Discover(
		fixture.Deployment("omni", "api", "img/homerun2-omni-pitcher:1", 1, map[string]string{"REDIS_STREAM": "messages"}),
		fixture.Deployment("core", "consumer", "img/homerun2-core-catcher:1", 1, nil),
		fixture.Deployment("demo", "pitcher", "img/homerun2-demo-pitcher:1", 1,
			map[string]string{"PITCH_TARGET": "omni-pitcher", "OMNI_PITCHER_URL": "http://omni", "OMNI_PITCHER_API_PATH": "pitch"}),
		fixture.Deployment("demo-generic", "pitcher", "img/homerun2-demo-pitcher:1", 1,
			map[string]string{"PITCH_TARGET": "omni-pitcher", "OMNI_PITCHER_URL": "http://omni"}),
	)
	body := htmlOf(t, get(t, newMux(t, of(t, res, err)), "/"), http.StatusOK)
	contains(t, body,
		// streams: demo reaches messages through omni
		"omni, demo → omni",
		// components: where each demo-pitcher posts
		"→ omni <code>/pitch</code> <code>messages</code>",
		"→ omni <code>/generic</code>", "the pitches are answered 404",
		// findings
		"pitch-path-unknown", "where omni-pitcher does not take its messages",
	)
}

func TestOverview_SnapshotError(t *testing.T) {
	mux := newMux(t, stubSnapshots{err: errors.New(`deployments.apps is forbidden: <b>User</b> "x"`)})
	body := htmlOf(t, get(t, mux, "/"), http.StatusServiceUnavailable)
	contains(t, body, "Cannot read the namespace homerun2", "deployments.apps is forbidden", "&lt;b&gt;User&lt;/b&gt;")
	lacks(t, body, "<b>User</b>", "Findings", "Snapshot of")
}

func TestOverview_StaleSnapshot(t *testing.T) {
	res, err := fixture.Mixup()
	snaps := of(t, res, err)
	snaps.snap.RefreshError = errors.New("apiserver unavailable")
	snaps.snap.RefreshFailedAt = takenAt.Add(30 * time.Second)

	body := htmlOf(t, get(t, newMux(t, snaps), "/"), http.StatusOK)
	contains(t, body, "Latest refresh failed 12s ago", "apiserver unavailable", "Findings")
}

func TestMatrix(t *testing.T) {
	mux := mixupMux(t)

	// Without a stream: the first one a catcher reads.
	body := htmlOf(t, get(t, mux, "/matrix"), http.StatusOK)
	contains(t, body,
		`<option value="messages" selected>`, `<option value="homerun">`,
		`<th class="severity-error">critical</th>`,
		"<code>(other)</code>",
		`<span class="catcher">light</span>`, "<code>error</code> WLED Blurz in sunset",
		`<span class="catcher">core</span>`, "receives every message",
	)
	// light has no rule for info, but info is not a must-react severity.
	contains(t, body, "<em>no reaction</em>")
	lacks(t, body, `class="entry gap"`)

	body = htmlOf(t, get(t, mux, "/matrix?stream=homerun"), http.StatusOK)
	contains(t, body, "No catcher reads homerun")
	lacks(t, body, `<table class="matrix">`)

	// A stream typed by hand is shown even if nobody uses it.
	body = htmlOf(t, get(t, mux, "/matrix?stream=alerts"), http.StatusOK)
	contains(t, body, `<option value="alerts" selected>`, "No catcher reads alerts")
}

func TestMatrix_Gap(t *testing.T) {
	// led-catcher without its profile file runs with an empty profile: it
	// reacts to nothing, including error and critical.
	res, err := fixture.Discover(
		fixture.Deployment("led", "led-catcher", "img/homerun2-led-catcher:1", 1,
			map[string]string{"PROFILE_PATH": "/config/profile.yaml"}, fixture.ProfileVolume("gone", true)),
	)
	body := htmlOf(t, get(t, newMux(t, of(t, res, err)), "/matrix?stream=messages"), http.StatusOK)
	contains(t, body, `class="entry gap"`)
	if strings.Count(body, `class="entry gap"`) != 2 {
		t.Errorf("want gaps for error and critical only, got %d", strings.Count(body, `class="entry gap"`))
	}
}

func TestDryRunForm(t *testing.T) {
	mux := mixupMux(t)

	body := htmlOf(t, get(t, mux, "/dryrun"), http.StatusOK)
	contains(t, body, `name="stream" list="streams" value="messages"`, `name="severity" list="severities" value="error"`,
		`<option value="homerun">`, `<option value="critical">`, `hx-post="/dryrun"`, `method="post" action="/dryrun"`)

	body = htmlOf(t, get(t, mux, "/dryrun?stream=homerun"), http.StatusOK)
	contains(t, body, `name="stream" list="streams" value="homerun"`)
}

func TestDryRun_FullPage(t *testing.T) {
	body := htmlOf(t, postDryRun(t, mixupMux(t), url.Values{"stream": {"homerun"}, "severity": {"error"}, "title": {"Build failed"}}, false), http.StatusOK)
	contains(t, body, "<html", "This message reaches nobody", "Pitchers publishing here: demo",
		`value="Build failed"`, "no - does not read this stream")
}

func TestDryRun_HTMX(t *testing.T) {
	body := htmlOf(t, postDryRun(t, mixupMux(t), url.Values{"stream": {"messages"}, "severity": {"critical"}, "system": {"k8s"}}, true), http.StatusOK)
	lacks(t, body, "<html", "reaches nobody")
	contains(t, body, "Published to <code>messages</code>", "Pitchers publishing here: omni",
		"<code>error</code> WLED Blurz in sunset", "receives every message")
}

func TestDryRun_Errors(t *testing.T) {
	mux := mixupMux(t)

	body := htmlOf(t, postDryRun(t, mux, url.Values{"stream": {"  "}}, false), http.StatusBadRequest)
	contains(t, body, "<html", "stream is required")

	// htmx swaps only 2xx, so the partial reports the error with 200.
	body = htmlOf(t, postDryRun(t, mux, url.Values{"stream": {""}}, true), http.StatusOK)
	contains(t, body, "stream is required")
	lacks(t, body, "<html")

	body = htmlOf(t, postDryRun(t, mux, url.Values{"stream": {"messages"}, "message": {strings.Repeat("x", maxFormBytes)}}, false),
		http.StatusRequestEntityTooLarge)
	contains(t, body, "invalid form")

	down := newMux(t, stubSnapshots{err: errors.New("forbidden")})
	body = htmlOf(t, postDryRun(t, down, url.Values{"stream": {"messages"}}, true), http.StatusOK)
	contains(t, body, "cannot read the namespace: forbidden")
	body = htmlOf(t, postDryRun(t, down, url.Values{"stream": {"messages"}}, false), http.StatusServiceUnavailable)
	contains(t, body, "Cannot read the namespace homerun2")
}

func TestDryRun_EscapesInput(t *testing.T) {
	body := htmlOf(t, postDryRun(t, mixupMux(t), url.Values{
		"stream": {`messages"><script>alert(1)</script>`},
		"title":  {`<img src=x onerror=alert(1)>`},
	}, false), http.StatusOK)
	lacks(t, body, "<script>alert(1)</script>", "<img src=x")
	contains(t, body, "&lt;script&gt;alert(1)&lt;/script&gt;", "&lt;img src=x onerror=alert(1)&gt;")
}

func TestStatic(t *testing.T) {
	mux := mixupMux(t)
	for path, want := range map[string]string{
		"/static/htmx.min.js": `version:"2.0.4"`,
		"/static/viewer.css":  ".header-bar",
	} {
		rec := get(t, mux, path)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if rec := get(t, mux, "/static/favicon.png"); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("favicon: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestRoutes(t *testing.T) {
	mux := mixupMux(t)
	if rec := get(t, mux, "/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("/nope: %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/", http.NoBody))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /: %d", rec.Code)
	}
}

func TestHumanDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second:              "0s",
		42 * time.Second:          "42s",
		3 * time.Minute:           "3m",
		2*time.Hour + time.Minute: "2h",
	} {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
