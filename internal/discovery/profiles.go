package discovery

import (
	"fmt"
	"path"
	"strings"

	homerun "github.com/stuttgart-things/homerun-library/v4"
	"github.com/stuttgart-things/homerun-library/v4/routing"
	corev1 "k8s.io/api/core/v1"
)

// ProfileStatus says whether a catcher's profile could be read.
type ProfileStatus string

const (
	// ProfileOK: the profile was found and parsed.
	ProfileOK ProfileStatus = "ok"
	// ProfileMissing: the path resolves to a ConfigMap volume, but there is
	// no file there.
	ProfileMissing ProfileStatus = "missing"
	// ProfileInvalid: the file is there and does not parse.
	ProfileInvalid ProfileStatus = "invalid"
	// ProfileUnresolved: where the file comes from is not something the viewer
	// can read - a relative path, a non-ConfigMap volume, a path from a Secret.
	ProfileUnresolved ProfileStatus = "unresolved"
)

// ProfileRef is where a catcher's profile comes from and whether it could be
// read.
type ProfileRef struct {
	Status ProfileStatus `json:"status"`
	// Path is the profile path the catcher opens.
	Path      string `json:"path"`
	ConfigMap string `json:"configMap,omitempty"`
	Key       string `json:"key,omitempty"`
	// Message explains any status but ok, including what the catcher does as
	// a result.
	Message string `json:"message,omitempty"`
}

// missingProfileEffect is what each catcher does when its profile file does
// not exist, read from each catcher's code.
var missingProfileEffect = map[Kind]string{
	KindLightCatcher:        "light-catcher lights nothing: it reads the profile for every message and fails",
	KindLEDCatcher:          "led-catcher runs with an empty profile and displays nothing",
	KindNotificationCatcher: "notification-catcher does not start without its config",
}

// profileError is why a profile file could not be read.
type profileError struct {
	status ProfileStatus
	msg    string
	// podCannotStart: the reason keeps the pod from starting at all, so what
	// the catcher does without a profile does not apply.
	podCannotStart bool
}

func unresolvedf(format string, args ...any) *profileError {
	return &profileError{status: ProfileUnresolved, msg: fmt.Sprintf(format, args...)}
}

func missingf(format string, args ...any) *profileError {
	return &profileError{status: ProfileMissing, msg: fmt.Sprintf(format, args...)}
}

func cannotStartf(format string, args ...any) *profileError {
	return &profileError{status: ProfileMissing, msg: fmt.Sprintf(format, args...), podCannotStart: true}
}

// unavailableProfile stands in for a profile that could not be used, so a dry
// run says why instead of pretending the catcher reacts to everything - which
// is what a nil routing.Profile means.
type unavailableProfile struct {
	summary, problem string
}

func (p unavailableProfile) Evaluate(homerun.Message) []routing.Reaction {
	return []routing.Reaction{{Summary: p.summary, Problem: p.problem}}
}

func (unavailableProfile) Systems() []string { return nil }

// resolveProfiles loads the profile of every catcher that has one.
func (r *Result) resolveProfiles() {
	for i := range r.Components {
		c := &r.Components[i]
		if c.ProfilePath == nil {
			continue
		}
		c.Profile, c.rules = r.loadProfile(c)
	}
}

func (r *Result) loadProfile(c *Component) (*ProfileRef, routing.Profile) {
	ref := &ProfileRef{Path: c.ProfilePath.Value}

	data, perr := r.readFile(c, c.ProfilePath, ref)
	if perr != nil {
		ref.Status = perr.status
		switch {
		case perr.status == ProfileUnresolved:
			ref.Message = perr.msg
			return ref, unavailableProfile{summary: "unknown", problem: "profile not resolved: " + perr.msg}
		case perr.podCannotStart:
			ref.Message = perr.msg
			c.StartProblems = append(c.StartProblems, perr.msg)
			return ref, unavailableProfile{summary: "does not run", problem: perr.msg}
		default:
			ref.Message = perr.msg + "; " + missingProfileEffect[c.Kind]
			if c.Kind == KindLEDCatcher {
				empty, err := routing.ParseLEDProfile(nil)
				if err == nil {
					return ref, empty
				}
			}
			return ref, unavailableProfile{summary: "does nothing", problem: ref.Message}
		}
	}

	profile, err := parseProfile(c.Kind, data)
	if err != nil {
		ref.Status, ref.Message = ProfileInvalid, err.Error()
		return ref, routing.InvalidProfile{Err: err}
	}
	ref.Status = ProfileOK
	return ref, profile
}

func parseProfile(k Kind, data []byte) (routing.Profile, error) {
	switch k {
	case KindLightCatcher:
		p, err := routing.ParseLightProfile(data)
		if err != nil {
			return nil, err
		}
		return p, nil
	case KindLEDCatcher:
		p, err := routing.ParseLEDProfile(data)
		if err != nil {
			return nil, err
		}
		return p, nil
	case KindNotificationCatcher:
		p, err := routing.ParseNotificationConfig(data)
		if err != nil {
			return nil, err
		}
		return p, nil
	default:
		return nil, fmt.Errorf("no profile parser for %s", k)
	}
}

