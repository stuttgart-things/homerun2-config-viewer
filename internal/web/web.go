// Package web serves the viewer's HTML pages: overview, matrix and dry run.
// Pages are rendered on the server and work without JavaScript; htmx only
// swaps the dry-run result in place. Nothing here publishes a message.
package web

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	homerun "github.com/stuttgart-things/homerun-library/v4"
	"github.com/stuttgart-things/homerun-library/v4/routing"

	"github.com/stuttgart-things/homerun2-config-viewer/internal/discovery"
	"github.com/stuttgart-things/homerun2-config-viewer/internal/handlers"
	"github.com/stuttgart-things/homerun2-config-viewer/internal/snapshot"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// maxFormBytes bounds a dry-run form submission.
const maxFormBytes = 64 << 10

// shortCommitLen is how much of the commit the footer shows.
const shortCommitLen = 7

// Severities the pages treat specially.
const (
	severityError    = "error"
	severityCritical = "critical"
	// defaultDryRunSeverity prefills the dry-run form: the severity whose
	// routing matters most.
	defaultDryRunSeverity = severityError
)

// Finding group levels, which set their color.
const (
	levelError = "error"
	levelWarn  = "warn"
	levelInfo  = "info"
)

// Snapshotter hands out the current snapshot; snapshot.Cache implements it.
type Snapshotter interface {
	Get(ctx context.Context) (snapshot.Snapshot, error)
}

// Options are what the pages show besides the snapshot.
type Options struct {
	// Namespace and LabelSelector are shown even when no snapshot could be
	// read, so the page says what it tried to read.
	Namespace           string
	LabelSelector       string
	MustReactSeverities []string
	Build               handlers.BuildInfo
}

// Server serves the pages.
type Server struct {
	snapshots Snapshotter
	opts      Options
	tmpl      *template.Template
	static    http.Handler
	now       func() time.Time
}

// New parses the embedded templates and returns the server.
func New(snapshots Snapshotter, opts Options) (*Server, error) {
	s := &Server{snapshots: snapshots, opts: opts, now: time.Now}

	tmpl, err := template.New("").Funcs(s.funcs()).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	s.tmpl = tmpl

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("static files: %w", err)
	}
	s.static = http.StripPrefix("/static/", http.FileServerFS(static))
	return s, nil
}

// Register adds the page routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.overview)
	mux.HandleFunc("GET /matrix", s.matrix)
	mux.HandleFunc("GET /dryrun", s.dryRunForm)
	mux.HandleFunc("POST /dryrun", s.dryRun)
	mux.Handle("GET /static/", s.static)
}

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"join":          strings.Join,
		"add":           func(a, b int) int { return a + b },
		"since":         func(t time.Time) string { return humanDuration(s.now().Sub(t)) },
		"severityClass": severityClass,
		"shortCommit": func(c string) string {
			if len(c) > shortCommitLen {
				return c[:shortCommitLen]
			}
			return c
		},
	}
}

// page is what every page shows around its content.
type page struct {
	Title         string
	Page          string
	Namespace     string
	LabelSelector string
	MustReact     []string
	Build         handlers.BuildInfo

	TakenAt         time.Time
	RefreshError    string
	RefreshFailedAt time.Time
	// Err is why no snapshot could be read at all.
	Err string
}

// newPage fetches the snapshot for a page. The result is nil when there is
// none; the page then says why.
func (s *Server) newPage(ctx context.Context, name, title string) (page, *discovery.Result) {
	p := page{
		Title:         title,
		Page:          name,
		Namespace:     s.opts.Namespace,
		LabelSelector: s.opts.LabelSelector,
		MustReact:     s.opts.MustReactSeverities,
		Build:         s.opts.Build,
	}
	snap, err := s.snapshots.Get(ctx)
	if err != nil {
		p.Err = err.Error()
		return p, nil
	}
	p.TakenAt = snap.TakenAt
	if snap.RefreshError != nil {
		p.RefreshError = snap.RefreshError.Error()
		p.RefreshFailedAt = snap.RefreshFailedAt
	}
	return p, snap.Result
}

