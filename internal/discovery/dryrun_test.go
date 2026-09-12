package discovery

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	homerun "github.com/stuttgart-things/homerun-library/v4"
	"github.com/stuttgart-things/homerun-library/v4/routing"
)

var dryRunNow = time.Date(2026, 9, 12, 7, 7, 14, 0, time.UTC)

// pathSummary renders a PitchPath in one line for comparison.
func pathSummary(p PitchPath) string {
	var b strings.Builder
	if p.Via != "" {
		b.WriteString("via " + p.Via + " " + p.Endpoint + ": ")
	}
	switch {
	case p.Problem != "":
		b.WriteString("problem " + p.Problem)
	case p.Rejected != "":
		b.WriteString("rejected " + p.Rejected)
	case p.Result != nil:
		var defaulted []string
		for _, f := range p.Defaulted {
			defaulted = append(defaulted, f.Field+"="+f.Value)
		}
		var receivers []string
		for _, d := range p.Result.Deliveries {
			if d.Receives {
				receivers = append(receivers, d.Component)
			}
		}
		b.WriteString(p.Result.Stream)
		if p.Rule != "" {
			b.WriteString(" (" + p.Rule + ")")
		}
		if len(defaulted) > 0 {
			b.WriteString(" defaulted " + strings.Join(defaulted, ","))
		}
		b.WriteString(" -> " + strings.Join(receivers, ","))
	}
	return b.String()
}

func TestDryRunFrom(t *testing.T) {
	full := homerun.Message{Title: "t", Message: "m", Severity: "info", Author: "e2e", Timestamp: "2020-01-01T00:00:00Z"}
	tabletennis := full
	tabletennis.System = "tabletennis"
	noSystem := homerun.Message{Title: "t", Message: "m", Severity: "critical", Author: "e2e"}
	noTitle := homerun.Message{Message: "m", Severity: "error", System: "github"}

	viaOmni := []depOpt{env("PITCH_TARGET", "omni-pitcher"), env("OMNI_PITCHER_URL", "http://omni"), env("OMNI_PITCHER_API_PATH", "pitch")}
	cases := []struct {
		name string
		// demo configures demo-pitcher; nil leaves it out.
		demo  []depOpt
		from  string
		msg   homerun.Message
		paths []string
		notes []string
	}{
		{name: "omni-pitcher routes tabletennis away", from: "omni", msg: tabletennis,
			paths: []string{"via omni /pitch: tabletennis (rule 1: system contains \"tabletennis\") -> "}},
		{name: "omni-pitcher fills in the system", from: "omni", msg: noSystem,
			paths: []string{"via omni /pitch: messages (no rule matches) defaulted timestamp=2026-09-12T07:07:14Z,system=homerun2-omni-pitcher -> core"}},
		{name: "omni-pitcher rejects a message without title", from: "omni", msg: noTitle,
			paths: []string{"via omni /pitch: rejected omni answers 400: title is required"}},
		{name: "demo-pitcher through omni-pitcher", demo: viaOmni, from: "demo", msg: noSystem,
			paths: []string{"via omni /pitch: messages (no rule matches) defaulted timestamp=2026-09-12T07:07:14Z,system=homerun2-omni-pitcher -> core"}},
		{name: "demo-pitcher both", demo: []depOpt{env("PITCH_TARGET", "both"), env("REDIS_STREAM", "messages"),
			env("OMNI_PITCHER_URL", "http://omni"), env("OMNI_PITCHER_API_PATH", "pitch")}, from: "demo", msg: tabletennis,
			paths: []string{"messages -> core", "via omni /pitch: tabletennis (rule 1: system contains \"tabletennis\") -> "}},
		{name: "demo-pitcher to localhost", demo: []depOpt{env("PITCH_TARGET", "omni-pitcher")}, from: "demo", msg: full,
			paths: []string{"problem localhost is the pitcher's own pod, where no omni-pitcher listens: the pitches fail"}},
		{name: "demo-pitcher to a path omni-pitcher does not serve",
			demo: []depOpt{env("PITCH_TARGET", "omni-pitcher"), env("OMNI_PITCHER_URL", "http://omni")}, from: "demo", msg: full,
			paths: []string{"via omni /generic: problem omni-pitcher serves /pitch, /pitch/grafana, /pitch/github, not /generic: the pitches are answered 404"}},
		{name: "demo-pitcher writing a file", demo: []depOpt{env("PITCH_TARGET", "file")}, from: "demo", msg: full,
			notes: []string{"demo publishes to no stream the viewer can follow", "PITCH_TARGET=file writes to a file: publishes to no stream"}},
		{name: "demo-pitcher scaled to zero", demo: append([]depOpt{scaledToZero}, viaOmni...), from: "demo", msg: full,
			notes: []string{"demo is scaled to zero: it sends nothing"}},
		{name: "a pitcher writing the stream itself publishes the message as entered",
			demo: []depOpt{env("PITCH_TARGET", "redis"), env("REDIS_STREAM", "messages")}, from: "demo", msg: noTitle,
			paths: []string{"messages -> core"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objs := pitchNamespace()
			if tc.demo != nil {
				objs = append(objs, demoPitcher(tc.demo...))
			}
			res := discover(t, objs...)
			got, err := res.DryRunFrom(tc.from, tc.msg, dryRunNow)
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			for _, p := range got.Paths {
				paths = append(paths, pathSummary(p))
			}
			if !slices.Equal(paths, tc.paths) || !slices.Equal(got.Notes, tc.notes) {
				t.Errorf("paths =\n  %s\nwant\n  %s\nnotes = %q, want %q", strings.Join(paths, "\n  "), strings.Join(tc.paths, "\n  "), got.Notes, tc.notes)
			}
			if got.Pitcher != tc.from || got.Message != tc.msg || got.Paths == nil {
				t.Errorf("echo: pitcher %q message %+v paths %v", got.Pitcher, got.Message, got.Paths)
			}
		})
	}
}

