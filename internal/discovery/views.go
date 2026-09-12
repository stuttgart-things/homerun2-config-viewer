package discovery

import (
	"fmt"
	"slices"

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

	// A profile that keeps the pod from starting is already reported above.
	if p := c.Profile; p != nil && !slices.Contains(c.StartProblems, p.Message) {
		switch p.Status {
		case ProfileMissing:
			add(FindingProfileMissing, "%s", p.Message)
		case ProfileUnresolved:
			add(FindingProfileUnresolved, "%s", p.Message)
		}
	}
	return out
}

// StreamUse is who publishes to and who reads one stream.
type StreamUse struct {
	Stream   string         `json:"stream"`
	Pitchers []string       `json:"pitchers"`
	Catchers []StreamReader `json:"catchers"`
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
		use := StreamUse{Stream: stream, Pitchers: []string{}, Catchers: []StreamReader{}}
		for i := range comps {
			if !slices.Contains(comps[i].Streams, stream) {
				continue
			}
			switch comps[i].Role {
			case routing.RolePitcher:
				use.Pitchers = append(use.Pitchers, comps[i].Name)
			case routing.RoleCatcher:
				use.Catchers = append(use.Catchers, StreamReader{Name: comps[i].Name, ConsumerGroup: comps[i].ConsumerGroup})
			}
		}
		out = append(out, use)
	}
	return out
}

// DryRunResult is what a message published to a stream would do.
type DryRunResult struct {
	Stream  string          `json:"stream"`
	Message homerun.Message `json:"message"`
	// Pitchers are the routed pitchers publishing to the stream.
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
