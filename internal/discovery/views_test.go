package discovery

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stuttgart-things/homerun-library/v4/routing"
	corev1 "k8s.io/api/core/v1"
)

func findingSet(findings []routing.Finding) []string {
	var out []string
	for _, f := range findings {
		out = append(out, string(f.Kind)+":"+f.Component)
	}
	sort.Strings(out)
	return out
}

func TestFindings(t *testing.T) {
	res := discover(t,
		deployment("omni", "api", "img/homerun2-omni-pitcher:1", env("REDIS_STREAM", "messages")),
		// The homerun2-test1 mix-up: publishes to a stream nobody reads.
		deployment("demo", "pitcher", "img/homerun2-demo-pitcher:1", env("PITCH_TARGET", "redis"), env("REDIS_STREAM", "homerun")),
		// envFrom a ConfigMap that does not exist.
		deployment("core", "consumer", "img/homerun2-core-catcher:1", configEnvFrom),
		configMap("light-profile", map[string]string{"profile.yaml": lightProfileYAML}),
		lightCatcher("light"),
		// No profile file: led-catcher runs with an empty profile.
		deployment("led", "led-catcher", "img/homerun2-led-catcher:1",
			env("PROFILE_PATH", "/config/profile.yaml"), mount("p", "/config", ""), optionalCMVolume("p", "gone")),
		// Its required config ConfigMap is missing: it does not start, and that
		// is reported once, not also as a missing profile.
		deployment("notify", "notifier", "img/homerun2-notification-catcher:1",
			mount("n", "/etc/notification-catcher", ""), cmVolume("n", "notify-notify")),
		deployment("secret", "consumer", "img/homerun2-core-catcher:1",
			envVar(corev1.EnvVar{Name: "REDIS_STREAM", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "s"}, Key: "stream",
			}}})),
		deployment("stopped", "consumer", "img/homerun2-core-catcher:1", scaledToZero),
		// Relative PROFILE_PATH without workingDir. Its own consumer group, so
		// it does not share light's default one.
		deployment("unres", "light-catcher", "img/homerun2-light-catcher:1", env("CONSUMER_GROUP", "unres")),
	)

	got := findingSet(res.Findings([]string{"error", "critical"}))
	want := []string{
		"pod-cannot-start:core",
		"pod-cannot-start:notify",
		"profile-missing:led",
		"profile-unresolved:unres",
		"scaled-to-zero:stopped",
		"streams-unknown:secret",
		"uncovered-severity:led",
		"unread-stream:demo",
	}
	if !slices.Equal(got, want) {
		t.Errorf("findings =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}

	for _, f := range res.Findings(nil) {
		if f.Component != "" && !strings.HasPrefix(f.Message, f.Component+" ") && !strings.HasPrefix(f.Message, f.Component+":") {
			t.Errorf("finding message does not name its component: %+v", f)
		}
	}
}

func TestStreamUses(t *testing.T) {
	res := discover(t,
		deployment("omni", "api", "img/homerun2-omni-pitcher:1", env("REDIS_STREAM", "messages")),
		deployment("demo", "pitcher", "img/homerun2-demo-pitcher:1", env("PITCH_TARGET", "redis"), env("REDIS_STREAM", "homerun")),
		deployment("core", "consumer", "img/homerun2-core-catcher:1", env("REDIS_STREAMS", "messages,alerts")),
		deployment("notify", "notifier", "img/homerun2-notification-catcher:1", env("CONSUMER_GROUP", "n")),
		deployment("stopped", "consumer", "img/homerun2-core-catcher:1", scaledToZero, env("REDIS_STREAM", "messages")),
	)

	uses := res.StreamUses()
	var got []string
	for _, u := range uses {
		var catchers []string
		for _, c := range u.Catchers {
			catchers = append(catchers, c.Name+"("+c.ConsumerGroup+")")
		}
		var pitchers []string
		for _, p := range u.Pitchers {
			pitchers = append(pitchers, p.Name)
			if len(p.When) > 0 {
				t.Errorf("%s: a pitcher without routes publishes every message, got when %v", p.Name, p.When)
			}
		}
		got = append(got, u.Stream+" pitchers="+strings.Join(pitchers, ",")+" catchers="+strings.Join(catchers, ","))
	}
	want := []string{
		"alerts pitchers= catchers=core(homerun2-core-catcher),notify(n)",
		"homerun pitchers=demo catchers=",
		"messages pitchers=omni catchers=core(homerun2-core-catcher)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("stream uses =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	for _, u := range uses {
		if u.Pitchers == nil || u.Catchers == nil {
			t.Errorf("%s: empty lists must encode as [], not null", u.Stream)
		}
	}
}

func TestOnDemandStreams(t *testing.T) {
	res := discover(t,
		deployment("score", "pitcher", "img/homerun2-demo-pitcher:1", env("PITCH_TARGET", "redis"), env("REDIS_STREAM", "tabletennis")),
		deployment("demo", "pitcher", "img/homerun2-demo-pitcher:1", env("PITCH_TARGET", "redis"), env("REDIS_STREAM", "homerun")),
		deployment("led", "led-catcher", "img/homerun2-led-catcher:1",
			env("REDIS_STREAMS", "messages,alerts"), env("UI_STREAM_PRESETS", "messages|alerts, tabletennis | scale,,messages")),
		// Scaled to zero, its presets read nothing.
		deployment("stopped", "led-catcher", "img/homerun2-led-catcher:1", scaledToZero,
			env("CONSUMER_GROUP", "stopped"), env("UI_STREAM_PRESETS", "homerun")),
	)

	led := component(t, res, "led")
	if !slices.Equal(led.OnDemandStreams, []string{"tabletennis", "scale"}) {
		t.Errorf("on-demand streams = %v", led.OnDemandStreams)
	}
	if !slices.ContainsFunc(led.Notes, func(n string) bool { return strings.Contains(n, `"tabletennis", "scale" only on demand`) }) {
		t.Errorf("notes = %v", led.Notes)
	}
	// Read on demand, not from the start: the matrix does not list it.
	for _, c := range res.RoutingComponents() {
		if c.Role == routing.RoleCatcher && slices.Contains(c.Streams, "tabletennis") {
			t.Errorf("%s reads tabletennis from the start", c.Name)
		}
	}

	var unread []string
	for _, f := range res.Findings(nil) {
		if f.Kind == routing.FindingUnreadStream {
			unread = append(unread, f.Stream)
		}
	}
	if !slices.Equal(unread, []string{"homerun"}) {
		t.Errorf("unread streams = %v, want only homerun", unread)
	}

	for _, u := range res.StreamUses() {
		if u.Stream != "tabletennis" {
			continue
		}
		if want := []StreamReader{{Name: "led", ConsumerGroup: "homerun2-led-catcher", OnDemand: true}}; !slices.Equal(u.Catchers, want) {
			t.Errorf("tabletennis catchers = %+v, want %+v", u.Catchers, want)
		}
	}
}

func TestOnDemandStreams_NoPresets(t *testing.T) {
	res := discover(t, deployment("led", "led-catcher", "img/homerun2-led-catcher:1", env("REDIS_STREAMS", "messages,alerts")))

	// Without presets, the simulator offers the configured streams only.
	if led := component(t, res, "led"); led.OnDemandStreams != nil || len(led.Notes) != 0 {
		t.Errorf("led = on demand %v notes %v", led.OnDemandStreams, led.Notes)
	}
}
