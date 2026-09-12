package discovery

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/stuttgart-things/homerun-library/v4/routing"
)

// PitchTarget is where a pitcher POSTs its messages over HTTP instead of, or
// besides, writing to a stream itself.
type PitchTarget struct {
	// URL is the URL the pitcher posts to.
	URL string `json:"url"`
	// From are the values URL was built from.
	From []Value `json:"from"`
	// OmniPitcher is the omni-pitcher Deployment URL reaches. Empty when it
	// reaches none in this namespace.
	OmniPitcher string `json:"omniPitcher,omitempty"`
	// Path is the path of URL: the endpoint omni-pitcher answers, and what its
	// routes match against.
	Path string `json:"path,omitempty"`
	// Streams are the streams the pitches end up on through OmniPitcher.
	Streams []string `json:"streams,omitempty"`
	// Problem says why the pitches do not arrive, or cannot be followed.
	Problem string `json:"problem,omitempty"`

	// problemKind is the finding Problem is reported as. Empty when another
	// component's finding already covers it.
	problemKind routing.FindingKind
}

func (t *PitchTarget) failf(kind routing.FindingKind, format string, args ...any) {
	t.problemKind, t.Problem = kind, fmt.Sprintf(format, args...)
}

// Defaults of demo-pitcher's HTTP pitcher, read from its main.go.
const (
	defaultOmniPitcherURL     = "http://localhost:4000"
	defaultOmniPitcherAPIPath = "generic"
)

// demoPitchTargets are the PITCH_TARGET values demo-pitcher knows.
var demoPitchTargets = []string{modeRedis, modeFile, modeOmniPitcher, modeBoth}

// demoPitchTarget is where demo-pitcher's HTTP pitcher posts: OMNI_PITCHER_URL
// and OMNI_PITCHER_API_PATH joined with a slash, as its HTTPPitcher does.
func demoPitchTarget(env environment) *PitchTarget {
	base := env.lookup("OMNI_PITCHER_URL", defaultOmniPitcherURL)
	apiPath := env.lookup("OMNI_PITCHER_API_PATH", defaultOmniPitcherAPIPath)
	return &PitchTarget{URL: base.Value + "/" + apiPath.Value, From: []Value{base, apiPath}}
}

// resolvePitchTargets follows every pitcher that pitches over HTTP to the
// omni-pitcher it reaches. It runs after resolveRoutes, since the streams a
// pitch ends up on are that omni-pitcher's.
func (r *Result) resolvePitchTargets() {
	for i := range r.Components {
		if t := r.Components[i].PitchTarget; t != nil {
			r.resolvePitchTarget(t)
		}
	}
}

func (r *Result) resolvePitchTarget(t *PitchTarget) {
	for _, v := range t.From {
		if v.Unresolved != "" && v.Source != SourceDefault {
			t.failf(FindingPitchTargetUnresolved, "the URL cannot be resolved: %s %s", v.Name, v.Unresolved)
			return
		}
	}

	u, err := url.Parse(t.URL)
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		t.failf(FindingPitchTargetUnresolved, "%q is not an http(s) URL: nothing receives the pitches", t.URL)
		return
	}
	t.Path = u.Path
	if t.Path == "" {
		t.Path = "/"
	}

	omni, why := r.omniPitcherAt(u.Hostname())
	if omni == nil {
		t.failf(FindingPitchTargetUnresolved, "%s", why)
		return
	}
	t.OmniPitcher = omni.Name

	switch t.Path {
	case routing.PitchPath:
	case routing.PitchPathGrafana, routing.PitchPathGitHub:
		t.failf(FindingPitchPathUnknown,
			"%s is omni-pitcher's webhook endpoint: it builds messages from a webhook payload, not from the message the pitcher sends; use %s",
			t.Path, routing.PitchPath)
		return
	default:
		t.failf(FindingPitchPathUnknown, "omni-pitcher serves %s, not %s: the pitches are answered 404",
			strings.Join(routing.PitchPaths, ", "), t.Path)
		return
	}

	switch {
	case !omni.Running():
		t.Problem = omni.Name + " is scaled to zero, so the pitches fail"
	case len(omni.StartProblems) > 0:
		t.Problem = omni.Name + " cannot start, so the pitches fail"
	default:
		t.Streams = viaStreams(omni, t.Path)
	}
}

// omniPitcherAt returns the omni-pitcher of the namespace that host names. It
// matches the Service, which every homerun2 KCL base names like its
// Deployment, as <name>, <name>.<namespace> or <name>.<namespace>.svc with any
// cluster domain. why says what host is when it names none.
func (r *Result) omniPitcherAt(host string) (omni *Component, why string) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if ip := net.ParseIP(host); host == "localhost" || ip != nil && ip.IsLoopback() {
		return nil, host + " is the pitcher's own pod, where no omni-pitcher listens: the pitches fail"
	}

	labels := strings.Split(host, ".")
	inNamespace := len(labels) == 1 || labels[1] == r.Namespace && (len(labels) == 2 || labels[2] == "svc")
	if inNamespace {
		for i := range r.Components {
			if c := &r.Components[i]; c.Kind == KindOmniPitcher && c.Name == labels[0] {
				return c, ""
			}
		}
	}
	return nil, fmt.Sprintf("%s is not an omni-pitcher Service in namespace %s: the viewer cannot follow the pitches", host, r.Namespace)
}

// viaStreams returns the streams omni reaches for pitches arriving on path:
// its streams when it has no routes, otherwise the default stream and the
// stream of every rule whose endpoint matcher path can satisfy.
func viaStreams(omni *Component, path string) []string {
	if omni.Routes == nil || omni.Routes.Routes == nil {
		return omni.Streams
	}
	routes := omni.Routes.Routes
	out := []string{routes.DefaultStream}
	for _, route := range routes.Routes {
		if route.Match.Endpoint != "" && !strings.Contains(path, route.Match.Endpoint) {
			continue
		}
		if !slices.Contains(out, route.Stream) {
			out = append(out, route.Stream)
		}
	}
	return out
}
