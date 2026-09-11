// Package discovery turns the homerun2 Deployments of a namespace into
// components: which service each one runs, which streams it publishes to or
// reads, and with which consumer group. It only lists Deployments and
// ConfigMaps, and never reads Secrets.
package discovery

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/stuttgart-things/homerun-library/v4/routing"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Component is one homerun2 Deployment as the viewer understands it.
type Component struct {
	// Name is the Deployment name.
	Name string `json:"name"`
	// ComponentLabel is the app.kubernetes.io/component label.
	ComponentLabel string `json:"componentLabel"`
	Kind           Kind   `json:"kind"`
	// Role is pitcher or catcher; empty for components outside message
	// routing (scout, wled-mock, unknown ones).
	Role routing.Role `json:"role,omitempty"`
	// Replicas is the desired replica count. Zero means nothing runs.
	Replicas int32 `json:"replicas"`
	// Container is the container the values below were read from.
	Container string `json:"container"`
	Image     string `json:"image"`

	// Streams are the streams the component publishes to or reads. Empty when
	// it uses none or they could not be resolved - Notes says which.
	Streams []string `json:"streams"`
	// StreamValues are the variables Streams was resolved from.
	StreamValues []Value `json:"streamValues,omitempty"`
	// ConsumerGroup is set for catchers.
	ConsumerGroup *Value `json:"consumerGroup,omitempty"`
	// ProfilePath is set for catchers with a profile.
	ProfilePath *Value `json:"profilePath,omitempty"`
	// Mode is the variable selecting how a pitcher publishes
	// (demo-pitcher PITCH_TARGET, git-pitcher PITCHER_MODE).
	Mode *Value `json:"mode,omitempty"`
	// Profile is where the catcher's profile comes from and whether it could
	// be read. Set for catchers with a profile.
	Profile *ProfileRef `json:"profile,omitempty"`

	// StreamsUnresolved: the streams depend on a value the viewer cannot
	// resolve, so they are unknown rather than empty.
	StreamsUnresolved bool `json:"streamsUnresolved,omitempty"`

	// StartProblems are reasons the pod cannot start at all, such as a
	// ConfigMap it requires that does not exist.
	StartProblems []string `json:"startProblems,omitempty"`
	// Notes are things the viewer could not resolve or that change what the
	// component does, in plain words.
	Notes []string `json:"notes,omitempty"`

	container corev1.Container
	volumes   []corev1.Volume
	// rules is the parsed profile handed to routing. Never nil for a catcher
	// with a profile path: nil means "reacts to every message".
	rules routing.Profile
}

// Running reports whether the Deployment wants at least one replica.
func (c *Component) Running() bool { return c.Replicas > 0 }

// Routed reports whether the component takes part in message routing: it is
// a pitcher or catcher, runs - wants replicas and can start - and its streams
// are known.
func (c *Component) Routed() bool {
	return c.Role != "" && c.Running() && len(c.StartProblems) == 0 && len(c.Streams) > 0
}

// Result is what Discover found in a namespace.
type Result struct {
	Namespace     string      `json:"namespace"`
	LabelSelector string      `json:"labelSelector"`
	Components    []Component `json:"components"`

	configMaps map[string]*corev1.ConfigMap
}

// ConfigMap returns the ConfigMap name from the snapshot Discover read.
func (r *Result) ConfigMap(name string) (*corev1.ConfigMap, bool) {
	cm, ok := r.configMaps[name]
	return cm, ok
}

// Discoverer reads homerun2 components from one namespace.
type Discoverer struct {
	Client        kubernetes.Interface
	Namespace     string
	LabelSelector string
}