// Overview

type findingGroup struct {
	Kind     routing.FindingKind
	Level    string
	Explain  string
	Findings []routing.Finding
}

// findingKinds orders finding groups by how much they hurt: first what keeps
// messages from arriving at all, last what is merely noteworthy.
var findingKinds = []struct {
	kind           routing.FindingKind
	level, explain string
}{
	{discovery.FindingPodCannotStart, levelError, "the pod cannot start at all"},
	{routing.FindingUnreadStream, levelError, "a pitcher publishes to a stream no catcher reads - and still reports success"},
	{discovery.FindingPitchPathUnknown, levelError, "a pitcher posts to a path where omni-pitcher does not take its messages"},
	{discovery.FindingPitchTargetUnresolved, levelWarn, "a pitcher posts to a URL that is not an omni-pitcher of this namespace"},
	{discovery.FindingPitchTargetInvalid, levelWarn, "demo-pitcher's PITCH_TARGET is a value it does not know, so it silently uses redis"},
	{routing.FindingSharedConsumerGroup, levelWarn, "catchers share a consumer group, so each message reaches only one of them"},
	{routing.FindingUncoveredSeverity, levelWarn, "a catcher with rules does not react to a severity it should"},
	{routing.FindingBrokenRule, levelWarn, "a rule matches but cannot act as configured"},
	{discovery.FindingProfileMissing, levelWarn, "a catcher's profile file does not exist"},
	{discovery.FindingProfileUnresolved, levelInfo, "where a catcher's profile comes from cannot be read"},
	{discovery.FindingStreamsUnknown, levelInfo, "a component's streams cannot be resolved"},
	{discovery.FindingScaledToZero, levelInfo, "scaled to zero"},
}

func groupFindings(findings []routing.Finding) []findingGroup {
	var groups []findingGroup
	known := map[routing.FindingKind]bool{}
	for _, k := range findingKinds {
		known[k.kind] = true
		g := findingGroup{Kind: k.kind, Level: k.level, Explain: k.explain}
		for _, f := range findings {
			if f.Kind == k.kind {
				g.Findings = append(g.Findings, f)
			}
		}
		if len(g.Findings) > 0 {
			groups = append(groups, g)
		}
	}
	for _, f := range findings {
		if known[f.Kind] {
			continue
		}
		i := slices.IndexFunc(groups, func(g findingGroup) bool { return g.Kind == f.Kind })
		if i < 0 {
			groups = append(groups, findingGroup{Kind: f.Kind, Level: levelInfo})
			i = len(groups) - 1
		}
		groups[i].Findings = append(groups[i].Findings, f)
	}
	return groups
}

type overviewPage struct {
	page
	FindingCount  int
	FindingGroups []findingGroup
	Streams       []discovery.StreamUse
	Components    []*discovery.Component
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	p, res := s.newPage(r.Context(), "overview", "Overview")
	data := overviewPage{page: p}
	if res == nil {
		s.render(w, http.StatusServiceUnavailable, "overview.html", data)
		return
	}

	findings := res.Findings(s.opts.MustReactSeverities)
	data.FindingCount = len(findings)
	data.FindingGroups = groupFindings(findings)
	data.Streams = res.StreamUses()
	for i := range res.Components {
		data.Components = append(data.Components, &res.Components[i])
	}
	s.render(w, http.StatusOK, "overview.html", data)
}

// Matrix

type matrixEntry struct {
	Catcher string
	Rule    string
	Summary string
	Problem string
	// Gap: the catcher does not react, to a severity it should.
	Gap bool
}

type matrixCell struct {
	Entries []matrixEntry
}

type matrixRow struct {
	System string
	Cells  []matrixCell
}

