package discovery

import (
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// routesWithGitHub routes tabletennis and GitHub webhooks to streams of their
// own.
const routesWithGitHub = `streams: [messages, tabletennis, github-events]
default_stream: messages
routes:
  - match: {system: tabletennis}
    stream: tabletennis
  - match: {endpoint: /pitch/github}
    stream: github-events
`

// pitchNamespace is omni-pitcher "omni" with routesWithGitHub, a catcher on
// messages, and the given pitchers.
func pitchNamespace(pitchers ...*appsv1.Deployment) []runtime.Object {
	objs := []runtime.Object{
		omniPitcher(),
		configMap("omni-routes", map[string]string{"routes.yaml": routesWithGitHub}),
		deployment("core", "consumer", "img/homerun2-core-catcher:1", env("REDIS_STREAM", "messages")),
	}
	for _, p := range pitchers {
		objs = append(objs, p)
	}
	return objs
}

func demoPitcher(opts ...depOpt) *appsv1.Deployment {
	return deployment("demo", "pitcher", "img/homerun2-demo-pitcher:1", opts...)
}

// componentFindings returns the finding kinds reported for component name.
func componentFindings(res *Result, name string) []string {
	var out []string
	for _, f := range res.Findings([]string{"error", "critical"}) {
		if f.Component == name {
			out = append(out, string(f.Kind))
		}
	}
	return out
}

type pitchCase struct {
	name string
	opts []depOpt
	// omni, path and streams are the expected PitchTarget; omni "" expects it
	// to reach no omni-pitcher, and no PitchTarget at all when noTarget.
	omni, path string
	streams    []string
	noTarget   bool
	// direct are the streams demo-pitcher writes to itself.
	direct []string
	// problem is a substring of PitchTarget.Problem; "" expects none.
	problem  string
	findings []string
}

func TestPitchTargets_DemoPitcher(t *testing.T) {
	omniTarget := env("PITCH_TARGET", "omni-pitcher")
	pitch := env("OMNI_PITCHER_API_PATH", "pitch")
	routed := []string{"messages", "tabletennis"}

	cases := []pitchCase{
		{name: "service DNS name", opts: []depOpt{omniTarget, env("OMNI_PITCHER_URL", "http://omni.homerun2.svc.cluster.local"), pitch},
			omni: "omni", path: "/pitch", streams: routed},
		{name: "short name", opts: []depOpt{omniTarget, env("OMNI_PITCHER_URL", "http://omni"), pitch},
			omni: "omni", path: "/pitch", streams: routed},
		{name: "namespace and port", opts: []depOpt{omniTarget, env("OMNI_PITCHER_URL", "http://omni.homerun2:8080"), pitch},
			omni: "omni", path: "/pitch", streams: routed},
		{name: "both", opts: []depOpt{env("PITCH_TARGET", "both"), env("REDIS_STREAM", "demo"), env("OMNI_PITCHER_URL", "http://omni"), pitch},
			omni: "omni", path: "/pitch", streams: routed, direct: []string{"demo"}, findings: []string{"unread-stream"}},
		{name: "defaults", opts: []depOpt{omniTarget},
			problem: "localhost is the pitcher's own pod", findings: []string{"pitch-target-unresolved"}},
		{name: "loopback address", opts: []depOpt{omniTarget, env("OMNI_PITCHER_URL", "http://127.0.0.1:4000"), pitch},
			problem: "127.0.0.1 is the pitcher's own pod", findings: []string{"pitch-target-unresolved"}},
		{name: "default API path", opts: []depOpt{omniTarget, env("OMNI_PITCHER_URL", "http://omni")},
			omni: "omni", path: "/generic", problem: "omni-pitcher serves /pitch, /pitch/grafana, /pitch/github, not /generic: the pitches are answered 404",
			findings: []string{"pitch-path-unknown"}},
		{name: "webhook path", opts: []depOpt{omniTarget, env("OMNI_PITCHER_URL", "http://omni"), env("OMNI_PITCHER_API_PATH", "pitch/github")},
			omni: "omni", path: "/pitch/github", problem: "/pitch/github is omni-pitcher's webhook endpoint", findings: []string{"pitch-path-unknown"}},
		{name: "other namespace", opts: []depOpt{omniTarget, env("OMNI_PITCHER_URL", "http://omni.elsewhere.svc"), pitch},
			problem: "omni.elsewhere.svc is not an omni-pitcher Service in namespace homerun2", findings: []string{"pitch-target-unresolved"}},
		{name: "not a Service name", opts: []depOpt{omniTarget, env("OMNI_PITCHER_URL", "http://omni.homerun2.example.com"), pitch},
			problem: "omni.homerun2.example.com is not an omni-pitcher Service", findings: []string{"pitch-target-unresolved"}},
		{name: "not an omni-pitcher", opts: []depOpt{omniTarget, env("OMNI_PITCHER_URL", "http://core"), pitch},
			problem: "core is not an omni-pitcher Service", findings: []string{"pitch-target-unresolved"}},
		{name: "no scheme", opts: []depOpt{omniTarget, env("OMNI_PITCHER_URL", "omni:4000"), pitch},
			problem: `"omni:4000/pitch" is not an http(s) URL`, findings: []string{"pitch-target-unresolved"}},
		{name: "URL from a Secret", opts: []depOpt{omniTarget, pitch, envVar(corev1.EnvVar{Name: "OMNI_PITCHER_URL", ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "s"}, Key: "url"},
		}})}, problem: "the URL cannot be resolved: OMNI_PITCHER_URL comes from a Secret", findings: []string{"pitch-target-unresolved"}},
		{name: "unknown PITCH_TARGET", opts: []depOpt{env("PITCH_TARGET", "http"), env("REDIS_STREAM", "messages")},
			noTarget: true, direct: []string{"messages"}, findings: []string{"pitch-target-invalid"}},
		{name: "file", opts: []depOpt{env("PITCH_TARGET", "file")}, noTarget: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := discover(t, pitchNamespace(demoPitcher(tc.opts...))...)
			checkPitchCase(t, res, component(t, res, "demo"), tc)
		})
	}
}