// Discover lists the selected Deployments and all ConfigMaps of the
// namespace - ConfigMaps are not filtered by label, since those referenced by
// a Deployment are not always labeled - and resolves each component.
func (d *Discoverer) Discover(ctx context.Context) (*Result, error) {
	deployments, err := d.Client.AppsV1().Deployments(d.Namespace).List(ctx, metav1.ListOptions{LabelSelector: d.LabelSelector})
	if err != nil {
		return nil, fmt.Errorf("list deployments in %s: %w", d.Namespace, err)
	}
	cmList, err := d.Client.CoreV1().ConfigMaps(d.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list configmaps in %s: %w", d.Namespace, err)
	}

	res := &Result{
		Namespace:     d.Namespace,
		LabelSelector: d.LabelSelector,
		configMaps:    make(map[string]*corev1.ConfigMap, len(cmList.Items)),
	}
	for i := range cmList.Items {
		res.configMaps[cmList.Items[i].Name] = &cmList.Items[i]
	}
	for i := range deployments.Items {
		res.Components = append(res.Components, resolveComponent(&deployments.Items[i], res.configMaps, d.Namespace))
	}
	slices.SortFunc(res.Components, func(a, b Component) int { return cmp.Compare(a.Name, b.Name) })
	res.resolveProfiles()
	return res, nil
}

func resolveComponent(dep *appsv1.Deployment, configMaps map[string]*corev1.ConfigMap, namespace string) Component {
	c := Component{
		Name:           dep.Name,
		ComponentLabel: dep.Labels[ComponentLabel],
		Replicas:       1,
		volumes:        dep.Spec.Template.Spec.Volumes,
	}
	if dep.Spec.Replicas != nil {
		c.Replicas = *dep.Spec.Replicas
	}
	if !c.Running() {
		c.Notes = append(c.Notes, "scaled to zero: nothing runs, so it neither publishes nor reads")
	}

	container, note := pickContainer(dep)
	if container == nil {
		c.Kind = KindUnknown
		c.Notes = append(c.Notes, "the Deployment has no containers")
		return c
	}
	if note != "" {
		c.Notes = append(c.Notes, note)
	}
	c.container = *container
	c.Container = container.Name
	c.Image = container.Image

	c.Kind = classify(c.ComponentLabel, dep.Name, container.Image)
	info := kinds[c.Kind]
	c.Role = info.role

	env := resolveEnv(container, configMaps, namespace)
	c.StartProblems = append(c.StartProblems, env.problems...)

	switch c.Role {
	case routing.RoleCatcher:
		resolveCatcher(&c, env, info)
	case routing.RolePitcher:
		resolvePitcher(&c, env, info)
	default:
		if c.Kind == KindUnknown {
			c.Notes = append(c.Notes, fmt.Sprintf("component label %q is not a known homerun2 component: not part of routing", c.ComponentLabel))
		}
	}
	return c
}

// pickContainer returns the container named like the Deployment, the only
// container, or the first one with a note saying so.
func pickContainer(dep *appsv1.Deployment) (container *corev1.Container, note string) {
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return nil, ""
	}
	for i := range containers {
		if containers[i].Name == dep.Name {
			return &containers[i], ""
		}
	}
	if len(containers) == 1 {
		return &containers[0], ""
	}
	return &containers[0], fmt.Sprintf("%d containers and none named %s: read container %s", len(containers), dep.Name, containers[0].Name)
}

func resolveCatcher(c *Component, env environment, info kindInfo) {
	streamsEnv := env.lookup("REDIS_STREAMS", "")
	streamEnv := env.lookup("REDIS_STREAM", "")
	c.StreamValues = []Value{streamsEnv, streamEnv}
	setStreams(c, routing.ParseStreams(streamsEnv.Value, streamEnv.Value, info.defaultStream), streamsEnv, streamEnv)

	group := env.lookup("CONSUMER_GROUP", info.defaultGroup)
	c.ConsumerGroup = &group
	noteUnresolved(c, group)

	if info.profileEnv != "" {
		p := env.lookup(info.profileEnv, info.profileDefault)
		c.ProfilePath = &p
		noteUnresolved(c, p)
	}
}

