package discovery

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stuttgart-things/homerun-library/v4/routing"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const ns = "homerun2"

type depOpt func(*appsv1.Deployment)

// deployment builds a Deployment the way the homerun2 KCL renders one: labels,
// one container named after the Deployment, REDIS_PASSWORD from a Secret.
func deployment(name, component, image string, opts ...depOpt) *appsv1.Deployment {
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				"app.kubernetes.io/name":    name,
				"app.kubernetes.io/part-of": "homerun2",
				ComponentLabel:              component,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  name,
						Image: image,
						Env: []corev1.EnvVar{
							{Name: "REDIS_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: name + "-redis"}, Key: "password",
							}}},
						},
					}},
				},
			},
		},
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

func env(name, value string) depOpt {
	return func(d *appsv1.Deployment) {
		c := &d.Spec.Template.Spec.Containers[0]
		c.Env = append(c.Env, corev1.EnvVar{Name: name, Value: value})
	}
}

func envVar(v corev1.EnvVar) depOpt {
	return func(d *appsv1.Deployment) {
		c := &d.Spec.Template.Spec.Containers[0]
		c.Env = append(c.Env, v)
	}
}

func envFrom(src corev1.EnvFromSource) depOpt {
	return func(d *appsv1.Deployment) {
		c := &d.Spec.Template.Spec.Containers[0]
		c.EnvFrom = append(c.EnvFrom, src)
	}
}

// configEnvFrom adds the <name>-config envFrom every homerun2 KCL renders.
func configEnvFrom(d *appsv1.Deployment) {
	envFrom(corev1.EnvFromSource{
		ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: d.Name + "-config"}},
	})(d)
}

// scaledToZero sets replicas to 0.
func scaledToZero(d *appsv1.Deployment) {
	var zero int32
	d.Spec.Replicas = &zero
}

func configMap(name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Data: data}
}

func discover(t *testing.T, objects ...runtime.Object) *Result {
	t.Helper()
	d := &Discoverer{Client: fake.NewClientset(objects...), Namespace: ns, LabelSelector: "app.kubernetes.io/part-of=homerun2"}
	res, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return res
}

func component(t *testing.T, res *Result, name string) *Component {
	t.Helper()
	for i := range res.Components {
		if res.Components[i].Name == name {
			return &res.Components[i]
		}
	}
	t.Fatalf("no component %s in %v", name, res.Components)
	return nil
}

func hasNote(c *Component, substr string) bool {
	return slices.ContainsFunc(c.Notes, func(n string) bool { return strings.Contains(n, substr) })
}

// test1 mirrors the Deployments running on homerun2-test1 (2026-09-11), reduced
// to what discovery reads.
func test1() []runtime.Object {
	objs := []runtime.Object{
		deployment("homerun2-core-catcher", "consumer", "ghcr.io/stuttgart-things/homerun2-core-catcher:v1.0.2", configEnvFrom,
			env("REDIS_STREAM", "messages"), env("CONSUMER_GROUP", "homerun2-core-catcher")),
		deployment("homerun2-demo-pitcher", "pitcher", "ghcr.io/stuttgart-things/homerun2-demo-pitcher:v2.0.1", configEnvFrom,
			env("PITCH_TARGET", "omni-pitcher"), env("REDIS_STREAM", "homerun")),
		deployment("homerun2-git-pitcher", "pitcher", "ghcr.io/stuttgart-things/homerun2-git-pitcher:v1.0.1", configEnvFrom,
			env("PITCHER_MODE", "redis"), env("REDIS_STREAM", "messages"), env("WATCH_CONFIG", "/config/watch-profile.yaml")),
		deployment("homerun2-k8s-pitcher", "watcher", "ghcr.io/stuttgart-things/homerun2-k8s-pitcher:v1.0.1"),
		deployment("homerun2-led-catcher", "led-catcher", "ghcr.io/stuttgart-things/homerun2-led-catcher:v0.7.0", configEnvFrom,
			env("REDIS_STREAM", "messages"), env("CONSUMER_GROUP", "homerun2-led-catcher"), env("PROFILE_PATH", "/config/profile.yaml")),
		deployment("homerun2-light-catcher", "light-catcher", "ghcr.io/stuttgart-things/homerun2-light-catcher:v1.0.0", configEnvFrom,
			env("REDIS_STREAM", "messages"), env("CONSUMER_GROUP", "homerun2-light-catcher"), env("PROFILE_PATH", "/config/profile.yaml")),
		deployment("homerun2-omni-pitcher", "api", "ghcr.io/stuttgart-things/homerun2-omni-pitcher:v2.1.2", configEnvFrom,
			env("ROUTES_CONFIG", "/config/routing/routes.yaml"), env("REDIS_STREAM", "messages")),
		deployment("homerun2-scout", "analytics", "ghcr.io/stuttgart-things/homerun2-scout:v0.8.2", configEnvFrom),
		deployment("homerun2-wled-mock", "wled-mock", "ghcr.io/stuttgart-things/homerun2-wled-mock:v1.0.0"),
		// Not selected: no part-of label.
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "redis-stack", Namespace: ns}},
	}
	for _, name := range []string{"core-catcher", "demo-pitcher", "git-pitcher", "led-catcher", "light-catcher", "omni-pitcher", "scout"} {
		objs = append(objs, configMap("homerun2-"+name+"-config", map[string]string{"LOG_LEVEL": "info"}))
	}
	return objs
}