func TestDryRunFrom_MessageAsPublished(t *testing.T) {
	res := discover(t, pitchNamespace()...)
	sent := homerun.Message{Title: "t", Message: "m"}
	got, err := res.DryRunFrom("omni", sent, dryRunNow)
	if err != nil {
		t.Fatal(err)
	}
	// The catchers are evaluated with the message omni-pitcher publishes.
	want, err := routing.PreparePitch(sent, dryRunNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Paths) != 1 || got.Paths[0].Result == nil || got.Paths[0].Result.Message != want.Message || want.Message.System != routing.OmniPitcherSystem {
		t.Fatalf("paths = %+v", got.Paths)
	}
	var fields []string
	for _, f := range got.Paths[0].Defaulted {
		fields = append(fields, f.Field)
	}
	if !slices.Equal(fields, []string{"severity", "author", "timestamp", "system"}) {
		t.Errorf("defaulted = %v", got.Paths[0].Defaulted)
	}
}

func TestDryRunFrom_OmniPitcherVariants(t *testing.T) {
	res := discover(t,
		deployment("plain", "api", "img/homerun2-omni-pitcher:1", env("REDIS_STREAM", "alerts")),
		deployment("stopped", "api", "img/homerun2-omni-pitcher:1", scaledToZero),
		deployment("file", "api", "img/homerun2-omni-pitcher:1", env("PITCHER_MODE", "file")),
		deployment("unreadable", "api", "img/homerun2-omni-pitcher:1", env("ROUTES_CONFIG", "/etc/routes.yaml")),
		demoPitcher(env("PITCH_TARGET", "omni-pitcher"), env("OMNI_PITCHER_URL", "http://stopped"), env("OMNI_PITCHER_API_PATH", "pitch")),
	)
	msg := homerun.Message{Title: "t", Message: "m", Severity: "error", Author: "a", System: "s", Timestamp: "2020-01-01T00:00:00Z"}
	cases := map[string]struct {
		paths, notes []string
	}{
		"plain":      {paths: []string{"via plain /pitch: alerts -> "}},
		"stopped":    {notes: []string{"stopped is scaled to zero: it sends nothing"}},
		"file":       {notes: []string{"file publishes to no stream the viewer can follow", "PITCHER_MODE=file writes to a file: publishes to no stream"}},
		"unreadable": {paths: []string{"via unreadable /pitch: problem unreadable's streams are unknown"}},
		"demo":       {paths: []string{"via stopped /pitch: problem stopped is scaled to zero, so the pitches fail"}},
	}
	for name, want := range cases {
		got, err := res.DryRunFrom(name, msg, dryRunNow)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var paths []string
		for _, p := range got.Paths {
			paths = append(paths, pathSummary(p))
		}
		if !slices.Equal(paths, want.paths) || !slices.Equal(got.Notes, want.notes) {
			t.Errorf("%s: paths %q notes %q, want %q %q", name, paths, got.Notes, want.paths, want.notes)
		}
	}
}

// omni-pitcher routes the message it publishes: a rule on a field it fills
// in matches a message that leaves it empty.
func TestDryRunFrom_RoutesSeeTheDefaults(t *testing.T) {
	res := discover(t,
		omniPitcher(),
		configMap("omni-routes", map[string]string{"routes.yaml": `streams: [messages, own]
default_stream: messages
routes:
  - match: {system: homerun2-omni-pitcher}
    stream: own
`}),
	)
	got, err := res.DryRunFrom("omni", homerun.Message{Title: "t", Message: "m"}, dryRunNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Paths) != 1 || got.Paths[0].Result == nil || got.Paths[0].Result.Stream != "own" || got.Paths[0].Rule != `rule 1: system contains "homerun2-omni-pitcher"` {
		t.Errorf("paths = %+v", got.Paths)
	}
}

func TestDryRunFrom_NotAPitcher(t *testing.T) {
	res := discover(t, pitchNamespace()...)
	for _, name := range []string{"core", "nope"} {
		_, err := res.DryRunFrom(name, homerun.Message{}, dryRunNow)
		if !errors.Is(err, ErrNotAPitcher) || !strings.Contains(err.Error(), `"`+name+`": not a pitcher in the namespace homerun2`) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestDryRun_ListsPitchersThroughOmniPitcher(t *testing.T) {
	res := discover(t, pitchNamespace(demoPitcher(
		env("PITCH_TARGET", "omni-pitcher"), env("OMNI_PITCHER_URL", "http://omni"), env("OMNI_PITCHER_API_PATH", "pitch"),
	))...)
	if got := res.DryRun("messages", homerun.Message{}).Pitchers; !slices.Equal(got, []string{"omni", "demo"}) {
		t.Errorf("pitchers = %v", got)
	}
}
