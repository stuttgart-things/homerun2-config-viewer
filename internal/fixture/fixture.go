// Package fixture builds discovery results from fake clusters, for tests of
// the packages that present them. It is imported by tests only.
package fixture

import (
	"context"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/stuttgart-things/homerun2-config-viewer/internal/discovery"
)

// Namespace is the namespace every fixture object lives in.
const Namespace = "homerun2"

// LabelSelector selects the fixture Deployments.
const LabelSelector = "app.kubernetes.io/part-of=homerun2"

const (
	envRedisStream = "REDIS_STREAM"
	streamMessages = "messages"
)

// LightProfile is a light-catcher profile reacting to error and critical from
// any system.
const LightProfile = `effects:
  error:
    systems: ["*"]
    severity: [error, critical]
    fx: Blurz
    color: sunset
    endpoint: http://wled
`

// Deployment builds a homerun2 Deployment with one container named after it.
// env is sorted by name so the result is deterministic.
func Deployment(name, component, image string, replicas int32, env map[string]string, spec ...func(*corev1.PodSpec)) *appsv1.Deployment {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	c := corev1.Container{Name: name, Image: image}
	for _, k := range keys {
		c.Env = append(c.Env, corev1.EnvVar{Name: k, Value: env[k]})
	}

	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace, Labels: map[string]string{
			"app.kubernetes.io/part-of": "homerun2",
			discovery.ComponentLabel:    component,
		}},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{c}}},
		},
	}
	for _, s := range spec {
		s(&d.Spec.Template.Spec)
	}
	return d
}

// ProfileVolume mounts ConfigMap configMap at /config, as the light- and
// led-catcher KCL does.
func ProfileVolume(configMap string, optional bool) func(*corev1.PodSpec) {
	return func(s *corev1.PodSpec) {
		s.Containers[0].VolumeMounts = append(s.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "profile", MountPath: "/config"})
		s.Volumes = append(s.Volumes, corev1.Volume{Name: "profile", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: configMap},
			Optional:             &optional,
		}}})
	}
}

// ConfigMap builds a ConfigMap in Namespace.
func ConfigMap(name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace}, Data: data}
}

// Discover runs discovery against a fake cluster holding objs.
func Discover(objs ...runtime.Object) (*discovery.Result, error) {
	d := &discovery.Discoverer{Client: fake.NewClientset(objs...), Namespace: Namespace, LabelSelector: LabelSelector}
	return d.Discover(context.Background())
}

// Mixup is the homerun2-test1 mix-up from homerun-library#122: demo-pitcher
// publishes to "homerun" while every catcher reads "messages". It also has a
// catcher scaled to zero.
//
//	omni     pitcher  messages
//	demo     pitcher  homerun   (nobody reads it)
//	core     catcher  messages  reacts to everything
//	light    catcher  messages  LightProfile
//	stopped  catcher  -         replicas 0
func Mixup() (*discovery.Result, error) {
	return Discover(
		Deployment("omni", "api", "img/homerun2-omni-pitcher:1", 1, map[string]string{envRedisStream: streamMessages}),
		Deployment("demo", "pitcher", "img/homerun2-demo-pitcher:1", 1, map[string]string{"PITCH_TARGET": "redis", envRedisStream: "homerun"}),
		Deployment("core", "consumer", "img/homerun2-core-catcher:1", 1, map[string]string{envRedisStream: streamMessages}),
		Deployment("light", "light-catcher", "img/homerun2-light-catcher:1", 1,
			map[string]string{envRedisStream: streamMessages, "PROFILE_PATH": "/config/profile.yaml"}, ProfileVolume("light-profile", false)),
		ConfigMap("light-profile", map[string]string{"profile.yaml": LightProfile}),
		Deployment("stopped", "consumer", "img/homerun2-core-catcher:1", 0, nil),
	)
}