func TestDiscover_Test1(t *testing.T) {
	res := discover(t, test1()...)

	if len(res.Components) != 9 {
		t.Fatalf("got %d components, want the 9 labeled Deployments", len(res.Components))
	}
	if !slices.IsSortedFunc(res.Components, func(a, b Component) int { return strings.Compare(a.Name, b.Name) }) {
		t.Error("components are not sorted by name")
	}

	cases := []struct {
		name    string
		kind    Kind
		role    routing.Role
		streams []string
		group   string
		routed  bool
	}{
		{"homerun2-core-catcher", KindCoreCatcher, routing.RoleCatcher, []string{"messages"}, "homerun2-core-catcher", true},
		{"homerun2-demo-pitcher", KindDemoPitcher, routing.RolePitcher, nil, "", false},
		{"homerun2-git-pitcher", KindGitPitcher, routing.RolePitcher, []string{"messages"}, "", true},
		{"homerun2-k8s-pitcher", KindK8sPitcher, routing.RolePitcher, nil, "", false},
		{"homerun2-led-catcher", KindLEDCatcher, routing.RoleCatcher, []string{"messages"}, "homerun2-led-catcher", true},
		{"homerun2-light-catcher", KindLightCatcher, routing.RoleCatcher, []string{"messages"}, "homerun2-light-catcher", true},
		{"homerun2-omni-pitcher", KindOmniPitcher, routing.RolePitcher, []string{"messages"}, "", true},
		{"homerun2-scout", KindScout, "", nil, "", false},
		{"homerun2-wled-mock", KindWLEDMock, "", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := component(t, res, tc.name)
			if c.Kind != tc.kind || c.Role != tc.role || !slices.Equal(c.Streams, tc.streams) || c.Routed() != tc.routed {
				t.Errorf("got kind=%s role=%q streams=%v routed=%v, notes %v", c.Kind, c.Role, c.Streams, c.Routed(), c.Notes)
			}
			if got := groupOf(c); got != tc.group {
				t.Errorf("consumer group = %q, want %q", got, tc.group)
			}
			if c.Container != tc.name || c.Replicas != 1 {
				t.Errorf("container = %q, replicas = %d", c.Container, c.Replicas)
			}
		})
	}
	checkTest1Notes(t, res)
}

func groupOf(c *Component) string {
	if c.ConsumerGroup == nil {
		return ""
	}
	return c.ConsumerGroup.Value
}

func checkTest1Notes(t *testing.T, res *Result) {
	t.Helper()
	demo := component(t, res, "homerun2-demo-pitcher")
	if demo.Mode == nil || demo.Mode.Value != "omni-pitcher" || !hasNote(demo, "over HTTP to omni-pitcher") {
		t.Errorf("demo-pitcher on test1 pitches over HTTP, REDIS_STREAM=homerun does not apply: mode %+v, notes %v", demo.Mode, demo.Notes)
	}
	if omni := component(t, res, "homerun2-omni-pitcher"); !hasNote(omni, "ROUTES_CONFIG") {
		t.Errorf("omni-pitcher must note its routes config: %v", omni.Notes)
	}
	if k8s := component(t, res, "homerun2-k8s-pitcher"); !hasNote(k8s, "profile") {
		t.Errorf("k8s-pitcher must say its target is not resolved: %v", k8s.Notes)
	}
	light := component(t, res, "homerun2-light-catcher")
	if light.ProfilePath == nil || light.ProfilePath.Value != "/config/profile.yaml" || light.ProfilePath.Source != "env" {
		t.Errorf("light-catcher profile path = %+v", light.ProfilePath)
	}

	if got := res.Streams(); !slices.Equal(got, []string{"messages"}) {
		t.Errorf("Streams() = %v", got)
	}
}