type matrixPage struct {
	page
	Streams    []string
	Stream     string
	Severities []string
	Catchers   []string
	Rows       []matrixRow
}

func (s *Server) matrix(w http.ResponseWriter, r *http.Request) {
	p, res := s.newPage(r.Context(), "matrix", "Matrix")
	data := matrixPage{page: p}
	if res == nil {
		s.render(w, http.StatusServiceUnavailable, "matrix.html", data)
		return
	}

	data.Streams = res.Streams()
	data.Stream = strings.TrimSpace(r.URL.Query().Get("stream"))
	if data.Stream == "" {
		data.Stream = defaultStream(res)
	}
	if data.Stream != "" {
		if !slices.Contains(data.Streams, data.Stream) {
			data.Streams = append(data.Streams, data.Stream)
		}
		m := routing.BuildMatrix(res.RoutingComponents(), data.Stream, nil)
		data.Severities = m.Severities
		data.Catchers = m.Catchers
		data.Rows = matrixRows(&m, s.opts.MustReactSeverities)
	}
	s.render(w, http.StatusOK, "matrix.html", data)
}

func matrixRows(m *routing.Matrix, mustReact []string) []matrixRow {
	rows := make([]matrixRow, 0, len(m.Systems))
	for _, system := range m.Systems {
		row := matrixRow{System: system}
		for _, sev := range m.Severities {
			var cell matrixCell
			c, _ := m.Cell(system, sev)
			for _, d := range c.Deliveries {
				if len(d.Reactions) == 0 {
					cell.Entries = append(cell.Entries, matrixEntry{Catcher: d.Component, Gap: slices.Contains(mustReact, sev)})
					continue
				}
				for _, re := range d.Reactions {
					cell.Entries = append(cell.Entries, matrixEntry{Catcher: d.Component, Rule: re.Rule, Summary: re.Summary, Problem: re.Problem})
				}
			}
			row.Cells = append(row.Cells, cell)
		}
		rows = append(rows, row)
	}
	return rows
}

// defaultStream is the first stream a catcher reads, or the first stream.
func defaultStream(res *discovery.Result) string {
	uses := res.StreamUses()
	for _, u := range uses {
		if len(u.Catchers) > 0 {
			return u.Stream
		}
	}
	if len(uses) > 0 {
		return uses[0].Stream
	}
	return ""
}

// Dry run

// Dry-run targets: a message published to a stream, or sent by a pitcher.
const (
	targetStream  = "stream"
	targetPitcher = "pitcher"
)

type dryRunForm struct {
	// Target is targetPitcher for a dry run from a pitcher; anything else is
	// one to a stream.
	Target   string
	Stream   string
	Pitcher  string
	Title    string
	Message  string
	Severity string
	System   string
	Tags     string
	Author   string
}

type dryRunPage struct {
	page
	Form       dryRunForm
	Streams    []string
	Pitchers   []string
	Severities []string
	Systems    []string
	FormError  string
	// Result answers a dry run to a stream, PitchResult one from a pitcher.
	Result      *discovery.DryRunResult
	PitchResult *discovery.PitchDryRun
}

func (s *Server) dryRunForm(w http.ResponseWriter, r *http.Request) {
	p, res := s.newPage(r.Context(), "dryrun", "Dry run")
	q := r.URL.Query()
	data := dryRunPage{
		page: p,
		Form: dryRunForm{
			Target: targetStream, Stream: strings.TrimSpace(q.Get("stream")), Pitcher: strings.TrimSpace(q.Get("pitcher")),
			Severity: defaultDryRunSeverity,
		},
		Severities: routing.Severities,
	}
	if data.Form.Pitcher != "" {
		data.Form.Target = targetPitcher
	}
	if res == nil {
		s.render(w, http.StatusServiceUnavailable, "dryrun.html", data)
		return
	}
	fillChoices(&data, res)
	if data.Form.Stream == "" && data.Form.Target == targetStream {
		data.Form.Stream = defaultStream(res)
	}
	s.render(w, http.StatusOK, "dryrun.html", data)
}

