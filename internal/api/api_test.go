package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun-library/v4/routing"

	"github.com/stuttgart-things/homerun2-config-viewer/internal/discovery"
	"github.com/stuttgart-things/homerun2-config-viewer/internal/fixture"
	"github.com/stuttgart-things/homerun2-config-viewer/internal/snapshot"
)

var takenAt = time.Date(2026, 9, 11, 14, 0, 0, 0, time.UTC)

// mixup is fixture.Mixup: demo-pitcher on "homerun", catchers on "messages",
// and a catcher scaled to zero.
func mixup(t *testing.T) *discovery.Result {
	t.Helper()
	res, err := fixture.Mixup()
	if err != nil {
		t.Fatal(err)
	}
	return res
}

type stubSnapshots struct {
	snap snapshot.Snapshot
	err  error
}

func (s stubSnapshots) Get(context.Context) (snapshot.Snapshot, error) { return s.snap, s.err }

func newMux(t *testing.T, snaps Snapshotter) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	New(snaps, []string{"error", "critical"}).Register(mux)
	return mux
}

func fixtureMux(t *testing.T) *http.ServeMux {
	t.Helper()
	return newMux(t, stubSnapshots{snap: snapshot.Snapshot{Result: mixup(t), TakenAt: takenAt}})
}

func do(t *testing.T, mux http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) T {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, wantStatus, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, rec.Body)
	}
	return v
}

