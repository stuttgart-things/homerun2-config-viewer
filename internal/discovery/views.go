package discovery

import (
	"fmt"
	"slices"
	"strings"

	homerun "github.com/stuttgart-things/homerun-library/v4"
	"github.com/stuttgart-things/homerun-library/v4/routing"
)

// Finding kinds the viewer adds to those of routing.Check: problems with the
// components themselves, not with how messages are routed between them.
const (
	FindingPodCannotStart    routing.FindingKind = "pod-cannot-start"
	FindingScaledToZero      routing.FindingKind = "scaled-to-zero"
	FindingStreamsUnknown    routing.FindingKind = "streams-unknown"
	FindingProfileMissing    routing.FindingKind = "profile-missing"
	FindingProfileUnresolved routing.FindingKind = "profile-unresolved"
	// FindingPitchTargetInvalid: demo-pitcher's PITCH_TARGET is a value it
	// does not know, so it silently uses redis.
	FindingPitchTargetInvalid routing.FindingKind = "pitch-target-invalid"
	// FindingPitchTargetUnresolved: a pitcher posts to a URL that is not an
	// omni-pitcher of the namespace.
	FindingPitchTargetUnresolved routing.FindingKind = "pitch-target-unresolved"
	// FindingPitchPathUnknown: a pitcher posts to a path omni-pitcher does not
	// take messages on.
	FindingPitchPathUnknown routing.FindingKind = "pitch-path-unknown"
)

// Findings returns routing.Check over the routed components, followed by the
// viewer's findings about each component in name order.
func (r *Result) Findings(mustReact []string) []routing.Finding {
	findings := routing.Check(r.RoutingComponents(), mustReact)
	for i := range r.Components {
		findings = append(findings, r.Components[i].findings()...)
	}
	return findings
}

func (c *Component) findings() []routing.Finding {
	var out []routing.Finding
	add := func(kind routing.FindingKind, format string, args ...any) {
		out = append(out, routing.Finding{Kind: kind, Component: c.Name, Message: c.Name + ": " + fmt.Sprintf(format, args...)})
	}

	for _, p := range c.StartProblems {
		add(FindingPodCannotStart, "%s", p)
	}

	if c.Role != "" {
		switch {
		case !c.Running():
			add(FindingScaledToZero, "scaled to zero, so it neither publishes nor reads")
		case c.StreamsUnresolved:
			add(FindingStreamsUnknown, "its streams cannot be resolved, so it is left out of routing")
		}
	}

	c.pitchFindings(add)

	// A catcher's profile that keeps the pod from starting is already
	// reported above. k8s-pitcher's profile problems are start problems or
	// unknown streams, reported above as well.
	if p := c.Profile; p != nil && c.Role == routing.RoleCatcher && !slices.Contains(c.StartProblems, p.Message) {
		switch p.Status {
		case ProfileMissing:
			add(FindingProfileMissing, "%s", p.Message)
		case ProfileUnresolved:
			add(FindingProfileUnresolved, "%s", p.Message)
		}
	}
	return out
}

// pitchFindings reports how a pitcher's pitches go wrong. A pitch problem of a
// pod that does not run is moot.
func (c *Component) pitchFindings(add func(kind routing.FindingKind, format string, args ...any)) {
	if t := c.PitchTarget; t != nil && t.problemKind != "" && c.Running() && len(c.StartProblems) == 0 {
		add(t.problemKind, "%s", t.Problem)
	}
	m := c.Mode
	if c.Kind != KindDemoPitcher || m == nil || m.Unresolved != "" && m.Source != SourceDefault || slices.Contains(demoPitchTargets, m.Value) {
		return
	}
	add(FindingPitchTargetInvalid, "PITCH_TARGET=%q is not one of %s: demo-pitcher silently falls back to redis",
		m.Value, strings.Join(demoPitchTargets, ", "))
}

// StreamUse is who publishes to and who reads one stream.
type StreamUse struct {
	Stream   string         `json:"stream"`
	Pitchers []StreamWriter `json:"pitchers"`
	Catchers []StreamReader `json:"catchers"`
}