func (s *Server) dryRun(w http.ResponseWriter, r *http.Request) {
	htmx := r.Header.Get("HX-Request") == "true"
	p, res := s.newPage(r.Context(), "dryrun", "Dry run")
	data := dryRunPage{page: p, Severities: routing.Severities}

	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		data.FormError = "invalid form: " + err.Error()
		s.renderDryRun(w, htmx, status, &data)
		return
	}
	field := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	data.Form = dryRunForm{
		Target: field("target"), Stream: field("stream"), Pitcher: field("pitcher"), Title: field("title"), Message: field("message"),
		Severity: field("severity"), System: field("system"), Tags: field("tags"), Author: field("author"),
	}

	if res == nil {
		s.renderDryRun(w, htmx, http.StatusServiceUnavailable, &data)
		return
	}
	fillChoices(&data, res)
	msg := homerun.Message{
		Title: data.Form.Title, Message: data.Form.Message, Severity: data.Form.Severity,
		System: data.Form.System, Tags: data.Form.Tags, Author: data.Form.Author,
	}

	if data.Form.Target == targetPitcher {
		if data.Form.Pitcher == "" {
			data.FormError = "pitcher is required"
			s.renderDryRun(w, htmx, http.StatusBadRequest, &data)
			return
		}
		result, err := res.DryRunFrom(data.Form.Pitcher, msg, s.now())
		if err != nil {
			data.FormError = err.Error()
			s.renderDryRun(w, htmx, http.StatusBadRequest, &data)
			return
		}
		data.PitchResult = &result
		s.renderDryRun(w, htmx, http.StatusOK, &data)
		return
	}

	if data.Form.Stream == "" {
		data.FormError = "stream is required"
		s.renderDryRun(w, htmx, http.StatusBadRequest, &data)
		return
	}
	result := res.DryRun(data.Form.Stream, msg)
	data.Result = &result
	s.renderDryRun(w, htmx, http.StatusOK, &data)
}

// renderDryRun renders the whole page, or for htmx only the result. htmx
// swaps 2xx responses only, so the partial carries any error in its body with
// status 200.
func (s *Server) renderDryRun(w http.ResponseWriter, htmx bool, status int, data *dryRunPage) {
	if !htmx {
		s.render(w, status, "dryrun.html", data)
		return
	}
	if data.Err != "" && data.FormError == "" {
		data.FormError = "cannot read the namespace: " + data.Err
	}
	s.render(w, http.StatusOK, "dryrun-result", data)
}

func fillChoices(data *dryRunPage, res *discovery.Result) {
	data.Streams = res.Streams()
	for i := range res.Components {
		if res.Components[i].Role == routing.RolePitcher {
			data.Pitchers = append(data.Pitchers, res.Components[i].Name)
		}
	}
	for _, c := range res.RoutingComponents() {
		if c.Profile == nil {
			continue
		}
		for _, sys := range c.Profile.Systems() {
			if !slices.Contains(data.Systems, sys) {
				data.Systems = append(data.Systems, sys)
			}
		}
	}
	slices.Sort(data.Systems)
}

// Rendering

// render executes the template into a buffer first, so a template error is a
// clean 500 instead of half a page.
func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		slog.Error("failed to render page", "template", name, "error", err)
		http.Error(w, "internal error rendering the page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		slog.Debug("failed to write page", "template", name, "error", err)
	}
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d.Seconds()), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// severityClass maps a severity to its CSS class; error and critical share
// one, as they share a rank.
func severityClass(s string) string {
	switch sev := strings.ToLower(s); sev {
	case severityCritical:
		return "severity-" + severityError
	case severityError, "warning", "success", "debug":
		return "severity-" + sev
	default:
		return "severity-info"
	}
}