func checkPitchCase(t *testing.T, res *Result, c *Component, tc pitchCase) {
	t.Helper()
	if !slices.Equal(c.Streams, tc.direct) {
		t.Errorf("direct streams = %v, want %v", c.Streams, tc.direct)
	}
	if got := componentFindings(res, c.Name); !slices.Equal(got, tc.findings) {
		t.Errorf("findings = %v, want %v", got, tc.findings)
	}
	tgt := c.PitchTarget
	if tc.noTarget {
		if tgt != nil {
			t.Errorf("pitch target = %+v, want none", tgt)
		}
		return
	}
	if tgt == nil {
		t.Fatal("no pitch target")
	}
	if tgt.OmniPitcher != tc.omni || tc.path != "" && tgt.Path != tc.path || !slices.Equal(tgt.Streams, tc.streams) {
		t.Errorf("target omni=%q path=%q streams=%v, want %q %q %v", tgt.OmniPitcher, tgt.Path, tgt.Streams, tc.omni, tc.path, tc.streams)
	}
	if tc.problem == "" && tgt.Problem != "" || !strings.Contains(tgt.Problem, tc.problem) {
		t.Errorf("problem = %q, want %q", tgt.Problem, tc.problem)
	}
}

func TestPitchTargets_StreamUses(t *testing.T) {
	res := discover(t, pitchNamespace(demoPitcher(
		env("PITCH_TARGET", "omni-pitcher"), env("OMNI_PITCHER_URL", "http://omni"), env("OMNI_PITCHER_API_PATH", "pitch"),
	))...)

	var got []string
	for _, u := range res.StreamUses() {
		for _, p := range u.Pitchers {
			got = append(got, u.Stream+": "+p.Name+" via "+p.Via+" when "+strings.Join(p.When, "; "))
		}
	}
	want := []string{
		`github-events: omni via  when rule 2: endpoint contains "/pitch/github"`,
		"messages: omni via  when no rule matches",
		"messages: demo via omni when no rule matches",
		`tabletennis: omni via  when rule 1: system contains "tabletennis"`,
		`tabletennis: demo via omni when rule 1: system contains "tabletennis"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("stream uses =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}

	// The unread streams are omni-pitcher's: demo-pitcher reaches them only
	// through it, so it adds no finding of its own.
	if got := findingSet(res.Findings(nil)); !slices.Equal(got, []string{"unread-stream:omni", "unread-stream:omni"}) {
		t.Errorf("findings = %v", got)
	}
}

func TestPitchTargets_WhenOmniPitcherDoesNotRun(t *testing.T) {
	stopped := omniPitcher(scaledToZero)
	res := discover(t,
		stopped,
		configMap("omni-routes", map[string]string{"routes.yaml": routesWithGitHub}),
		demoPitcher(env("PITCH_TARGET", "omni-pitcher"), env("OMNI_PITCHER_URL", "http://omni"), env("OMNI_PITCHER_API_PATH", "pitch")),
	)
	demo := component(t, res, "demo")
	if tgt := demo.PitchTarget; tgt == nil || tgt.OmniPitcher != "omni" || tgt.Streams != nil || tgt.Problem != "omni is scaled to zero, so the pitches fail" {
		t.Errorf("target = %+v", demo.PitchTarget)
	}
	// omni-pitcher's own scaled-to-zero finding says it; demo adds none.
	if got := findingSet(res.Findings(nil)); !slices.Equal(got, []string{"scaled-to-zero:omni"}) {
		t.Errorf("findings = %v", got)
	}
}

func TestPitchTargets_NoFindingForAPitcherThatDoesNotRun(t *testing.T) {
	res := discover(t, pitchNamespace(demoPitcher(env("PITCH_TARGET", "omni-pitcher"), scaledToZero))...)
	if got := componentFindings(res, "demo"); !slices.Equal(got, []string{"scaled-to-zero"}) {
		t.Errorf("findings = %v", got)
	}
}