func TestDiscover_Defaults(t *testing.T) {
	// Nothing set: every service falls back to its own default.
	res := discover(t,
		deployment("core", "consumer", "img/homerun2-core-catcher:1"),
		deployment("light", "light-catcher", "img/homerun2-light-catcher:1"),
		deployment("led", "led-catcher", "img/homerun2-led-catcher:1"),
		deployment("notify", "notifier", "img/homerun2-notification-catcher:1"),
		deployment("omni", "api", "img/homerun2-omni-pitcher:1"),
		deployment("git", "pitcher", "img/homerun2-git-pitcher:1"),
		deployment("demo", "pitcher", "img/homerun2-demo-pitcher:1"),
	)

	cases := []struct {
		name, stream, group, profile string
	}{
		{"core", "messages", "homerun2-core-catcher", ""},
		{"light", "messages", "homerun2-light-catcher", "profile.yaml"},
		{"led", "messages", "homerun2-led-catcher", "profile.yaml"},
		{"notify", "alerts", "homerun2-notification-catcher", "/etc/notification-catcher/config.yaml"},
		{"omni", "messages", "", ""},
		{"git", "messages", "", ""},
		{"demo", "homerun", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := component(t, res, tc.name)
			if !slices.Equal(c.Streams, []string{tc.stream}) {
				t.Errorf("streams = %v, want [%s]", c.Streams, tc.stream)
			}
			for _, v := range c.StreamValues {
				if v.Source != SourceDefault {
					t.Errorf("%s source = %q, want default", v.Name, v.Source)
				}
			}
			if tc.group != "" && (c.ConsumerGroup == nil || c.ConsumerGroup.Value != tc.group || c.ConsumerGroup.Source != SourceDefault) {
				t.Errorf("group = %+v, want default %s", c.ConsumerGroup, tc.group)
			}
			if tc.profile != "" && (c.ProfilePath == nil || c.ProfilePath.Value != tc.profile) {
				t.Errorf("profile = %+v, want %s", c.ProfilePath, tc.profile)
			}
			if len(c.Notes) != 0 {
				t.Errorf("unexpected notes: %v", c.Notes)
			}
		})
	}
}

func TestDiscover_MultiStream(t *testing.T) {
	res := discover(t,
		deployment("core", "consumer", "img/homerun2-core-catcher:1", env("REDIS_STREAMS", "a, b,,c"), env("REDIS_STREAM", "ignored")),
		deployment("omni", "api", "img/homerun2-omni-pitcher:1", env("REDIS_STREAMS", "a,b")),
	)
	if got := component(t, res, "core").Streams; !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("catcher streams = %v", got)
	}
	// Pitchers read REDIS_STREAM only.
	if got := component(t, res, "omni").Streams; !slices.Equal(got, []string{"messages"}) {
		t.Errorf("pitcher streams = %v", got)
	}
}