func resolvePitcher(c *Component, env environment, info kindInfo) {
	switch c.Kind {
	case KindK8sPitcher:
		c.Notes = append(c.Notes, "k8s-pitcher takes its stream or HTTP target from its profile, which is not resolved yet (#10)")
		return
	case KindPitcher:
		c.Notes = append(c.Notes, "component \"pitcher\" but neither git-pitcher nor demo-pitcher: its default stream is unknown")
	case KindDemoPitcher:
		if !demoPitcherUsesRedis(c, env) {
			return
		}
	case KindGitPitcher:
		if !gitPitcherUsesRedis(c, env) {
			return
		}
	case KindOmniPitcher:
		if routes := env.lookup("ROUTES_CONFIG", ""); routes.Value != "" {
			c.Notes = append(c.Notes, fmt.Sprintf(
				"ROUTES_CONFIG=%s can route messages to other streams, which is not resolved yet (#10)", routes.Value))
		}
	}

	stream := env.lookup("REDIS_STREAM", info.defaultStream)
	c.StreamValues = []Value{stream}
	var streams []string
	if stream.Value != "" {
		streams = []string{stream.Value}
	}
	setStreams(c, streams, stream)
}

// Publishing modes of demo-pitcher's PITCH_TARGET and git-pitcher's
// PITCHER_MODE.
const (
	modeRedis       = "redis"
	modeFile        = "file"
	modeBoth        = "both"
	modeOmniPitcher = "omni-pitcher"
)

// demoPitcherUsesRedis applies demo-pitcher's PITCH_TARGET switch: redis
// (default) and both publish to REDIS_STREAM, omni-pitcher pitches over
// HTTP, file writes a file, and anything else falls back to redis.
func demoPitcherUsesRedis(c *Component, env environment) bool {
	target := env.lookup("PITCH_TARGET", modeRedis)
	c.Mode = &target
	noteUnresolved(c, target)

	switch target.Value {
	case modeRedis:
		return true
	case modeBoth:
		c.Notes = append(c.Notes, "PITCH_TARGET=both also pitches over HTTP to omni-pitcher, which is not resolved yet (#10)")
		return true
	case modeOmniPitcher:
		c.Notes = append(c.Notes, "PITCH_TARGET=omni-pitcher pitches over HTTP to omni-pitcher, not to a stream; not resolved yet (#10)")
		return false
	case modeFile:
		c.Notes = append(c.Notes, "PITCH_TARGET=file writes to a file: publishes to no stream")
		return false
	default:
		c.Notes = append(c.Notes, fmt.Sprintf(
			"PITCH_TARGET=%q is not one of redis, file, omni-pitcher, both: demo-pitcher silently falls back to redis", target.Value))
		return true
	}
}

// gitPitcherUsesRedis applies git-pitcher's PITCHER_MODE switch: file writes
// a file, anything else publishes to Redis.
func gitPitcherUsesRedis(c *Component, env environment) bool {
	mode := env.lookup("PITCHER_MODE", modeRedis)
	c.Mode = &mode
	noteUnresolved(c, mode)

	switch mode.Value {
	case modeFile:
		c.Notes = append(c.Notes, "PITCHER_MODE=file writes to a file: publishes to no stream")
		return false
	case modeRedis:
		return true
	default:
		c.Notes = append(c.Notes, fmt.Sprintf("PITCHER_MODE=%q is not file: git-pitcher uses redis", mode.Value))
		return true
	}
}

// setStreams sets the streams unless one of the values they depend on is
// unresolved: a stream taken from a Secret is unknown, not the default.
func setStreams(c *Component, streams []string, from ...Value) {
	for _, v := range from {
		if v.Unresolved != "" && v.Source != SourceDefault {
			c.StreamsUnresolved = true
			c.Notes = append(c.Notes, fmt.Sprintf("streams unknown: %s %s", v.Name, v.Unresolved))
			return
		}
		noteUnresolved(c, v)
	}
	c.Streams = streams
}

func noteUnresolved(c *Component, v Value) {
	if v.Unresolved == "" {
		return
	}
	note := fmt.Sprintf("%s: %s", v.Name, v.Unresolved)
	if !slices.Contains(c.Notes, note) {
		c.Notes = append(c.Notes, note)
	}
}

// Streams returns every stream a routed component publishes to or reads,
// sorted.
func (r *Result) Streams() []string {
	var out []string
	for i := range r.Components {
		if !r.Components[i].Routed() {
			continue
		}
		for _, s := range r.Components[i].Streams {
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	slices.SortFunc(out, strings.Compare)
	return out
}
