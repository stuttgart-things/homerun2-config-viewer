// Package api serves the viewer's JSON API over the current snapshot of the
// namespace. It only reads: nothing it offers publishes a message.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	homerun "github.com/stuttgart-things/homerun-library/v4"
	"github.com/stuttgart-things/homerun-library/v4/routing"

	"github.com/stuttgart-things/homerun2-config-viewer/internal/discovery"
	"github.com/stuttgart-things/homerun2-config-viewer/internal/snapshot"
)

// MaxDryRunBody bounds the size of a dry-run request.
const MaxDryRunBody = 64 << 10

// Snapshotter hands out the current snapshot; snapshot.Cache implements it.
type Snapshotter interface {
	Get(ctx context.Context) (snapshot.Snapshot, error)
}

// Server serves the API.
type Server struct {
	snapshots Snapshotter
	mustReact []string
}

// New returns a server reading snapshots from snapshots. mustReact are the
// severities every catcher with rules is expected to react to.
func New(snapshots Snapshotter, mustReact []string) *Server {
	return &Server{snapshots: snapshots, mustReact: mustReact}
}

// Register adds the API routes to mux. Other methods on these paths get 405
// from the mux, with an Allow header.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/components", s.components)
	mux.HandleFunc("GET /api/findings", s.findings)
	mux.HandleFunc("GET /api/streams", s.streams)
	mux.HandleFunc("GET /api/matrix", s.matrix)
	mux.HandleFunc("POST /api/dryrun", s.dryRun)
}

// Meta describes the snapshot a response was computed from.
type Meta struct {
	Namespace     string    `json:"namespace"`
	LabelSelector string    `json:"labelSelector"`
	TakenAt       time.Time `json:"takenAt"`
	// RefreshError is set when the latest rebuild failed: the response then
	// comes from the snapshot taken at TakenAt.
	RefreshError    string     `json:"refreshError,omitempty"`
	RefreshFailedAt *time.Time `json:"refreshFailedAt,omitempty"`
}

// ComponentsResponse is the body of GET /api/components.
type ComponentsResponse struct {
	Meta       Meta                  `json:"meta"`
	Components []discovery.Component `json:"components"`
}

// FindingsResponse is the body of GET /api/findings.
type FindingsResponse struct {
	Meta                Meta              `json:"meta"`
	MustReactSeverities []string          `json:"mustReactSeverities"`
	Findings            []routing.Finding `json:"findings"`
}

// StreamsResponse is the body of GET /api/streams.
type StreamsResponse struct {
	Meta    Meta                  `json:"meta"`
	Streams []discovery.StreamUse `json:"streams"`
}

// MatrixResponse is the body of GET /api/matrix.
type MatrixResponse struct {
	Meta   Meta           `json:"meta"`
	Matrix routing.Matrix `json:"matrix"`
}

// DryRunRequest is the body of POST /api/dryrun.
type DryRunRequest struct {
	Stream  string          `json:"stream"`
	Message homerun.Message `json:"message"`
}

// DryRunResponse is the answer to POST /api/dryrun: the snapshot meta and
// discovery.DryRunResult, flattened.
type DryRunResponse struct {
	Meta Meta `json:"meta"`
	discovery.DryRunResult
}

type errorResponse struct {
	Error string `json:"error"`
}

func (s *Server) components(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	comps := snap.Result.Components
	if comps == nil {
		comps = []discovery.Component{}
	}
	writeJSON(w, http.StatusOK, ComponentsResponse{Meta: metaOf(&snap), Components: comps})
}

func (s *Server) findings(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	findings := snap.Result.Findings(s.mustReact)
	if findings == nil {
		findings = []routing.Finding{}
	}
	mustReact := s.mustReact
	if mustReact == nil {
		mustReact = []string{}
	}
	writeJSON(w, http.StatusOK, FindingsResponse{Meta: metaOf(&snap), MustReactSeverities: mustReact, Findings: findings})
}

func (s *Server) streams(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, StreamsResponse{Meta: metaOf(&snap), Streams: snap.Result.StreamUses()})
}

func (s *Server) matrix(w http.ResponseWriter, r *http.Request) {
	stream := strings.TrimSpace(r.URL.Query().Get("stream"))
	if stream == "" {
		writeError(w, http.StatusBadRequest, "query parameter stream is required")
		return
	}
	severities, err := parseSeverities(r.URL.Query().Get("severities"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	snap, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	m := routing.BuildMatrix(snap.Result.RoutingComponents(), stream, severities)
	writeJSON(w, http.StatusOK, MatrixResponse{Meta: metaOf(&snap), Matrix: m})
}

func (s *Server) dryRun(w http.ResponseWriter, r *http.Request) {
	req, status, err := decodeDryRun(w, r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}

	snap, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, DryRunResponse{Meta: metaOf(&snap), DryRunResult: snap.Result.DryRun(req.Stream, req.Message)})
}

// decodeDryRun reads exactly one DryRunRequest with known fields only, and a
// non-empty stream.
func decodeDryRun(w http.ResponseWriter, r *http.Request) (DryRunRequest, int, error) {
	var req DryRunRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxDryRunBody))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return req, http.StatusRequestEntityTooLarge, fmt.Errorf("request body is larger than %d bytes", MaxDryRunBody)
		}
		return req, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return req, http.StatusBadRequest, errors.New("request body must be a single JSON object")
	}

	req.Stream = strings.TrimSpace(req.Stream)
	if req.Stream == "" {
		return req, http.StatusBadRequest, errors.New("stream is required")
	}
	return req, http.StatusOK, nil
}

// parseSeverities parses a comma-separated severity list. Empty means all.
func parseSeverities(v string) ([]string, error) {
	if strings.TrimSpace(v) == "" {
		return nil, nil
	}
	var out []string
	for _, sev := range strings.Split(v, ",") {
		sev = strings.ToLower(strings.TrimSpace(sev))
		if sev == "" {
			continue
		}
		if !slices.Contains(routing.Severities, sev) {
			return nil, fmt.Errorf("severity %q is not one of %s", sev, strings.Join(routing.Severities, ", "))
		}
		if !slices.Contains(out, sev) {
			out = append(out, sev)
		}
	}
	return out, nil
}

// snapshot fetches the current snapshot, answering 503 when there is none.
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) (snapshot.Snapshot, bool) {
	snap, err := s.snapshots.Get(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "cannot read the namespace: "+err.Error())
		return snap, false
	}
	return snap, true
}

func metaOf(snap *snapshot.Snapshot) Meta {
	m := Meta{Namespace: snap.Result.Namespace, LabelSelector: snap.Result.LabelSelector, TakenAt: snap.TakenAt}
	if snap.RefreshError != nil {
		m.RefreshError = snap.RefreshError.Error()
		failedAt := snap.RefreshFailedAt
		m.RefreshFailedAt = &failedAt
	}
	return m
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("failed to write API response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}
