package web

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/stuttgart-things/homerun-library/v4/routing"
)

// Micro-frontend fragments for homerun2-portal. The portal reads
// <base>/ui/manifest.json to learn which fragments a service offers and loads
// each <base>/ui/fragment/<id> with htmx into its overview. A fragment is bare
// HTML styled only by the portal's hr-* classes (static/homerun2.css in
// homerun2-portal); every URL in it carries BasePath, because it is served on
// the portal's origin.

const (
	findingsFragmentLimit   = 6
	findingsFragmentRefresh = 15
)

// Fragment is one entry of the manifest.
type Fragment struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	RefreshSeconds int    `json:"refreshSeconds"`
}

// Manifest lists the fragments the viewer offers to the portal.
type Manifest struct {
	Name      string     `json:"name"`
	Title     string     `json:"title"`
	Version   string     `json:"version"`
	BasePath  string     `json:"basePath"`
	Fragments []Fragment `json:"fragments"`
}

func (s *Server) manifest(w http.ResponseWriter, _ *http.Request) {
	m := Manifest{
		Name:     "homerun2-config-viewer",
		Title:    "Config Viewer",
		Version:  s.opts.Build.Version,
		BasePath: s.opts.BasePath,
		Fragments: []Fragment{{
			ID:             "findings",
			Title:          "Findings",
			URL:            s.opts.BasePath + "/ui/fragment/findings",
			RefreshSeconds: findingsFragmentRefresh,
		}},
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(m); err != nil {
		slog.Debug("failed to write manifest", "error", err)
	}
}

// fragmentFinding is one finding in the fragment, with the level of its group.
type fragmentFinding struct {
	Kind     routing.FindingKind
	Severity string
	Message  string
}

type findingsFragment struct {
	// Err is why no snapshot could be read at all.
	Err      string
	Count    int
	Findings []fragmentFinding
	More     int
}

// findingsFragment serves GET /ui/fragment/findings: the number of findings
// and the worst of them, in the order of the overview. It reads the cached
// snapshot like the pages, so the portal's refresh does not reach the
// Kubernetes API more often than CACHE_TTL. htmx swaps 2xx responses only, so
// a missing snapshot is reported in the body with status 200.
func (s *Server) findingsFragment(w http.ResponseWriter, r *http.Request) {
	var data findingsFragment
	snap, err := s.snapshots.Get(r.Context())
	switch {
	case err != nil:
		data.Err = err.Error()
	case snap.Result != nil:
		findings := snap.Result.Findings(s.opts.MustReactSeverities)
		data.Count = len(findings)
		for _, g := range groupFindings(findings) {
			for _, f := range g.Findings {
				if len(data.Findings) == findingsFragmentLimit {
					break
				}
				data.Findings = append(data.Findings, fragmentFinding{Kind: f.Kind, Severity: hrSeverity(g.Level), Message: f.Message})
			}
		}
		data.More = data.Count - len(data.Findings)
	}
	s.render(w, http.StatusOK, "fragment-findings", data)
}

// hrSeverity maps a finding group level to the portal's hr-sev-* classes.
func hrSeverity(level string) string {
	switch level {
	case levelError:
		return "error"
	case levelWarn:
		return "warning"
	default:
		return "info"
	}
}
