package discovery

import (
	"strings"
	"testing"

	homerun "github.com/stuttgart-things/homerun-library/v4"
	"github.com/stuttgart-things/homerun-library/v4/routing"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// lightProfileYAML and ledProfileYAML are the profiles deployed on
// homerun2-test1 (2026-09-11), comments dropped.
const lightProfileYAML = `---
effects:
  error:
    systems: ["*"]
    severity: [error, critical]
    fx: Blurz
    duration: 3
    color: sunset
    segments: [0]
    endpoint: http://homerun2-wled-mock
  warning:
    systems: ["*"]
    severity: [warning]
    fx: Twinkle
    duration: 3
    color: beach
    segments: [0]
    endpoint: http://homerun2-wled-mock
  success:
    systems: ["*"]
    severity: [success]
    fx: Aurora
    duration: 3
    color: forest
    segments: [0]
    endpoint: http://homerun2-wled-mock
  info:
    systems: ["*"]
    severity: [info]
    fx: DJ Light
    duration: 3
    color: ocean
    segments: [0]
    endpoint: http://homerun2-wled-mock
`

const ledProfileYAML = `displayRules:
  error-all:
    systems: ["*"]
    severity: [ERROR, CRITICAL]
    kind: text
    text: "{{ system }}: {{ title }}"
    font: 6x10.bdf
    duration: 5
  warning-all:
    systems: ["*"]
    severity: [WARNING]
    kind: text
    text: "{{ system }}: {{ title }}"
    font: 6x10.bdf
    duration: 5
  tabletennis-score:
    systems: [tabletennis]
    severity: [INFO, SUCCESS]
    kind: static
    text: "{{ title }}"
    font: 6x10.bdf
    hold: true
    duration: 3
  default-info:
    systems: ["*"]
    severity: [INFO, SUCCESS]
    kind: text
    text: "{{ system }}: {{ title }}"
    font: 6x10.bdf
    duration: 5
colors:
  error: [255, 0, 0]
  critical: [255, 0, 0]
  warning: [255, 165, 0]
  success: [0, 255, 0]
  info: [0, 100, 255]
  debug: [128, 128, 128]
`

const notifyConfigYAML = `outputs:
  - name: teams-ops
    type: msteams
    webhook_url: ${TEAMS_WEBHOOK}
    filters:
      severity_min: warning
`

func mount(volume, mountPath, subPath string) depOpt {
	return func(d *appsv1.Deployment) {
		c := &d.Spec.Template.Spec.Containers[0]
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: volume, MountPath: mountPath, SubPath: subPath, ReadOnly: true})
	}
}

func volume(v corev1.Volume) depOpt {
	return func(d *appsv1.Deployment) {
		d.Spec.Template.Spec.Volumes = append(d.Spec.Template.Spec.Volumes, v)
	}
}

func cmVolume(name, configMap string, items ...corev1.KeyToPath) depOpt {
	return volume(corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{Name: configMap}, Items: items,
	}}})
}

func optionalCMVolume(name, configMap string) depOpt {
	yes := true
	return volume(corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{Name: configMap}, Optional: &yes,
	}}})
}

// lightCatcher is light-catcher as its KCL renders it: PROFILE_PATH points
// into the <name>-profile ConfigMap mounted at /config.
func lightCatcher(name string, opts ...depOpt) *appsv1.Deployment {
	base := []depOpt{
		env("PROFILE_PATH", "/config/profile.yaml"),
		mount("profile", "/config", ""),
		cmVolume("profile", name+"-profile"),
	}
	return deployment(name, "light-catcher", "img/homerun2-light-catcher:1", append(base, opts...)...)
}

func profileOf(t *testing.T, objs ...runtime.Object) *Component {
	t.Helper()
	res := discover(t, objs...)
	for i := range res.Components {
		if res.Components[i].ProfilePath != nil {
			return &res.Components[i]
		}
	}
	t.Fatal("no component with a profile")
	return nil
}

