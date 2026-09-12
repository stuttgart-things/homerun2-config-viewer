package discovery

import (
	"slices"
	"strings"
	"testing"

	"github.com/stuttgart-things/homerun-library/v4/routing"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// test1RoutesYAML is the routing file omni-pitcher runs with on homerun2-test1.
const test1RoutesYAML = `streams:
  - messages
  - tabletennis
default_stream: messages
routes:
  - match:
      system: tabletennis
    stream: tabletennis
`

// omniPitcher is omni-pitcher as its KCL renders it with routing enabled:
// ROUTES_CONFIG points into the <name>-routes ConfigMap mounted at
// /config/routing.
func omniPitcher(name string, opts ...depOpt) *appsv1.Deployment {
	base := []depOpt{
		env("ROUTES_CONFIG", "/config/routing/routes.yaml"),
		env("REDIS_STREAM", "messages"),
		mount("routes", "/config/routing", ""),
		cmVolume("routes", name+"-routes"),
	}
	return deployment(name, "api", "img/homerun2-omni-pitcher:1", append(base, opts...)...)
}

func TestRoutes_Resolved(t *testing.T) {
	res := discover(t,
		omniPitcher("omni"),
		configMap("omni-routes", map[string]string{"routes.yaml": test1RoutesYAML}),
		deployment("core", "consumer", "img/homerun2-core-catcher:1", env("REDIS_STREAM", "messages")),
	)

	checkResolvedOmni(t, component(t, res, "omni"))

	var when []string
	for _, u := range res.StreamUses() {
		for _, p := range u.Pitchers {
			when = append(when, u.Stream+": "+strings.Join(p.When, "; "))
		}
	}
	if want := []string{"messages: no rule matches", `tabletennis: rule 1: system contains "tabletennis"`}; !slices.Equal(when, want) {
		t.Errorf("when = %q, want %q", when, want)
	}

	got := findingSet(res.Findings([]string{"error", "critical"}))
	if want := []string{"unread-stream:omni"}; !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
}

func checkResolvedOmni(t *testing.T, omni *Component) {
	t.Helper()
	if !slices.Equal(omni.Streams, []string{"messages", "tabletennis"}) || !omni.Routed() || len(omni.Notes) != 0 {
		t.Fatalf("omni: streams %v, routed %v, notes %v", omni.Streams, omni.Routed(), omni.Notes)
	}
	ref := omni.Routes
	if ref == nil || ref.Status != ProfileOK || ref.Path != "/config/routing/routes.yaml" || ref.ConfigMap != "omni-routes" || ref.Key != "routes.yaml" || ref.Routes == nil {
		t.Fatalf("routes ref = %+v", ref)
	}
	if len(omni.StreamValues) != 1 || omni.StreamValues[0].Name != "ROUTES_CONFIG" {
		t.Errorf("streams come from ROUTES_CONFIG, not REDIS_STREAM: %+v", omni.StreamValues)
	}
	if omni.Mode == nil || omni.Mode.Value != "redis" || omni.Mode.Source != SourceDefault {
		t.Errorf("mode = %+v", omni.Mode)
	}
}

type routesProblem struct {
	name string
	opts []depOpt
	cm   map[string]string
	// status of the routes file; "" when omni-pitcher does not read one.
	status ProfileStatus
	// startProblem and note are substrings; "" expects none.
	startProblem, note string
	unresolved         bool
}

func TestRoutes_Problems(t *testing.T) {
	yes := true
	cases := []routesProblem{
		{
			name:         "invalid routes",
			cm:           map[string]string{"routes.yaml": "streams: [messages]\n"},
			status:       ProfileInvalid,
			startProblem: "routing file /config/routing/routes.yaml: invalid routes: default_stream is required: omni-pitcher exits at startup",
		},
		{
			name:         "unparsable routes",
			cm:           map[string]string{"routes.yaml": "streams: [oops\n"},
			status:       ProfileInvalid,
			startProblem: "parse routes",
		},
		{
			name:         "key missing",
			cm:           map[string]string{"other.yaml": test1RoutesYAML},
			status:       ProfileMissing,
			startProblem: "routing file /config/routing/routes.yaml: ConfigMap omni-routes has no key routes.yaml: omni-pitcher exits at startup",
		},
		{
			name:         "ConfigMap missing",
			status:       ProfileMissing,
			startProblem: "ConfigMap omni-routes does not exist: the pod cannot start",
		},
		{
			name: "optional ConfigMap missing",
			opts: []depOpt{func(d *appsv1.Deployment) {
				d.Spec.Template.Spec.Volumes[0].ConfigMap.Optional = &yes
			}},
			status:       ProfileMissing,
			startProblem: "so the optional volume routes is empty: omni-pitcher exits at startup",
		},
		{
			name:       "not on a volume",
			opts:       []depOpt{env("ROUTES_CONFIG", "/etc/omni/routes.yaml")},
			cm:         map[string]string{"routes.yaml": test1RoutesYAML},
			status:     ProfileUnresolved,
			note:       "streams unknown: the routing file cannot be read: /etc/omni/routes.yaml is not on a mounted volume",
			unresolved: true,
		},
		{
			name: "path from a Secret",
			opts: []depOpt{envVar(corev1.EnvVar{Name: "ROUTES_CONFIG", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "s"}, Key: "routes",
			}}})},
			cm:         map[string]string{"routes.yaml": test1RoutesYAML},
			status:     ProfileUnresolved,
			note:       "ROUTES_CONFIG comes from a Secret",
			unresolved: true,
		},
		{
			name: "file mode ignores the routes",
			opts: []depOpt{env("PITCHER_MODE", "file")},
			cm:   map[string]string{"routes.yaml": test1RoutesYAML},
			note: "PITCHER_MODE=file writes to a file",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objs := []runtime.Object{omniPitcher("omni", tc.opts...)}
			if tc.cm != nil {
				objs = append(objs, configMap("omni-routes", tc.cm))
			}
			checkRoutesProblem(t, component(t, discover(t, objs...), "omni"), tc)
		})
	}
}