func TestDiscover_EnvResolution(t *testing.T) {
	yes := true
	res := discover(t,
		configMap("core-config", map[string]string{"LOG_LEVEL": "info"}),
		configMap("shared", map[string]string{"REDIS_STREAM": "from-envfrom", "CONSUMER_GROUP": "g-envfrom"}),
		configMap("later", map[string]string{"REDIS_STREAM": "later-envfrom"}),
		configMap("prefixed", map[string]string{"STREAM": "prefixed-stream"}),
		configMap("keys", map[string]string{"group": "g-keyref"}),

		// env beats envFrom; a later envFrom beats an earlier one.
		deployment("core", "consumer", "img/homerun2-core-catcher:1",
			envFrom(corev1.EnvFromSource{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "shared"}}}),
			envFrom(corev1.EnvFromSource{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "later"}}}),
			env("CONSUMER_GROUP", "g-env"),
		),
		// envFrom prefix, configMapKeyRef, a later env entry of the same name wins.
		deployment("light", "light-catcher", "img/homerun2-light-catcher:1",
			envFrom(corev1.EnvFromSource{Prefix: "REDIS_", ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "prefixed"}}}),
			env("CONSUMER_GROUP", "first"),
			envVar(corev1.EnvVar{Name: "CONSUMER_GROUP", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "keys"}, Key: "group",
			}}}),
		),
		// Optional references to missing things leave the variable unset.
		deployment("led", "led-catcher", "img/homerun2-led-catcher:1",
			envFrom(corev1.EnvFromSource{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "gone"}, Optional: &yes}}),
			envVar(corev1.EnvVar{Name: "CONSUMER_GROUP", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "gone"}, Key: "g", Optional: &yes,
			}}}),
		),
	)

	core := component(t, res, "core")
	if !slices.Equal(core.Streams, []string{"later-envfrom"}) {
		t.Errorf("core streams = %v, want the later envFrom", core.Streams)
	}
	if core.ConsumerGroup.Value != "g-env" || core.ConsumerGroup.Source != "env" {
		t.Errorf("core group = %+v, want env over envFrom", core.ConsumerGroup)
	}

	light := component(t, res, "light")
	if !slices.Equal(light.Streams, []string{"prefixed-stream"}) {
		t.Errorf("light streams = %v, want the prefixed envFrom key", light.Streams)
	}
	if light.ConsumerGroup.Value != "g-keyref" || light.ConsumerGroup.Source != "ConfigMap keys key group" {
		t.Errorf("light group = %+v", light.ConsumerGroup)
	}

	led := component(t, res, "led")
	if led.ConsumerGroup.Source != SourceDefault || len(led.Notes) != 0 {
		t.Errorf("optional missing refs must fall back silently: group %+v, notes %v", led.ConsumerGroup, led.Notes)
	}
}

func TestDiscover_UnresolvableValues(t *testing.T) {
	res := discover(t,
		// The stream comes from a Secret: unknown, not the default.
		deployment("secret-stream", "consumer", "img/homerun2-core-catcher:1",
			envVar(corev1.EnvVar{Name: "REDIS_STREAM", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "s"}, Key: "stream",
			}}}),
		),
		// envFrom a Secret: the default is only likely.
		deployment("secret-envfrom", "light-catcher", "img/homerun2-light-catcher:1",
			envFrom(corev1.EnvFromSource{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "bundle"}}}),
		),
		// Missing non-optional ConfigMaps keep the pod from starting.
		deployment("missing-cm", "led-catcher", "img/homerun2-led-catcher:1",
			envVar(corev1.EnvVar{Name: "CONSUMER_GROUP", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "nope"}, Key: "g",
			}}}),
		),
		deployment("expansion", "consumer", "img/homerun2-core-catcher:1", env("REDIS_STREAM", "$(PREFIX)-messages")),
		deployment("podname", "consumer", "img/homerun2-core-catcher:1",
			envVar(corev1.EnvVar{Name: "CONSUMER_GROUP", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}}),
			envVar(corev1.EnvVar{Name: "REDIS_STREAM", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"}}}),
		),
	)

	if c := component(t, res, "secret-stream"); c.Streams != nil || c.Routed() || !hasNote(c, "streams unknown") {
		t.Errorf("secret-stream: streams %v, notes %v", c.Streams, c.Notes)
	}

	c := component(t, res, "secret-envfrom")
	if !slices.Equal(c.Streams, []string{"messages"}) || !hasNote(c, "envFrom Secret bundle may set it") {
		t.Errorf("secret-envfrom: streams %v, notes %v", c.Streams, c.Notes)
	}

	c = component(t, res, "missing-cm")
	if !hasNote(c, "ConfigMap nope does not exist: the pod cannot start") {
		t.Errorf("missing-cm: notes %v", c.Notes)
	}
	if want := "CONSUMER_GROUP: ConfigMap nope does not exist: the pod cannot start"; !slices.Contains(c.StartProblems, want) || c.Routed() {
		t.Errorf("a missing required keyRef keeps the pod from starting: start problems %v, routed %v", c.StartProblems, c.Routed())
	}

	if c = component(t, res, "expansion"); c.Streams != nil || !hasNote(c, "$(VAR) expansion") {
		t.Errorf("expansion: streams %v, notes %v", c.Streams, c.Notes)
	}

	c = component(t, res, "podname")
	if !slices.Equal(c.Streams, []string{ns}) {
		t.Errorf("fieldRef metadata.namespace resolves to the namespace: streams %v", c.Streams)
	}
	if !hasNote(c, "CONSUMER_GROUP: depends on the pod") {
		t.Errorf("podname: notes %v", c.Notes)
	}
}