func TestComponents(t *testing.T) {
	resp := decode[ComponentsResponse](t, do(t, fixtureMux(t), http.MethodGet, "/api/components", ""), http.StatusOK)

	if resp.Meta.Namespace != fixture.Namespace || !resp.Meta.TakenAt.Equal(takenAt) || resp.Meta.RefreshError != "" || resp.Meta.RefreshFailedAt != nil {
		t.Errorf("meta = %+v", resp.Meta)
	}
	var names []string
	for _, c := range resp.Components {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "core,demo,light,omni,stopped" {
		t.Errorf("components = %v", names)
	}
	for _, c := range resp.Components {
		if c.Name == "light" && (c.Profile == nil || c.Profile.Status != discovery.ProfileOK) {
			t.Errorf("light profile = %+v", c.Profile)
		}
	}
}

func TestFindings(t *testing.T) {
	resp := decode[FindingsResponse](t, do(t, fixtureMux(t), http.MethodGet, "/api/findings", ""), http.StatusOK)

	var got []string
	for _, f := range resp.Findings {
		got = append(got, string(f.Kind)+":"+f.Component+":"+f.Stream)
	}
	sort.Strings(got)
	if want := []string{"scaled-to-zero:stopped:", "unread-stream:demo:homerun"}; !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
	if !slices.Equal(resp.MustReactSeverities, []string{"error", "critical"}) {
		t.Errorf("mustReactSeverities = %v", resp.MustReactSeverities)
	}
}

func TestStreams(t *testing.T) {
	resp := decode[StreamsResponse](t, do(t, fixtureMux(t), http.MethodGet, "/api/streams", ""), http.StatusOK)

	var got []string
	for _, s := range resp.Streams {
		var catchers []string
		for _, c := range s.Catchers {
			catchers = append(catchers, c.Name)
		}
		got = append(got, s.Stream+":"+strings.Join(s.Pitchers, ",")+":"+strings.Join(catchers, ","))
	}
	if want := []string{"homerun:demo:", "messages:omni:core,light"}; !slices.Equal(got, want) {
		t.Errorf("streams = %v, want %v", got, want)
	}
}

func TestMatrix(t *testing.T) {
	mux := fixtureMux(t)

	resp := decode[MatrixResponse](t, do(t, mux, http.MethodGet, "/api/matrix?stream=messages", ""), http.StatusOK)
	if !slices.Equal(resp.Matrix.Catchers, []string{"core", "light"}) || len(resp.Matrix.Severities) != len(routing.Severities) {
		t.Errorf("matrix catchers %v, severities %v", resp.Matrix.Catchers, resp.Matrix.Severities)
	}
	cell, ok := resp.Matrix.Cell(routing.OtherSystem, "critical")
	if !ok {
		t.Fatal("no (other)/critical cell")
	}
	for _, d := range cell.Deliveries {
		if d.Component == "light" && (len(d.Reactions) != 1 || d.Reactions[0].Rule != "error") {
			t.Errorf("light (other)/critical = %+v", d)
		}
	}

	resp = decode[MatrixResponse](t, do(t, mux, http.MethodGet, "/api/matrix?stream=messages&severities=Error,critical,error", ""), http.StatusOK)
	if !slices.Equal(resp.Matrix.Severities, []string{"error", "critical"}) {
		t.Errorf("severities = %v", resp.Matrix.Severities)
	}

	// A stream nobody reads is a valid question with an empty answer.
	resp = decode[MatrixResponse](t, do(t, mux, http.MethodGet, "/api/matrix?stream=homerun", ""), http.StatusOK)
	if len(resp.Matrix.Catchers) != 0 {
		t.Errorf("homerun catchers = %v", resp.Matrix.Catchers)
	}

	for target, msg := range map[string]string{
		"/api/matrix":            "stream is required",
		"/api/matrix?stream=%20": "stream is required",
		"/api/matrix?stream=messages&severities=fatal": `severity "fatal"`,
	} {
		e := decode[errorResponse](t, do(t, mux, http.MethodGet, target, ""), http.StatusBadRequest)
		if !strings.Contains(e.Error, msg) {
			t.Errorf("%s: error %q, want %q", target, e.Error, msg)
		}
	}
}

func TestDryRun(t *testing.T) {
	mux := fixtureMux(t)

	// What demo-pitcher publishes on test1: nobody reads it.
	resp := decode[DryRunResponse](t, do(t, mux, http.MethodPost, "/api/dryrun",
		`{"stream":"homerun","message":{"title":"Build failed","severity":"error","system":"github"}}`), http.StatusOK)
	if !resp.ReachesNobody || !slices.Equal(resp.Pitchers, []string{"demo"}) || len(resp.Deliveries) != 2 {
		t.Errorf("homerun: %+v", resp)
	}
	if resp.Message.Title != "Build failed" || resp.Stream != "homerun" {
		t.Errorf("request echo: %+v", resp)
	}

	resp = decode[DryRunResponse](t, do(t, mux, http.MethodPost, "/api/dryrun",
		`{"stream":" messages ","message":{"severity":"critical","system":"k8s"}}`), http.StatusOK)
	if resp.ReachesNobody || resp.Stream != "messages" || !slices.Equal(resp.Pitchers, []string{"omni"}) {
		t.Errorf("messages: %+v", resp)
	}
	for _, d := range resp.Deliveries {
		if !d.Receives || len(d.Reactions) != 1 {
			t.Errorf("%s: %+v", d.Component, d)
		}
		if d.Component == "light" && d.Reactions[0].Rule != "error" {
			t.Errorf("light: %+v", d.Reactions[0])
		}
	}
}

func TestDryRun_Validation(t *testing.T) {
	mux := fixtureMux(t)
	cases := []struct {
		name, body string
		status     int
		msg        string
	}{
		{"not JSON", `{"stream":`, http.StatusBadRequest, "invalid request body"},
		{"unknown field", `{"stream":"messages","severity":"error"}`, http.StatusBadRequest, `unknown field "severity"`},
		{"unknown message field", `{"stream":"messages","message":{"level":"error"}}`, http.StatusBadRequest, `unknown field "level"`},
		{"no stream", `{"message":{"severity":"error"}}`, http.StatusBadRequest, "stream is required"},
		{"blank stream", `{"stream":"  "}`, http.StatusBadRequest, "stream is required"},
		{"two objects", `{"stream":"messages"}{"stream":"x"}`, http.StatusBadRequest, "single JSON object"},
		{"too large", `{"stream":"messages","message":{"message":"` + strings.Repeat("x", MaxDryRunBody) + `"}}`,
			http.StatusRequestEntityTooLarge, "larger than"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := decode[errorResponse](t, do(t, mux, http.MethodPost, "/api/dryrun", tc.body), tc.status)
			if !strings.Contains(e.Error, tc.msg) {
				t.Errorf("error %q, want %q", e.Error, tc.msg)
			}
		})
	}
}