// StreamWriter is a pitcher publishing to a stream.
type StreamWriter struct {
	Name string `json:"name"`
	// Via is the omni-pitcher the pitcher posts to, when it reaches the
	// stream over HTTP.
	Via string `json:"via,omitempty"`
	// When says which messages the pitcher publishes to the stream: one entry
	// per omni-pitcher routing rule choosing it, and "no rule matches" for its
	// default stream. Empty means every message.
	When []string `json:"when,omitempty"`
}

// StreamReader is a catcher reading a stream.
type StreamReader struct {
	Name string `json:"name"`
	// ConsumerGroup is empty when it could not be resolved.
	ConsumerGroup string `json:"consumerGroup,omitempty"`
}

// StreamUses returns, for every stream a routed component uses, the routed
// pitchers publishing to it and the routed catchers reading it.
func (r *Result) StreamUses() []StreamUse {
	comps := r.RoutingComponents()
	out := []StreamUse{}
	for _, stream := range r.Streams() {
		use := StreamUse{Stream: stream, Pitchers: []StreamWriter{}, Catchers: []StreamReader{}}
		for i := range comps {
			if !slices.Contains(comps[i].Streams, stream) {
				continue
			}
			switch comps[i].Role {
			case routing.RolePitcher:
				use.Pitchers = append(use.Pitchers, StreamWriter{Name: comps[i].Name, When: r.component(comps[i].Name).Routes.When(stream)})
			case routing.RoleCatcher:
				use.Catchers = append(use.Catchers, StreamReader{Name: comps[i].Name, ConsumerGroup: comps[i].ConsumerGroup})
			}
		}
		use.Pitchers = append(use.Pitchers, r.viaWriters(stream)...)
		out = append(out, use)
	}
	return out
}

// viaWriters returns the running pitchers whose HTTP pitches reach stream
// through an omni-pitcher.
func (r *Result) viaWriters(stream string) []StreamWriter {
	var out []StreamWriter
	for i := range r.Components {
		c := &r.Components[i]
		t := c.PitchTarget
		if t == nil || !c.Running() || len(c.StartProblems) > 0 || !slices.Contains(t.Streams, stream) {
			continue
		}
		out = append(out, StreamWriter{Name: c.Name, Via: t.OmniPitcher, When: r.component(t.OmniPitcher).Routes.WhenOn(stream, t.Path)})
	}
	return out
}

// component returns the component named name, or an empty one. It exists
// for every name RoutingComponents returns.
func (r *Result) component(name string) *Component {
	if c := r.findComponent(name); c != nil {
		return c
	}
	return &Component{}
}

// DryRunResult is what a message published to a stream would do.
type DryRunResult struct {
	Stream  string          `json:"stream"`
	Message homerun.Message `json:"message"`
	// Pitchers are the routed pitchers publishing to the stream, and those
	// pitching to an omni-pitcher that publishes to it.
	Pitchers []string `json:"pitchers"`
	// ReachesNobody: no catcher reads the stream. That is an answer, not an
	// error - it is the failure the viewer exists to show.
	ReachesNobody bool               `json:"reachesNobody"`
	Deliveries    []routing.Delivery `json:"deliveries"`
}

// DryRun evaluates msg published to stream against the routed components.
func (r *Result) DryRun(stream string, msg homerun.Message) DryRunResult {
	comps := r.RoutingComponents()
	res := DryRunResult{
		Stream:        stream,
		Message:       msg,
		Pitchers:      []string{},
		ReachesNobody: true,
		Deliveries:    routing.DryRun(comps, stream, msg),
	}
	for i := range comps {
		if comps[i].Role == routing.RolePitcher && slices.Contains(comps[i].Streams, stream) {
			res.Pitchers = append(res.Pitchers, comps[i].Name)
		}
	}
	for _, w := range r.viaWriters(stream) {
		res.Pitchers = append(res.Pitchers, w.Name)
	}
	for _, d := range res.Deliveries {
		if d.Receives {
			res.ReachesNobody = false
		}
	}
	if res.Deliveries == nil {
		res.Deliveries = []routing.Delivery{}
	}
	return res
}