func TestDiscover_MissingEnvFromConfigMap(t *testing.T) {
	res := discover(t, deployment("core", "consumer", "img/homerun2-core-catcher:1", configEnvFrom))
	c := component(t, res, "core")
	if len(c.StartProblems) != 1 || c.StartProblems[0] != "envFrom ConfigMap core-config does not exist: the pod cannot start" {
		t.Errorf("start problems %v", c.StartProblems)
	}
	if c.Routed() {
		t.Error("a pod that cannot start neither publishes nor reads")
	}
}

func TestDiscover_ReplicasAndContainers(t *testing.T) {
	sidecar := func(d *appsv1.Deployment) {
		spec := &d.Spec.Template.Spec
		spec.Containers = append([]corev1.Container{{Name: "istio-proxy", Image: "istio/proxyv2"}}, spec.Containers...)
	}
	renamed := func(d *appsv1.Deployment) {
		spec := &d.Spec.Template.Spec
		spec.Containers[0].Name = "main"
		spec.Containers = append(spec.Containers, corev1.Container{Name: "helper", Image: "busybox"})
	}
	none := func(d *appsv1.Deployment) { d.Spec.Template.Spec.Containers = nil }

	res := discover(t,
		deployment("zero", "consumer", "img/homerun2-core-catcher:1", scaledToZero),
		deployment("sidecar", "consumer", "img/homerun2-core-catcher:1", sidecar),
		deployment("renamed", "consumer", "img/homerun2-core-catcher:1", renamed),
		deployment("empty", "consumer", "img/homerun2-core-catcher:1", none),
	)

	if c := component(t, res, "zero"); c.Running() || c.Routed() || !hasNote(c, "scaled to zero") || len(c.Streams) != 1 {
		t.Errorf("zero: running=%v routed=%v streams=%v notes=%v", c.Running(), c.Routed(), c.Streams, c.Notes)
	}
	if c := component(t, res, "sidecar"); c.Container != "sidecar" || len(c.Notes) != 0 {
		t.Errorf("the container named like the Deployment wins over a sidecar: %q, %v", c.Container, c.Notes)
	}
	if c := component(t, res, "renamed"); c.Container != "main" || !hasNote(c, "read container main") {
		t.Errorf("renamed: %q, %v", c.Container, c.Notes)
	}
	if c := component(t, res, "empty"); c.Kind != KindUnknown || c.Routed() || !hasNote(c, "no containers") {
		t.Errorf("empty: %+v", c)
	}
}

func TestDiscover_PitcherModes(t *testing.T) {
	res := discover(t,
		deployment("demo-redis", "pitcher", "img/homerun2-demo-pitcher:1", env("PITCH_TARGET", "redis"), env("REDIS_STREAM", "s")),
		deployment("demo-both", "pitcher", "img/homerun2-demo-pitcher:1", env("PITCH_TARGET", "both"), env("REDIS_STREAM", "s")),
		deployment("demo-file", "pitcher", "img/homerun2-demo-pitcher:1", env("PITCH_TARGET", "file")),
		deployment("demo-http", "pitcher", "img/homerun2-demo-pitcher:1", env("PITCH_TARGET", "http"), env("REDIS_STREAM", "s")),
		deployment("git-file", "pitcher", "img/homerun2-git-pitcher:1", env("PITCHER_MODE", "file")),
		deployment("git-odd", "pitcher", "img/homerun2-git-pitcher:1", env("PITCHER_MODE", "stream")),
	)

	cases := []struct {
		name    string
		streams []string
		note    string
	}{
		{"demo-redis", []string{"s"}, ""},
		{"demo-both", []string{"s"}, "also pitches over HTTP"},
		{"demo-file", nil, "writes to a file"},
		{"demo-http", []string{"s"}, `PITCH_TARGET="http" is not one of`},
		{"git-file", nil, "writes to a file"},
		{"git-odd", []string{"messages"}, `PITCHER_MODE="stream" is not file`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := component(t, res, tc.name)
			if !slices.Equal(c.Streams, tc.streams) {
				t.Errorf("streams = %v, want %v", c.Streams, tc.streams)
			}
			if tc.note == "" && len(c.Notes) != 0 || tc.note != "" && !hasNote(c, tc.note) {
				t.Errorf("notes = %v, want %q", c.Notes, tc.note)
			}
		})
	}
}

