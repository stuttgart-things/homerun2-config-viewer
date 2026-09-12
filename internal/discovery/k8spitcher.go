package discovery

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
	corev1 "k8s.io/api/core/v1"
)

// k8sPitcherStartEffect is what k8s-pitcher does when its profile does not
// load, read from its main.go.
const k8sPitcherStartEffect = "k8s-pitcher exits at startup"

// profileFlag is k8s-pitcher's flag naming its profile.
const profileFlag = "-profile"

// k8sPitcherProfile is the part of k8s-pitcher's K8sPitcherProfile the viewer
// reads, with the validation k8s-pitcher applies when it loads the file.
// Mirrors homerun2-k8s-pitcher internal/profile.
type k8sPitcherProfile struct {
	Spec struct {
		Redis struct {
			Addr   string `yaml:"addr"`
			Stream string `yaml:"stream"`
		} `yaml:"redis"`
		Pitcher struct {
			Addr string `yaml:"addr"`
		} `yaml:"pitcher"`
		Collectors []struct {
			Kind     string        `yaml:"kind"`
			Interval time.Duration `yaml:"interval"`
		} `yaml:"collectors"`
		Informers []struct {
			Version  string   `yaml:"version"`
			Resource string   `yaml:"resource"`
			Events   []string `yaml:"events"`
		} `yaml:"informers"`
	} `yaml:"spec"`
}

func (p *k8sPitcherProfile) validate() error {
	hasRedis, hasPitcher := p.Spec.Redis.Addr != "", p.Spec.Pitcher.Addr != ""
	switch {
	case !hasRedis && !hasPitcher:
		return errors.New("either spec.redis.addr or spec.pitcher.addr is required")
	case hasRedis && hasPitcher:
		return errors.New("spec.redis and spec.pitcher are mutually exclusive")
	case hasRedis && p.Spec.Redis.Stream == "":
		return errors.New("spec.redis.stream is required")
	case len(p.Spec.Collectors) == 0 && len(p.Spec.Informers) == 0:
		return errors.New("at least one collector or informer must be defined")
	}
	return p.validateWatches()
}

// validateWatches checks the informers, then the collectors.
func (p *k8sPitcherProfile) validateWatches() error {
	for i, inf := range p.Spec.Informers {
		switch {
		case inf.Version == "":
			return fmt.Errorf("spec.informers[%d].version is required", i)
		case inf.Resource == "":
			return fmt.Errorf("spec.informers[%d].resource is required", i)
		case len(inf.Events) == 0:
			return fmt.Errorf("spec.informers[%d].events must not be empty", i)
		}
	}
	for i, col := range p.Spec.Collectors {
		switch {
		case col.Kind == "":
			return fmt.Errorf("spec.collectors[%d].kind is required", i)
		case col.Interval <= 0:
			return fmt.Errorf("spec.collectors[%d].interval must be positive", i)
		}
	}
	return nil
}

// profileArg returns k8s-pitcher's -profile flag the way Go's flag package
// reads it from the container: -profile or --profile, with =value or the next
// argument as its value, up to the first non-flag argument or "--". The
// arguments are the command after the program name, then args; without a
// command, args alone.
func profileArg(c *corev1.Container) (value string, found bool) {
	var argv []string
	if len(c.Command) > 0 {
		argv = append(argv, c.Command[1:]...)
	}
	argv = append(argv, c.Args...)

	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" || !strings.HasPrefix(a, "-") || a == "-" {
			return "", false
		}
		name, v, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-"), "=")
		if name != "profile" {
			if !hasValue && name == "kubeconfig" {
				i++ // the other string flag k8s-pitcher defines takes the next argument
			}
			continue
		}
		if hasValue {
			return v, true
		}
		if i+1 < len(argv) {
			return argv[i+1], true
		}
		return "", true
	}
	return "", false
}

// resolveK8sPitcherArgs finds k8s-pitcher's profile path, or records that the
// pod cannot start without one.
func resolveK8sPitcherArgs(c *Component, env environment) {
	mode := env.lookup("PITCHER_MODE", "")
	c.Mode = &mode
	noteUnresolved(c, mode)

	path, found := profileArg(&c.container)
	switch {
	case path == "":
		problem := "k8s-pitcher runs without " + profileFlag
		if found {
			problem = "k8s-pitcher's " + profileFlag + " has no value"
		}
		c.StartProblems = append(c.StartProblems, problem+": "+k8sPitcherStartEffect)
	case strings.Contains(path, "$("):
		c.ProfilePath = &Value{Name: profileFlag, Value: path, Source: "args", Unresolved: "uses $(VAR) expansion, which is not evaluated here"}
	default:
		c.ProfilePath = &Value{Name: profileFlag, Value: path, Source: "args"}
	}
}

// resolveK8sPitchers loads the profile of every k8s-pitcher and takes its
// stream or HTTP target from it.
func (r *Result) resolveK8sPitchers() {
	for i := range r.Components {
		if c := &r.Components[i]; c.Kind == KindK8sPitcher && c.ProfilePath != nil {
			r.loadK8sPitcherProfile(c)
		}
	}
}

func (r *Result) loadK8sPitcherProfile(c *Component) {
	ref := &ProfileRef{Path: c.ProfilePath.Value}
	c.Profile = ref

	data, ferr := r.readFile(c, c.ProfilePath, ref)
	if ferr != nil {
		ref.Status, ref.Message = ferr.status, ferr.msg
		switch {
		case ferr.status == ProfileUnresolved:
			c.StreamsUnresolved = true
			c.Notes = append(c.Notes, "streams unknown: its profile cannot be read: "+ferr.msg)
		case ferr.podCannotStart:
			c.StartProblems = append(c.StartProblems, ferr.msg)
		default:
			ref.Message = fmt.Sprintf("profile %s: %s: %s", ref.Path, ferr.msg, k8sPitcherStartEffect)
			c.StartProblems = append(c.StartProblems, ref.Message)
		}
		return
	}

	var p k8sPitcherProfile
	err := yaml.Unmarshal(data, &p)
	if err == nil {
		err = p.validate()
	}
	if err != nil {
		ref.Status, ref.Message = ProfileInvalid, fmt.Sprintf("profile %s: %s: %s", ref.Path, err, k8sPitcherStartEffect)
		c.StartProblems = append(c.StartProblems, ref.Message)
		return
	}
	ref.Status = ProfileOK

	source := fmt.Sprintf("profile ConfigMap %s key %s", ref.ConfigMap, ref.Key)
	switch {
	case c.Mode.Value == modeFile:
		c.Notes = append(c.Notes, "PITCHER_MODE=file writes to a file: publishes to no stream")
	case p.Spec.Pitcher.Addr != "":
		addr := Value{Name: "spec.pitcher.addr", Value: p.Spec.Pitcher.Addr, Source: source}
		c.PitchTarget = &PitchTarget{URL: addr.Value, From: []Value{addr}}
	default:
		stream := Value{Name: "spec.redis.stream", Value: p.Spec.Redis.Stream, Source: source}
		c.StreamValues = []Value{stream}
		c.Streams = []string{stream.Value}
	}
}