// readFile finds the file a component opens at the path pv holds: path, then
// the volume mount holding it, then the ConfigMap volume and the key projected
// at that path. It records the ConfigMap and key on ref as soon as they are
// known.
func (r *Result) readFile(c *Component, pv *Value, ref *ProfileRef) ([]byte, *profileError) {
	if pv.Unresolved != "" && pv.Source != SourceDefault {
		return nil, unresolvedf("%s %s", pv.Name, pv.Unresolved)
	}

	p := pv.Value
	if !path.IsAbs(p) {
		if c.container.WorkingDir == "" {
			return nil, unresolvedf("%s=%q is relative and the container sets no workingDir: it depends on the image", pv.Name, p)
		}
		p = path.Join(c.container.WorkingDir, p)
	}
	p = path.Clean(p)

	mount := findMount(c.container.VolumeMounts, p)
	if mount == nil {
		return nil, unresolvedf("%s is not on a mounted volume: the file would come from the image", p)
	}
	if mount.SubPathExpr != "" {
		return nil, unresolvedf("volume mount %s uses subPathExpr, which depends on the pod", mount.Name)
	}

	rel := strings.TrimPrefix(strings.TrimPrefix(p, path.Clean(mount.MountPath)), "/")
	if mount.SubPath != "" {
		rel = path.Join(mount.SubPath, rel)
	}
	if rel == "" || rel == "." {
		return nil, unresolvedf("%s is where volume %s is mounted, a directory", p, mount.Name)
	}

	vol := findVolume(c.volumes, mount.Name)
	if vol == nil {
		return nil, unresolvedf("volume %s is mounted but not defined", mount.Name)
	}
	if vol.ConfigMap == nil {
		return nil, unresolvedf("%s is on volume %s of type %s, which the viewer does not read", p, vol.Name, volumeType(vol))
	}
	return r.readConfigMapFile(vol, rel, ref)
}

func (r *Result) readConfigMapFile(vol *corev1.Volume, rel string, ref *ProfileRef) ([]byte, *profileError) {
	src := vol.ConfigMap
	ref.ConfigMap = src.Name
	optional := isOptional(src.Optional)

	cm, exists := r.configMaps[src.Name]
	if !exists {
		if optional {
			return nil, missingf("ConfigMap %s does not exist, so the optional volume %s is empty", src.Name, vol.Name)
		}
		return nil, cannotStartf("ConfigMap %s does not exist: the pod cannot start", src.Name)
	}

	key := rel
	if len(src.Items) > 0 {
		key = ""
		for _, item := range src.Items {
			if path.Clean(item.Path) == rel {
				key = item.Key
				break
			}
		}
		if key == "" {
			return nil, missingf("volume %s projects only its listed items, and none is at %s", vol.Name, rel)
		}
	} else if strings.Contains(rel, "/") {
		return nil, missingf("ConfigMap %s has no subdirectories: nothing is at %s", src.Name, rel)
	}
	ref.Key = key

	if v, ok := cm.Data[key]; ok {
		return []byte(v), nil
	}
	if v, ok := cm.BinaryData[key]; ok {
		return v, nil
	}
	if len(src.Items) > 0 && !optional {
		return nil, cannotStartf("ConfigMap %s has no key %s, which volume %s lists: the pod cannot start", src.Name, key, vol.Name)
	}
	return nil, missingf("ConfigMap %s has no key %s", src.Name, key)
}

// findMount returns the mount with the longest mount path containing p.
func findMount(mounts []corev1.VolumeMount, p string) *corev1.VolumeMount {
	var best *corev1.VolumeMount
	bestLen := -1
	for i := range mounts {
		mp := path.Clean(mounts[i].MountPath)
		if p != mp && mp != "/" && !strings.HasPrefix(p, mp+"/") {
			continue
		}
		if len(mp) > bestLen {
			best, bestLen = &mounts[i], len(mp)
		}
	}
	return best
}

func findVolume(volumes []corev1.Volume, name string) *corev1.Volume {
	for i := range volumes {
		if volumes[i].Name == name {
			return &volumes[i]
		}
	}
	return nil
}

func volumeType(v *corev1.Volume) string {
	switch {
	case v.EmptyDir != nil:
		return "emptyDir"
	case v.Secret != nil: // pragma: allowlist secret
		return "secret"
	case v.Projected != nil:
		return "projected"
	case v.PersistentVolumeClaim != nil:
		return "persistentVolumeClaim"
	case v.HostPath != nil:
		return "hostPath"
	default:
		return "other"
	}
}

// RoutingComponents returns the routed components for homerun-library
// routing, with their parsed profiles. Components that do not run, have no
// known streams or are outside routing are left out; a consumer group the
// viewer could not resolve is passed as unknown.
func (r *Result) RoutingComponents() []routing.Component {
	var out []routing.Component
	for i := range r.Components {
		c := &r.Components[i]
		if !c.Routed() {
			continue
		}
		rc := routing.Component{Name: c.Name, Role: c.Role, Streams: c.Streams, Profile: c.rules}
		if g := c.ConsumerGroup; g != nil && (g.Unresolved == "" || g.Source == SourceDefault) {
			rc.ConsumerGroup = g.Value
		}
		out = append(out, rc)
	}
	return out
}
