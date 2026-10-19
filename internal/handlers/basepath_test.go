package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMount(t *testing.T) {
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("app " + r.Method + " " + r.URL.Path))
	})
	health := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("health"))
	})

	cases := []struct {
		basePath, method, path string
		code                   int
		body, location         string
	}{
		{"", http.MethodGet, "/", 200, "app GET /", ""},
		{"", http.MethodGet, "/matrix", 200, "app GET /matrix", ""},
		{"", http.MethodGet, "/health", 200, "health", ""},
		{"", http.MethodGet, "/healthz", 200, "health", ""},
		{"/config", http.MethodGet, "/config/", 200, "app GET /", ""},
		{"/config", http.MethodGet, "/config/matrix", 200, "app GET /matrix", ""},
		{"/config", http.MethodPost, "/config/dryrun", 200, "app POST /dryrun", ""},
		{"/config", http.MethodGet, "/config/api/components", 200, "app GET /api/components", ""},
		{"/config", http.MethodGet, "/config/static/htmx.min.js", 200, "app GET /static/htmx.min.js", ""},
		{"/config", http.MethodGet, "/health", 200, "health", ""},
		{"/config", http.MethodGet, "/healthz", 200, "health", ""},
		{"/config", http.MethodGet, "/config", 307, "", "/config/"},
		{"/config", http.MethodGet, "/", 307, "", "/config/"},
		{"/config", http.MethodGet, "/matrix", 404, "", ""},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		Mount(tc.basePath, app, health).ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, http.NoBody))
		if rec.Code != tc.code {
			t.Errorf("base %q %s %s: code %d, want %d", tc.basePath, tc.method, tc.path, rec.Code, tc.code)
			continue
		}
		if tc.body != "" && rec.Body.String() != tc.body {
			t.Errorf("base %q %s %s: body %q, want %q", tc.basePath, tc.method, tc.path, rec.Body.String(), tc.body)
		}
		if loc := rec.Header().Get("Location"); tc.location != "" && loc != tc.location {
			t.Errorf("base %q %s %s: Location %q, want %q", tc.basePath, tc.method, tc.path, loc, tc.location)
		}
	}
}
