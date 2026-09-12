package discovery

import (
	"path"
	"strings"

	"github.com/stuttgart-things/homerun-library/v4/routing"
)

// Kind identifies which homerun2 service a Deployment runs.
type Kind string

// The homerun2 services the viewer knows.
const (
	KindOmniPitcher         Kind = "omni-pitcher"
	KindGitPitcher          Kind = "git-pitcher"
	KindDemoPitcher         Kind = "demo-pitcher"
	KindPitcher             Kind = "pitcher" // component "pitcher" that is neither git- nor demo-pitcher
	KindK8sPitcher          Kind = "k8s-pitcher"
	KindCoreCatcher         Kind = "core-catcher"
	KindLightCatcher        Kind = "light-catcher"
	KindLEDCatcher          Kind = "led-catcher"
	KindNotificationCatcher Kind = "notification-catcher"
	KindScout               Kind = "scout"
	KindWLEDMock            Kind = "wled-mock"
	KindConfigViewer        Kind = "config-viewer"
	KindUnknown             Kind = "unknown"
)

// ComponentLabel is the label every homerun2 KCL deployment sets to say what
// the component does.
const ComponentLabel = "app.kubernetes.io/component"

// Values of the component label that differ from the kind name.
const (
	// componentLabelPitcher is shared by git-pitcher and demo-pitcher.
	componentLabelPitcher  = "pitcher"
	componentLabelNotifier = "notifier"
)

// Service defaults that more than one service shares.
const (
	defaultStreamMessages = "messages"
	envProfilePath        = "PROFILE_PATH"
	defaultProfileFile    = "profile.yaml"
	// envConfigPath and defaultNotificationConfig: notification-catcher's
	// config path variable and its default.
	envConfigPath             = "CONFIG_PATH"
	defaultNotificationConfig = "/etc/notification-catcher/config.yaml"
)

// defaultGroup is the consumer group a catcher uses when CONSUMER_GROUP is
// unset: homerun2-<service>.
func defaultGroup(k Kind) string { return "homerun2-" + string(k) }

// kindInfo is what the viewer knows about a service: how it is labeled, and
// the defaults its code falls back to when an environment variable is unset.
// Every default here was read from the service's code on main; a service that
// changes one must change it here too, or the viewer shows a stream the
// service no longer uses.
type kindInfo struct {
	role routing.Role // empty: not part of message routing
	// multiStream: reads REDIS_STREAMS before REDIS_STREAM.
	multiStream   bool
	defaultStream string
	defaultGroup  string
	// profileEnv names the variable holding the profile path, profileDefault
	// the path used when it is unset.
	profileEnv     string
	profileDefault string
}

var kinds = map[Kind]kindInfo{
	KindOmniPitcher: {role: routing.RolePitcher, defaultStream: defaultStreamMessages},
	KindGitPitcher:  {role: routing.RolePitcher, defaultStream: defaultStreamMessages},
	KindDemoPitcher: {role: routing.RolePitcher, defaultStream: "homerun"},
	KindPitcher:     {role: routing.RolePitcher},
	KindK8sPitcher:  {role: routing.RolePitcher},
	KindCoreCatcher: {
		role: routing.RoleCatcher, multiStream: true,
		defaultStream: defaultStreamMessages, defaultGroup: defaultGroup(KindCoreCatcher),
	},
	KindLightCatcher: {
		role: routing.RoleCatcher, multiStream: true,
		defaultStream: defaultStreamMessages, defaultGroup: defaultGroup(KindLightCatcher),
		profileEnv: envProfilePath, profileDefault: defaultProfileFile,
	},
	KindLEDCatcher: {
		role: routing.RoleCatcher, multiStream: true,
		defaultStream: defaultStreamMessages, defaultGroup: defaultGroup(KindLEDCatcher),
		profileEnv: envProfilePath, profileDefault: defaultProfileFile,
	},
	KindNotificationCatcher: {
		role: routing.RoleCatcher, multiStream: true,
		defaultStream: "alerts", defaultGroup: defaultGroup(KindNotificationCatcher),
		profileEnv: envConfigPath, profileDefault: defaultNotificationConfig,
	},
	KindScout:        {},
	KindWLEDMock:     {},
	KindConfigViewer: {},
	KindUnknown:      {},
}

// componentKinds maps the component label to a kind. "pitcher" is shared by
// git-pitcher and demo-pitcher and resolved by classify.
var componentKinds = map[string]Kind{
	"api":                    KindOmniPitcher,
	"watcher":                KindK8sPitcher,
	"consumer":               KindCoreCatcher,
	string(KindLightCatcher): KindLightCatcher,
	string(KindLEDCatcher):   KindLEDCatcher,
	componentLabelNotifier:   KindNotificationCatcher,
	"analytics":              KindScout,
	"wled-mock":              KindWLEDMock,
	// The viewer lists itself: it is part-of homerun2 like every component.
	string(KindConfigViewer): KindConfigViewer,
}

// classify determines the kind from the component label. For the shared
// "pitcher" label it looks at the image repository first - it names the
// service whatever the Deployment is called - then at the Deployment name.
func classify(componentLabel, deploymentName, image string) Kind {
	if componentLabel != componentLabelPitcher {
		if k, ok := componentKinds[componentLabel]; ok {
			return k
		}
		return KindUnknown
	}
	for _, candidate := range []string{imageName(image), deploymentName} {
		switch {
		case strings.Contains(candidate, "demo-pitcher"):
			return KindDemoPitcher
		case strings.Contains(candidate, "git-pitcher"):
			return KindGitPitcher
		}
	}
	return KindPitcher
}

// imageName returns the last path element of an image reference without tag
// or digest: ghcr.io/org/homerun2-demo-pitcher:v2 -> homerun2-demo-pitcher.
func imageName(image string) string {
	image, _, _ = strings.Cut(image, "@")
	name := path.Base(image)
	name, _, _ = strings.Cut(name, ":")
	return name
}