func TestMethods(t *testing.T) {
	mux := fixtureMux(t)
	for _, tc := range []struct{ method, target, allow string }{
		{http.MethodPost, "/api/components", "GET, HEAD"},
		{http.MethodDelete, "/api/findings", "GET, HEAD"},
		{http.MethodGet, "/api/dryrun", "POST"},
	} {
		rec := do(t, mux, tc.method, tc.target, "")
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != tc.allow {
			t.Errorf("%s %s: %d, Allow %q", tc.method, tc.target, rec.Code, rec.Header().Get("Allow"))
		}
	}
}

func TestSnapshotUnavailable(t *testing.T) {
	mux := newMux(t, stubSnapshots{err: errors.New(`deployments.apps is forbidden: User "x" cannot list`)})
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/components", ""},
		{http.MethodGet, "/api/findings", ""},
		{http.MethodGet, "/api/streams", ""},
		{http.MethodGet, "/api/matrix?stream=messages", ""},
		{http.MethodPost, "/api/dryrun", `{"stream":"messages"}`},
	} {
		e := decode[errorResponse](t, do(t, mux, tc.method, tc.target, tc.body), http.StatusServiceUnavailable)
		if !strings.Contains(e.Error, "cannot read the namespace") || !strings.Contains(e.Error, "forbidden") {
			t.Errorf("%s: error %q", tc.target, e.Error)
		}
	}
}

func TestRefreshErrorInMeta(t *testing.T) {
	failedAt := takenAt.Add(time.Minute)
	mux := newMux(t, stubSnapshots{snap: snapshot.Snapshot{
		Result: mixup(t), TakenAt: takenAt, RefreshError: errors.New("apiserver unavailable"), RefreshFailedAt: failedAt,
	}})

	resp := decode[ComponentsResponse](t, do(t, mux, http.MethodGet, "/api/components", ""), http.StatusOK)
	if resp.Meta.RefreshError != "apiserver unavailable" || resp.Meta.RefreshFailedAt == nil || !resp.Meta.RefreshFailedAt.Equal(failedAt) {
		t.Errorf("meta = %+v", resp.Meta)
	}
	if len(resp.Components) == 0 {
		t.Error("a stale snapshot is still served")
	}
}

func TestEmptyNamespaceEncodesEmptyLists(t *testing.T) {
	res, err := fixture.Discover()
	if err != nil {
		t.Fatal(err)
	}
	mux := newMux(t, stubSnapshots{snap: snapshot.Snapshot{Result: res, TakenAt: takenAt}})

	for target, want := range map[string]string{
		"/api/components": `"components":[]`,
		"/api/findings":   `"findings":[]`,
		"/api/streams":    `"streams":[]`,
	} {
		if body := do(t, mux, http.MethodGet, target, "").Body.String(); !strings.Contains(body, want) {
			t.Errorf("%s: want %s in %s", target, want, body)
		}
	}
	if body := do(t, mux, http.MethodPost, "/api/dryrun", `{"stream":"messages"}`).Body.String(); !strings.Contains(body, `"deliveries":[]`) ||
		!strings.Contains(body, `"pitchers":[]`) || !strings.Contains(body, `"reachesNobody":true`) {
		t.Errorf("dry run in an empty namespace: %s", body)
	}
}