func TestProfiles_Test1(t *testing.T) {
	objs := append(withProfileMounts(test1()),
		configMap("homerun2-light-catcher-profile", map[string]string{"profile.yaml": lightProfileYAML}),
		configMap("homerun2-led-catcher-profile", map[string]string{"profile.yaml": ledProfileYAML}),
	)
	res := discover(t, objs...)

	light := component(t, res, "homerun2-light-catcher")
	if light.Profile == nil || light.Profile.Status != ProfileOK || light.Profile.ConfigMap != "homerun2-light-catcher-profile" || light.Profile.Key != "profile.yaml" {
		t.Fatalf("light profile = %+v", light.Profile)
	}
	if p, ok := light.rules.(*routing.LightProfile); !ok || len(p.Effects) != 4 {
		t.Errorf("light rules = %#v", light.rules)
	}
	led := component(t, res, "homerun2-led-catcher")
	if led.Profile.Status != ProfileOK {
		t.Fatalf("led profile = %+v", led.Profile)
	}
	if _, ok := led.rules.(*routing.LEDProfile); !ok {
		t.Errorf("led rules = %#v", led.rules)
	}
	if core := component(t, res, "homerun2-core-catcher"); core.Profile != nil || core.rules != nil {
		t.Errorf("core-catcher has no profile: %+v", core.Profile)
	}

	checkTest1Routing(t, res)
}

// checkTest1Routing checks what homerun-library routing makes of test1.
func checkTest1Routing(t *testing.T, res *Result) {
	t.Helper()
	comps := res.RoutingComponents()
	var names []string
	for _, c := range comps {
		names = append(names, c.Name)
	}
	want := "homerun2-core-catcher,homerun2-git-pitcher,homerun2-led-catcher,homerun2-light-catcher,homerun2-omni-pitcher"
	if strings.Join(names, ",") != want {
		t.Errorf("routed = %v, want %s (demo-pitcher over HTTP and k8s-pitcher unresolved are left out)", names, want)
	}

	if findings := routing.Check(comps, []string{"error", "critical"}); len(findings) != 0 {
		t.Errorf("test1 as deployed has no findings, got %+v", findings)
	}

	for _, d := range routing.DryRun(comps, "messages", homerun.Message{Title: "Build failed", Severity: "error", System: "github"}) {
		if !d.Receives || len(d.Reactions) != 1 {
			t.Errorf("%s: %+v", d.Component, d)
			continue
		}
		r := d.Reactions[0]
		switch d.Component {
		case "homerun2-light-catcher":
			if r.Rule != "error" || r.Problem != "" {
				t.Errorf("light: %+v", r)
			}
		case "homerun2-led-catcher":
			if r.Rule != "error-all" || r.Problem != "" {
				t.Errorf("led: %+v", r)
			}
		}
	}
}

// withProfileMounts adds the /config profile mount the KCL renders to the
// light- and led-catcher Deployments of the test1 fixture.
func withProfileMounts(objs []runtime.Object) []runtime.Object {
	for _, o := range objs {
		if d, ok := o.(*appsv1.Deployment); ok && (d.Name == "homerun2-light-catcher" || d.Name == "homerun2-led-catcher") {
			mount("profile", "/config", "")(d)
			cmVolume("profile", d.Name+"-profile")(d)
		}
	}
	return objs
}

func TestProfiles_NotificationCatcherItems(t *testing.T) {
	// notification-catcher's KCL: CONFIG_PATH from the <name>-env envFrom, and
	// a volume projecting only config.yaml.
	c := profileOf(t,
		configMap("notify-env", map[string]string{"CONFIG_PATH": "/etc/notification-catcher/config.yaml"}),
		configMap("notify-notify", map[string]string{"config.yaml": notifyConfigYAML, "other.yaml": "x"}),
		deployment("notify", "notifier", "img/homerun2-notification-catcher:1",
			envFrom(corev1.EnvFromSource{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "notify-env"}}}),
			mount("notify-config", "/etc/notification-catcher", ""),
			cmVolume("notify-config", "notify-notify", corev1.KeyToPath{Key: "config.yaml", Path: "config.yaml"}),
		),
	)
	if c.Profile.Status != ProfileOK || c.Profile.Key != "config.yaml" {
		t.Fatalf("profile = %+v", c.Profile)
	}
	cfg, ok := c.rules.(*routing.NotificationConfig)
	if !ok || len(cfg.Outputs) != 1 {
		t.Errorf("rules = %#v", c.rules)
	}
}