func checkRoutesProblem(t *testing.T, c *Component, tc routesProblem) {
	t.Helper()
	switch {
	case tc.status == "" && c.Routes != nil:
		t.Errorf("routes = %+v, want none", c.Routes)
	case tc.status != "" && (c.Routes == nil || c.Routes.Status != tc.status):
		t.Errorf("routes = %+v, want status %s", c.Routes, tc.status)
	}
	if tc.startProblem == "" && len(c.StartProblems) != 0 ||
		tc.startProblem != "" && !slices.ContainsFunc(c.StartProblems, func(p string) bool { return strings.Contains(p, tc.startProblem) }) {
		t.Errorf("start problems = %q, want %q", c.StartProblems, tc.startProblem)
	}
	if tc.note != "" && !hasNote(c, tc.note) {
		t.Errorf("notes = %q, want %q", c.Notes, tc.note)
	}
	if c.StreamsUnresolved != tc.unresolved || c.Streams != nil || c.Routed() {
		t.Errorf("streams %v, unresolved %v, routed %v", c.Streams, c.StreamsUnresolved, c.Routed())
	}
}

func TestRoutes_PitcherModeOddValue(t *testing.T) {
	res := discover(t, deployment("omni", "api", "img/homerun2-omni-pitcher:1", env("PITCHER_MODE", "stream"), env("REDIS_STREAM", "s")))
	c := component(t, res, "omni")
	if !slices.Equal(c.Streams, []string{"s"}) || !hasNote(c, `PITCHER_MODE="stream" is not file: omni-pitcher uses redis`) {
		t.Errorf("streams %v, notes %v", c.Streams, c.Notes)
	}
}

func TestRoutesRefWhen(t *testing.T) {
	var none *RoutesRef
	if got := none.When("messages"); got != nil {
		t.Errorf("nil ref: %v", got)
	}
	if got := (&RoutesRef{}).When("messages"); got != nil {
		t.Errorf("ref without routes: %v", got)
	}

	ref := &RoutesRef{Routes: &routing.StreamRoutes{
		Streams:       []string{"messages", "grafana-alerts"},
		DefaultStream: "messages",
		Routes: []routing.StreamRoute{
			{Match: routing.RouteMatch{Author: "grafana"}, Stream: "grafana-alerts"},
			{Match: routing.RouteMatch{Endpoint: routing.PitchPathGitHub}, Stream: "messages"},
			{Match: routing.RouteMatch{TitleContains: []string{"down", "failed"}}, Stream: "grafana-alerts"},
		},
	}}
	cases := map[string][]string{
		"grafana-alerts": {`rule 1: author contains "grafana"`, `rule 3: title contains any of "down", "failed"`},
		"messages":       {`rule 2: endpoint contains "/pitch/github"`, "no rule matches"},
		"unrouted":       nil,
	}
	for stream, want := range cases {
		if got := ref.When(stream); !slices.Equal(got, want) {
			t.Errorf("When(%s) = %q, want %q", stream, got, want)
		}
	}
}
