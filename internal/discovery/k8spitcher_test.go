package discovery

import (
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// test1K8sPitcherProfile is the profile k8s-pitcher runs with on
// homerun2-test1.
const test1K8sPitcherProfile = `apiVersion: homerun2.sthings.io/v1alpha1
kind: K8sPitcherProfile
metadata:
  name: homerun2-test1
spec:
  pitcher:
    addr: http://homerun2-omni-pitcher.homerun2.svc.cluster.local/pitch
    insecure: false
  auth:
    tokenFrom:
      secretKeyRef:
        name: homerun2-k8s-pitcher-token
        namespace: homerun2
        key: auth-token
  collectors:
    - kind: Node
      interval: 5m
  informers:
    - group: ""
      version: v1
      resource: events
      namespace: homerun2
      events: [add]
    - group: apps
      version: v1
      resource: deployments
      namespace: homerun2
      events: [add, delete]
`

func args(a ...string) depOpt {
	return func(d *appsv1.Deployment) {
		d.Spec.Template.Spec.Containers[0].Args = a
	}
}

// k8sPitcher is k8s-pitcher as its KCL renders it: -profile points into the
// <name>-profile ConfigMap mounted at /etc/k8s-pitcher.
func k8sPitcher(name string, opts ...depOpt) *appsv1.Deployment {
	base := []depOpt{
		args("--profile", "/etc/k8s-pitcher/profile.yaml"),
		mount("profile", "/etc/k8s-pitcher", ""),
		cmVolume("profile", name+"-profile"),
	}
	return deployment(name, "watcher", "img/homerun2-k8s-pitcher:1", append(base, opts...)...)
}

const k8sRedisProfile = `spec:
  redis: {addr: redis, stream: k8s-events}
  collectors: [{kind: Node, interval: 1m}]
`

type k8sCase struct {
	name    string
	opts    []depOpt
	profile string // "" leaves the ConfigMap out
	key     string // defaults to profile.yaml
	// target is the omni-pitcher the pitches reach; streams are the direct
	// streams.
	target  string
	streams []string
	// problem and note are substrings; problem "" expects no start problem.
	problem, note string
	findings      []string
}

func TestK8sPitcher(t *testing.T) {
	httpProfile := `spec:
  pitcher: {addr: "http://omni.homerun2.svc/pitch"}
  informers: [{version: v1, resource: events, events: [add]}]
`
	exits := "k8s-pitcher exits at startup"
	cases := []k8sCase{
		{name: "HTTP to omni-pitcher", profile: httpProfile, target: "omni"},
		{name: "redis", profile: k8sRedisProfile, streams: []string{"k8s-events"}, findings: []string{"unread-stream"}},
		{name: "file mode", opts: []depOpt{env("PITCHER_MODE", "file")}, profile: httpProfile, note: "PITCHER_MODE=file writes to a file"},
		{name: "redis and pitcher", profile: "spec:\n  redis: {addr: r, stream: s}\n  pitcher: {addr: http://omni/pitch}\n  collectors: [{kind: Node, interval: 1m}]\n",
			problem: "spec.redis and spec.pitcher are mutually exclusive: " + exits, findings: []string{"pod-cannot-start"}},
		{name: "neither", profile: "spec:\n  collectors: [{kind: Node, interval: 1m}]\n",
			problem: "either spec.redis.addr or spec.pitcher.addr is required", findings: []string{"pod-cannot-start"}},
		{name: "redis without stream", profile: "spec:\n  redis: {addr: r}\n  collectors: [{kind: Node, interval: 1m}]\n",
			problem: "spec.redis.stream is required", findings: []string{"pod-cannot-start"}},
		{name: "nothing to watch", profile: "spec:\n  pitcher: {addr: http://omni/pitch}\n",
			problem: "at least one collector or informer must be defined", findings: []string{"pod-cannot-start"}},
		{name: "informer without events", profile: "spec:\n  pitcher: {addr: http://omni/pitch}\n  informers: [{version: v1, resource: pods}]\n",
			problem: "spec.informers[0].events must not be empty", findings: []string{"pod-cannot-start"}},
		{name: "collector without interval", profile: "spec:\n  pitcher: {addr: http://omni/pitch}\n  collectors: [{kind: Node}]\n",
			problem: "spec.collectors[0].interval must be positive", findings: []string{"pod-cannot-start"}},
		{name: "unparsable", profile: "spec: [oops\n", problem: "profile /etc/k8s-pitcher/profile.yaml: yaml:", findings: []string{"pod-cannot-start"}},
		{name: "no -profile", opts: []depOpt{args()}, profile: httpProfile, problem: "k8s-pitcher runs without -profile: " + exits, findings: []string{"pod-cannot-start"}},
		{name: "-profile without value", opts: []depOpt{args("--profile")}, profile: httpProfile, problem: "-profile has no value", findings: []string{"pod-cannot-start"}},
		{name: "key missing", profile: httpProfile, key: "other.yaml",
			problem: "profile /etc/k8s-pitcher/profile.yaml: ConfigMap k8s-profile has no key profile.yaml: " + exits, findings: []string{"pod-cannot-start"}},
		{name: "ConfigMap missing", problem: "ConfigMap k8s-profile does not exist: the pod cannot start", findings: []string{"pod-cannot-start"}},
		{name: "not on a volume", opts: []depOpt{args("-profile=/profile.yaml")}, profile: httpProfile,
			note: "streams unknown: its profile cannot be read: /profile.yaml is not on a mounted volume", findings: []string{"streams-unknown"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objs := pitchNamespace(k8sPitcher("k8s", tc.opts...))
			if tc.profile != "" {
				key := tc.key
				if key == "" {
					key = "profile.yaml"
				}
				objs = append(objs, configMap("k8s-profile", map[string]string{key: tc.profile}))
			}
			res := discover(t, objs...)
			checkK8sCase(t, res, component(t, res, "k8s"), tc)
		})
	}
}

func checkK8sCase(t *testing.T, res *Result, c *Component, tc k8sCase) {
	t.Helper()
	target := ""
	if c.PitchTarget != nil {
		target = c.PitchTarget.OmniPitcher
	}
	if target != tc.target || !slices.Equal(c.Streams, tc.streams) {
		t.Errorf("target %q streams %v, want %q %v (notes %v, start problems %v)", target, c.Streams, tc.target, tc.streams, c.Notes, c.StartProblems)
	}
	if tc.problem == "" && len(c.StartProblems) != 0 ||
		tc.problem != "" && !slices.ContainsFunc(c.StartProblems, func(p string) bool { return strings.Contains(p, tc.problem) }) {
		t.Errorf("start problems = %q, want %q", c.StartProblems, tc.problem)
	}
	if tc.note != "" && !hasNote(c, tc.note) {
		t.Errorf("notes = %q, want %q", c.Notes, tc.note)
	}
	if got := componentFindings(res, c.Name); !slices.Equal(got, tc.findings) {
		t.Errorf("findings = %v, want %v", got, tc.findings)
	}
}

func TestK8sPitcher_RedisStreamSource(t *testing.T) {
	res := discover(t, k8sPitcher("k8s"), configMap("k8s-profile", map[string]string{"profile.yaml": k8sRedisProfile}))
	c := component(t, res, "k8s")
	want := []Value{{Name: "spec.redis.stream", Value: "k8s-events", Source: "profile ConfigMap k8s-profile key profile.yaml"}}
	if !slices.Equal(c.StreamValues, want) || c.Profile == nil || c.Profile.Status != ProfileOK || !c.Routed() {
		t.Errorf("stream values %+v, profile %+v, routed %v", c.StreamValues, c.Profile, c.Routed())
	}
}

func TestProfileArg(t *testing.T) {
	cases := []struct {
		name          string
		command, args []string
		want          string
		found         bool
	}{
		{"flag and value", nil, []string{"--profile", "/p"}, "/p", true},
		{"single dash with =", nil, []string{"-profile=/p"}, "/p", true},
		{"after kubeconfig", nil, []string{"--kubeconfig", "/k", "--profile", "/p"}, "/p", true},
		{"after kubeconfig=", nil, []string{"--kubeconfig=/k", "-profile", "/p"}, "/p", true},
		{"in the command", []string{"/ko-app/k8s-pitcher", "--profile", "/p"}, nil, "/p", true},
		{"command and args", []string{"/ko-app/k8s-pitcher"}, []string{"--profile=/p"}, "/p", true},
		{"after a non-flag argument", nil, []string{"run", "--profile", "/p"}, "", false},
		{"after --", nil, []string{"--", "--profile", "/p"}, "", false},
		{"without value", nil, []string{"--profile"}, "", true},
		{"another flag", nil, []string{"--profiles=/p"}, "", false},
		{"none", nil, nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := profileArg(&corev1.Container{Command: tc.command, Args: tc.args})
			if got != tc.want || found != tc.found {
				t.Errorf("profileArg = %q, %v; want %q, %v", got, found, tc.want, tc.found)
			}
		})
	}
}