func TestProfiles_Resolution(t *testing.T) {
	light := func(name, data string) *corev1.ConfigMap {
		return configMap(name, map[string]string{"profile.yaml": data})
	}

	cases := []struct {
		name       string
		objs       []runtime.Object
		status     ProfileStatus
		key        string
		msg        string
		noEffect   bool // the message must not describe the catcher's reaction
		evaluateFn func(t *testing.T, p routing.Profile)
	}{
		{
			name:   "configmap volume",
			objs:   []runtime.Object{light("l-profile", lightProfileYAML), lightCatcher("l")},
			status: ProfileOK, key: "profile.yaml",
		},
		{
			name: "subPath file mount",
			objs: []runtime.Object{light("rules", lightProfileYAML), deployment("l", "light-catcher", "img/homerun2-light-catcher:1",
				env("PROFILE_PATH", "/etc/light/profile.yaml"), mount("p", "/etc/light/profile.yaml", "profile.yaml"), cmVolume("p", "rules"))},
			status: ProfileOK, key: "profile.yaml",
		},
		{
			name: "items map another key to the path",
			objs: []runtime.Object{configMap("rules", map[string]string{"light.yaml": lightProfileYAML}), deployment("l", "light-catcher", "img/homerun2-light-catcher:1",
				env("PROFILE_PATH", "/config/profile.yaml"), mount("p", "/config", ""), cmVolume("p", "rules", corev1.KeyToPath{Key: "light.yaml", Path: "profile.yaml"}))},
			status: ProfileOK, key: "light.yaml",
		},
		{
			name: "subPath directory with a nested item path",
			objs: []runtime.Object{configMap("rules", map[string]string{"light.yaml": lightProfileYAML}), deployment("l", "light-catcher", "img/homerun2-light-catcher:1",
				env("PROFILE_PATH", "/config/profile.yaml"), mount("p", "/config", "team-a"), cmVolume("p", "rules", corev1.KeyToPath{Key: "light.yaml", Path: "team-a/profile.yaml"}))},
			status: ProfileOK, key: "light.yaml",
		},
		{
			name: "binaryData key",
			objs: []runtime.Object{&corev1.ConfigMap{ObjectMeta: configMap("l-profile", nil).ObjectMeta, BinaryData: map[string][]byte{"profile.yaml": []byte(lightProfileYAML)}},
				lightCatcher("l")},
			status: ProfileOK, key: "profile.yaml",
		},
		{
			name: "longest mount wins",
			objs: []runtime.Object{light("rules", lightProfileYAML), deployment("l", "light-catcher", "img/homerun2-light-catcher:1",
				env("PROFILE_PATH", "/config/rules/profile.yaml"),
				mount("scratch", "/config", ""), volume(corev1.Volume{Name: "scratch", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}),
				mount("p", "/config/rules", ""), cmVolume("p", "rules"))},
			status: ProfileOK, key: "profile.yaml",
		},
		{
			name: "relative path with a workingDir",
			objs: []runtime.Object{light("l-profile", lightProfileYAML), deployment("l", "light-catcher", "img/homerun2-light-catcher:1",
				func(d *appsv1.Deployment) { d.Spec.Template.Spec.Containers[0].WorkingDir = "/app" },
				mount("p", "/app", ""), cmVolume("p", "l-profile"))},
			status: ProfileOK, key: "profile.yaml",
		},

		{
			name:   "missing configmap keeps the pod from starting",
			objs:   []runtime.Object{lightCatcher("l")},
			status: ProfileMissing, msg: "ConfigMap l-profile does not exist: the pod cannot start", noEffect: true,
		},
		{
			name: "optional volume without its configmap",
			objs: []runtime.Object{deployment("l", "light-catcher", "img/homerun2-light-catcher:1",
				env("PROFILE_PATH", "/config/profile.yaml"), mount("p", "/config", ""), optionalCMVolume("p", "gone"))},
			status: ProfileMissing, msg: "light-catcher lights nothing",
		},
		{
			name:   "configmap without the key",
			objs:   []runtime.Object{configMap("l-profile", map[string]string{"other.yaml": "x"}), lightCatcher("l")},
			status: ProfileMissing, key: "profile.yaml", msg: "has no key profile.yaml; light-catcher lights nothing",
		},
		{
			name: "listed item missing from the configmap",
			objs: []runtime.Object{configMap("rules", map[string]string{"x": "y"}), deployment("l", "light-catcher", "img/homerun2-light-catcher:1",
				env("PROFILE_PATH", "/config/profile.yaml"), mount("p", "/config", ""), cmVolume("p", "rules", corev1.KeyToPath{Key: "light.yaml", Path: "profile.yaml"}))},
			status: ProfileMissing, key: "light.yaml", msg: "the pod cannot start", noEffect: true,
		},
		{
			name: "items do not project the path",
			objs: []runtime.Object{light("rules", lightProfileYAML), deployment("l", "light-catcher", "img/homerun2-light-catcher:1",
				env("PROFILE_PATH", "/config/profile.yaml"), mount("p", "/config", ""), cmVolume("p", "rules", corev1.KeyToPath{Key: "profile.yaml", Path: "other.yaml"}))},
			status: ProfileMissing, msg: "none is at profile.yaml",
		},
		{
			name:   "no subdirectories without items",
			objs:   []runtime.Object{light("l-profile", lightProfileYAML), lightCatcher("l", env("PROFILE_PATH", "/config/sub/profile.yaml"))},
			status: ProfileMissing, msg: "has no subdirectories",
		},
		{
			name:   "invalid yaml",
			objs:   []runtime.Object{light("l-profile", "effects: [unclosed"), lightCatcher("l")},
			status: ProfileInvalid, key: "profile.yaml", msg: "failed to parse light-catcher profile",
			evaluateFn: func(t *testing.T, p routing.Profile) {
				t.Helper()
				if _, ok := p.(routing.InvalidProfile); !ok {
					t.Errorf("rules = %#v, want routing.InvalidProfile", p)
				}
			},
		},

		{
			name:   "relative default path without workingDir",
			objs:   []runtime.Object{deployment("l", "light-catcher", "img/homerun2-light-catcher:1")},
			status: ProfileUnresolved, msg: `PROFILE_PATH="profile.yaml" is relative`,
		},
		{
			name: "a mount path is a directory, not a string prefix",
			objs: []runtime.Object{light("l-profile", lightProfileYAML), deployment("l", "light-catcher", "img/homerun2-light-catcher:1",
				env("PROFILE_PATH", "/configuration/profile.yaml"), mount("p", "/config", ""), cmVolume("p", "l-profile"))},
			status: ProfileUnresolved, msg: "/configuration/profile.yaml is not on a mounted volume",
		},
		{
			name:   "path not on a volume",
			objs:   []runtime.Object{deployment("l", "light-catcher", "img/homerun2-light-catcher:1", env("PROFILE_PATH", "/etc/profile.yaml"))},
			status: ProfileUnresolved, msg: "not on a mounted volume",
		},
		{
			name: "emptyDir volume",
			objs: []runtime.Object{deployment("l", "light-catcher", "img/homerun2-light-catcher:1", env("PROFILE_PATH", "/config/profile.yaml"),
				mount("p", "/config", ""), volume(corev1.Volume{Name: "p", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}))},
			status: ProfileUnresolved, msg: "type emptyDir",
		},
		{
			name: "projected volume",
			objs: []runtime.Object{deployment("l", "light-catcher", "img/homerun2-light-catcher:1", env("PROFILE_PATH", "/config/profile.yaml"),
				mount("p", "/config", ""), volume(corev1.Volume{Name: "p", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{}}}))},
			status: ProfileUnresolved, msg: "type projected",
		},
		{
			name:   "path is the mount point",
			objs:   []runtime.Object{light("l-profile", lightProfileYAML), lightCatcher("l", env("PROFILE_PATH", "/config"))},
			status: ProfileUnresolved, msg: "a directory",
		},
		{
			name: "path from a secret",
			objs: []runtime.Object{deployment("l", "light-catcher", "img/homerun2-light-catcher:1",
				envVar(corev1.EnvVar{Name: "PROFILE_PATH", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "s"}, Key: "path",
				}}}))},
			status: ProfileUnresolved, msg: "comes from a Secret",
			evaluateFn: func(t *testing.T, p routing.Profile) {
				t.Helper()
				r := p.Evaluate(homerun.Message{Severity: "error"})
				if len(r) != 1 || r[0].Summary != "unknown" || !strings.Contains(r[0].Problem, "profile not resolved") {
					t.Errorf("an unresolved profile must not pretend to react to everything: %+v", r)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := profileOf(t, tc.objs...)
			checkProfile(t, c, tc.status, tc.key, tc.msg, tc.noEffect)
			if tc.evaluateFn != nil {
				tc.evaluateFn(t, c.rules)
			}
		})
	}
}

