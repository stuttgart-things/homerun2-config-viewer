package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandler(t *testing.T) {
	h := NewHealthHandler(BuildInfo{Version: "1.2.3", Commit: "abc", Date: "2026-09-11"})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["status"] != "healthy" || body["version"] != "1.2.3" || body["commit"] != "abc" || body["date"] != "2026-09-11" || body["time"] == "" {
		t.Errorf("body = %v", body)
	}
}

func TestHealthHandler_RejectsOtherMethods(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHealthHandler(BuildInfo{})(rec, httptest.NewRequest(http.MethodPost, "/healthz", http.NoBody))

	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodGet {
		t.Errorf("status = %d, Allow = %q", rec.Code, rec.Header().Get("Allow"))
	}
}
