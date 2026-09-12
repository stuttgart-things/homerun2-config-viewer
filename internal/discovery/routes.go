package discovery

import (
	"fmt"
	"strings"

	"github.com/stuttgart-things/homerun-library/v4/routing"
)

// RoutesRef is where omni-pitcher's routing file (ROUTES_CONFIG) comes from,
// whether it loads, and the routes it holds.
type RoutesRef struct {
	ProfileRef
	Routes *routing.StreamRoutes `json:"routes,omitempty"`
}

// routesStartEffect is what omni-pitcher does when its routing file does not
// load, read from its main.go.
const routesStartEffect = "omni-pitcher exits at startup"

// noRuleMatches describes a message routed to the default stream.
const noRuleMatches = "no rule matches"

// When returns which messages omni-pitcher sends to stream, one entry per
// rule choosing it in rule order, then "no rule matches" when stream is the
// default stream. It is empty when the routes do not name stream.
func (ref *RoutesRef) When(stream string) []string { return ref.WhenOn(stream, "") }

// WhenOn is When for pitches arriving on endpoint: rules whose endpoint
// matcher it cannot satisfy are left out. An empty endpoint leaves none out.
func (ref *RoutesRef) WhenOn(stream, endpoint string) []string {
	if ref == nil || ref.Routes == nil {
		return nil
	}
	var out []string
	for i, route := range ref.Routes.Routes {
		if endpoint != "" && route.Match.Endpoint != "" && !strings.Contains(endpoint, route.Match.Endpoint) {
			continue
		}
		if route.Stream == stream {
			out = append(out, fmt.Sprintf("rule %d: %s", i+1, route.Match.Summary()))
		}
	}
	if ref.Routes.DefaultStream == stream {
		out = append(out, noRuleMatches)
	}
	return out
}

// resolveRoutes loads the routing file of every omni-pitcher that sets
// ROUTES_CONFIG and takes its streams from it.
func (r *Result) resolveRoutes() {
	for i := range r.Components {
		if c := &r.Components[i]; c.RoutesPath != nil {
			r.loadRoutes(c)
		}
	}
}

// loadRoutes sets c's streams to those its routes can reach. A file the
// viewer cannot read leaves them unknown; a missing or invalid one is a pod
// that cannot start, because omni-pitcher exits when the file does not load.
func (r *Result) loadRoutes(c *Component) {
	ref := &RoutesRef{ProfileRef: ProfileRef{Path: c.RoutesPath.Value}}
	c.Routes = ref
	c.StreamValues = []Value{*c.RoutesPath}

	data, ferr := r.readFile(c, c.RoutesPath, &ref.ProfileRef)
	if ferr != nil {
		ref.Status, ref.Message = ferr.status, ferr.msg
		switch {
		case ferr.status == ProfileUnresolved:
			c.StreamsUnresolved = true
			c.Notes = append(c.Notes, "streams unknown: the routing file cannot be read: "+ferr.msg)
		case ferr.podCannotStart:
			c.StartProblems = append(c.StartProblems, ferr.msg)
		default:
			c.StartProblems = append(c.StartProblems, fmt.Sprintf("routing file %s: %s: %s", ref.Path, ferr.msg, routesStartEffect))
		}
		return
	}

	routes, err := routing.ParseStreamRoutes(data)
	if err != nil {
		ref.Status, ref.Message = ProfileInvalid, err.Error()
		c.StartProblems = append(c.StartProblems, fmt.Sprintf("routing file %s: %s: %s", ref.Path, err, routesStartEffect))
		return
	}
	ref.Status, ref.Routes = ProfileOK, routes
	c.Streams = routes.Targets()
}