func checkProfile(t *testing.T, c *Component, status ProfileStatus, key, msg string, noEffect bool) {
	t.Helper()
	ref := c.Profile
	if ref == nil || ref.Status != status || ref.Key != key || !strings.Contains(ref.Message, msg) {
		t.Fatalf("profile = %+v, want status %s key %q message containing %q", ref, status, key, msg)
	}
	if noEffect && strings.Contains(ref.Message, "lights nothing") {
		t.Errorf("a pod that cannot start does not run, so what it does without a profile does not apply: %q", ref.Message)
	}
	if c.rules == nil {
		t.Fatal("a catcher with a profile path must never get a nil profile - nil means it reacts to everything")
	}
	if status == ProfileOK {
		if _, ok := c.rules.(*routing.LightProfile); !ok {
			t.Errorf("rules = %#v", c.rules)
		}
	}
}

func TestProfiles_MissingPerCatcher(t *testing.T) {
	cases := []struct {
		name, component, image, env, msg string
		empty                            bool
	}{
		{"light-catcher", "light-catcher", "img/homerun2-light-catcher:1", "PROFILE_PATH", "light-catcher lights nothing", false},
		{"led-catcher", "led-catcher", "img/homerun2-led-catcher:1", "PROFILE_PATH", "led-catcher runs with an empty profile", true},
		{"notification-catcher", "notifier", "img/homerun2-notification-catcher:1", "CONFIG_PATH", "notification-catcher does not start", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := profileOf(t, deployment("x", tc.component, tc.image,
				env(tc.env, "/config/p.yaml"), mount("p", "/config", ""), optionalCMVolume("p", "gone")))
			if c.Profile.Status != ProfileMissing || !strings.Contains(c.Profile.Message, tc.msg) {
				t.Fatalf("profile = %+v", c.Profile)
			}
			r := c.rules.Evaluate(homerun.Message{Severity: "critical", System: "x"})
			if tc.empty {
				if len(r) != 0 {
					t.Errorf("an empty led-catcher profile reacts to nothing, got %+v", r)
				}
				return
			}
			if len(r) != 1 || r[0].Summary != "does nothing" || r[0].Problem == "" {
				t.Errorf("reactions = %+v", r)
			}
		})
	}
}