func TestDiscover_UnknownComponents(t *testing.T) {
	res := discover(t,
		deployment("mystery", "frontend", "img/something:1"),
		deployment("homerun2-new-pitcher", "pitcher", "img/homerun2-new-pitcher:1", env("REDIS_STREAM", "x")),
		deployment("unset-pitcher", "pitcher", "img/homerun2-new-pitcher:1"),
	)
	if c := component(t, res, "mystery"); c.Kind != KindUnknown || c.Role != "" || c.Routed() || !hasNote(c, `"frontend" is not a known`) {
		t.Errorf("mystery: %+v", c)
	}
	if c := component(t, res, "homerun2-new-pitcher"); c.Kind != KindPitcher || !slices.Equal(c.Streams, []string{"x"}) || !hasNote(c, "default stream is unknown") {
		t.Errorf("new pitcher with REDIS_STREAM: %+v", c)
	}
	if c := component(t, res, "unset-pitcher"); c.Streams != nil || c.Routed() {
		t.Errorf("a pitcher of unknown kind without REDIS_STREAM has no known stream: %+v", c)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		label, name, image string
		want               Kind
	}{
		{"api", "x", "", KindOmniPitcher},
		{"notifier", "x", "", KindNotificationCatcher},
		{"pitcher", "anything", "ghcr.io/stuttgart-things/homerun2-demo-pitcher:v2.0.1", KindDemoPitcher},
		{"pitcher", "homerun2-demo-pitcher", "registry:5000/org/homerun2-git-pitcher@sha256:abc", KindGitPitcher},
		{"pitcher", "my-git-pitcher", "mirror/pitcher:1", KindGitPitcher},
		{"pitcher", "p", "mirror/pitcher:1", KindPitcher},
		{"config-viewer", "homerun2-config-viewer", "", KindConfigViewer},
		{"", "x", "", KindUnknown},
	}
	for _, tc := range cases {
		if got := classify(tc.label, tc.name, tc.image); got != tc.want {
			t.Errorf("classify(%q, %q, %q) = %s, want %s", tc.label, tc.name, tc.image, got, tc.want)
		}
	}
}

func TestDiscover_ListErrors(t *testing.T) {
	for _, resource := range []string{"deployments", "configmaps"} {
		t.Run(resource, func(t *testing.T) {
			client := fake.NewClientset()
			client.PrependReactor("list", resource, func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("forbidden")
			})
			d := &Discoverer{Client: client, Namespace: ns, LabelSelector: "app.kubernetes.io/part-of=homerun2"}
			_, err := d.Discover(context.Background())
			if err == nil || !strings.Contains(err.Error(), resource) || !strings.Contains(err.Error(), "forbidden") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestDiscover_UsesLabelSelectorAndNamespace(t *testing.T) {
	other := deployment("elsewhere", "consumer", "img/homerun2-core-catcher:1")
	other.Namespace = "other"
	unlabelled := deployment("unlabelled", "consumer", "img/homerun2-core-catcher:1")
	delete(unlabelled.Labels, "app.kubernetes.io/part-of")

	res := discover(t, other, unlabelled, deployment("here", "consumer", "img/homerun2-core-catcher:1"))
	if len(res.Components) != 1 || res.Components[0].Name != "here" {
		t.Errorf("components = %v", res.Components)
	}
}

func TestDiscover_ConfigMapsAreNotFilteredByLabel(t *testing.T) {
	res := discover(t, configMap("homerun2-omni-pitcher-routes", map[string]string{"routes.yaml": "x"}))
	if _, ok := res.ConfigMap("homerun2-omni-pitcher-routes"); !ok {
		t.Error("an unlabelled ConfigMap must be available for profile resolution")
	}
}

func TestDiscover_ViewerListsItselfWithoutANote(t *testing.T) {
	res := discover(t, deployment("homerun2-config-viewer", "config-viewer", "ghcr.io/stuttgart-things/homerun2-config-viewer:latest"))
	c := component(t, res, "homerun2-config-viewer")
	if c.Kind != KindConfigViewer || c.Role != "" || c.Routed() || len(c.Notes) != 0 {
		t.Errorf("the viewer is a known component outside routing, got %+v", c)
	}
}
