package discovery

import (
	"errors"
	"fmt"
	"slices"
	"time"

	homerun "github.com/stuttgart-things/homerun-library/v4"
	"github.com/stuttgart-things/homerun-library/v4/routing"
)

// ErrNotAPitcher is returned by DryRunFrom for a name that is no pitcher of
// the namespace.
var ErrNotAPitcher = errors.New("not a pitcher in the namespace")

// PitchDryRun is what a message sent by one pitcher would do: one path per
// way the pitcher publishes it.
type PitchDryRun struct {
	Pitcher string          `json:"pitcher"`
	Message homerun.Message `json:"message"`
	// Paths are the ways the pitcher publishes: to a stream itself, through
	// omni-pitcher, or both. Empty when it publishes nowhere the viewer can
	// follow; Notes say why.
	Paths []PitchPath `json:"paths"`
	Notes []string    `json:"notes,omitempty"`
}

// PitchPath is one way a message leaves a pitcher, and what it does at the
// end of that way.
type PitchPath struct {
	// Via is the omni-pitcher the message is pitched to. Empty for a pitcher
	// writing to the stream itself.
	Via string `json:"via,omitempty"`
	// Endpoint is the path the pitch arrives on at Via.
	Endpoint string `json:"endpoint,omitempty"`
	// Problem says why the message goes nowhere the viewer can follow.
	Problem string `json:"problem,omitempty"`
	// Rejected says why Via answers the pitch with an error instead of
	// publishing it.
	Rejected string `json:"rejected,omitempty"`
	// Defaulted are the fields Via filled in before publishing.
	Defaulted []DefaultedField `json:"defaulted,omitempty"`
	// Rule says which of Via's routing rules chose the stream, or "no rule
	// matches". Empty without routes.
	Rule string `json:"rule,omitempty"`
	// Result is the dry run on the stream the message is published to. Its
	// Message is the message as published. Nil when it is not published.
	Result *DryRunResult `json:"result,omitempty"`
}

// DefaultedField is a message field omni-pitcher filled in.
type DefaultedField struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// DryRunFrom evaluates msg sent by the pitcher named pitcher: to each stream
// it writes to itself as entered, and through the omni-pitcher it pitches to
// the way omni-pitcher publishes it - rejected, defaulted and routed. now is
// the time omni-pitcher stamps on a message without timestamp.
func (r *Result) DryRunFrom(pitcher string, msg homerun.Message, now time.Time) (PitchDryRun, error) {
	c := r.findComponent(pitcher)
	if c == nil || c.Role != routing.RolePitcher {
		return PitchDryRun{}, fmt.Errorf("%q: %w %s", pitcher, ErrNotAPitcher, r.Namespace)
	}
	res := PitchDryRun{Pitcher: c.Name, Message: msg, Paths: []PitchPath{}}

	switch {
	case !c.Running():
		res.Notes = append(res.Notes, c.Name+" is scaled to zero: it sends nothing")
		return res, nil
	case len(c.StartProblems) > 0:
		res.Notes = append(res.Notes, c.Name+" cannot start: it sends nothing")
		return res, nil
	}

	if c.Kind == KindOmniPitcher {
		// Pitched to omni-pitcher itself, on its message endpoint.
		if len(c.Streams) > 0 || c.StreamsUnresolved {
			res.Paths = append(res.Paths, r.pitchThrough(c, routing.PitchPath, msg, now))
		}
	} else {
		for _, stream := range c.Streams {
			d := r.DryRun(stream, msg)
			res.Paths = append(res.Paths, PitchPath{Result: &d})
		}
		if t := c.PitchTarget; t != nil {
			res.Paths = append(res.Paths, r.pitchTo(t, msg, now))
		}
	}

	if len(res.Paths) == 0 {
		res.Notes = append(res.Notes, c.Name+" publishes to no stream the viewer can follow")
		res.Notes = append(res.Notes, c.Notes...)
	}
	return res, nil
}

// pitchTo follows a pitch to t.
func (r *Result) pitchTo(t *PitchTarget, msg homerun.Message, now time.Time) PitchPath {
	if t.OmniPitcher == "" || t.Problem != "" {
		return PitchPath{Via: t.OmniPitcher, Endpoint: t.Path, Problem: t.Problem}
	}
	return r.pitchThrough(r.findComponent(t.OmniPitcher), t.Path, msg, now)
}

// pitchThrough is what omni does with msg pitched to endpoint. omni runs:
// DryRunFrom and resolvePitchTarget stop at one that does not.
func (r *Result) pitchThrough(omni *Component, endpoint string, msg homerun.Message, now time.Time) PitchPath {
	p := PitchPath{Via: omni.Name, Endpoint: endpoint}
	pitch, err := routing.PreparePitch(msg, now)
	if err != nil {
		p.Rejected = fmt.Sprintf("%s answers 400: %s", omni.Name, err)
		return p
	}
	for _, f := range pitch.Defaulted {
		p.Defaulted = append(p.Defaulted, DefaultedField{Field: f, Value: messageField(pitch.Message, f)})
	}

	var stream string
	switch ref := omni.Routes; {
	case ref != nil && ref.Routes != nil:
		var rule int
		stream, rule = ref.Routes.Resolve(endpoint, pitch.Message)
		p.Rule = noRuleMatches
		if rule >= 0 {
			p.Rule = fmt.Sprintf("rule %d: %s", rule+1, ref.Routes.Routes[rule].Match.Summary())
		}
	case len(omni.Streams) > 0:
		stream = omni.Streams[0]
	default:
		p.Problem = omni.Name + "'s streams are unknown"
		return p
	}

	d := r.DryRun(stream, pitch.Message)
	p.Result = &d
	return p
}

// messageField returns the Message field PreparePitch names field.
func messageField(msg homerun.Message, field string) string {
	switch field {
	case "severity":
		return msg.Severity
	case "author":
		return msg.Author
	case "timestamp":
		return msg.Timestamp
	case "system":
		return msg.System
	default:
		return ""
	}
}

// findComponent returns the component named name, or nil.
func (r *Result) findComponent(name string) *Component {
	i := slices.IndexFunc(r.Components, func(c Component) bool { return c.Name == name })
	if i < 0 {
		return nil
	}
	return &r.Components[i]
}