func TestRoutingComponents(t *testing.T) {
	res := discover(t,
		configMap("light-profile", map[string]string{"profile.yaml": lightProfileYAML}),
		lightCatcher("light"),
		deployment("core", "consumer", "img/homerun2-core-catcher:1"),
		deployment("stopped", "consumer", "img/homerun2-core-catcher:1", scaledToZero),
		deployment("secret-group", "consumer", "img/homerun2-core-catcher:1",
			envVar(corev1.EnvVar{Name: "CONSUMER_GROUP", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "s"}, Key: "g",
			}}})),
		// Unresolved but not empty: the literal must not reach routing.
		deployment("expanded-group", "consumer", "img/homerun2-core-catcher:1", env("CONSUMER_GROUP", "$(TEAM)-group")),
		deployment("scout", "analytics", "img/homerun2-scout:1"),
	)

	byName := map[string]routing.Component{}
	for _, c := range res.RoutingComponents() {
		byName[c.Name] = c
	}
	if len(byName) != 4 {
		t.Fatalf("routed = %v, want light, core, secret-group and expanded-group", byName)
	}
	if byName["core"].Profile != nil || byName["core"].ConsumerGroup != "homerun2-core-catcher" {
		t.Errorf("core = %+v", byName["core"])
	}
	if _, ok := byName["light"].Profile.(*routing.LightProfile); !ok {
		t.Errorf("light profile = %#v", byName["light"].Profile)
	}
	for _, name := range []string{"secret-group", "expanded-group"} {
		if g := byName[name].ConsumerGroup; g != "" {
			t.Errorf("%s: an unresolved consumer group must be passed as unknown, got %q", name, g)
		}
	}
}
