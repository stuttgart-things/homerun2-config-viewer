// Package handlers holds the HTTP handlers that are not part of the UI or API.
package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// BuildInfo holds version metadata.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// NewHealthHandler returns the handler for GET /healthz. It reports that the
// process serves HTTP; it does not call the Kubernetes API, so a slow API
// server cannot make the pod look dead.
func NewHealthHandler(info BuildInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"status":  "healthy",
			"time":    time.Now().UTC().Format(time.RFC3339),
			"version": info.Version,
			"commit":  info.Commit,
			"date":    info.Date,
		}); err != nil {
			slog.Debug("failed to write health response", "error", err)
		}
	}
}
