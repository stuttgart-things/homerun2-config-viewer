package discovery

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// Value is one environment variable as the container would see it, with where
// the value came from.
type Value struct {
	Name string `json:"name"`
	// Value is the resolved value. With Source "default" it is the service's
	// built-in default, because the variable is not set.
	Value string `json:"value"`
	// Source says where Value came from: "env", "envFrom ConfigMap <name>",
	// "ConfigMap <name> key <key>", "fieldRef <path>" or "default".
	Source string `json:"source"`
	// Unresolved, when set, says why Value may not be what the container
	// sees. The viewer shows it rather than guessing.
	Unresolved string `json:"unresolved,omitempty"`
}

// Value sources that are not a reference to an object.
const (
	// SourceDefault marks a Value that is the service's own default.
	SourceDefault = "default"
	// SourceEnv marks a literal value in the container's env.
	SourceEnv = "env"
)

// environment is a container's environment resolved the way the kubelet
// builds it: envFrom in order, then env in order, later definitions winning.
type environment struct {
	vars map[string]Value
	// secretEnvFrom lists Secrets imported with envFrom. Their keys are not
	// known without reading them, so any variable may come from one.
	secretEnvFrom []string
	// problems are container-level findings, such as a missing ConfigMap that
	// keeps the pod from starting.
	problems []string
}

// resolveEnv resolves container's environment. configMaps holds the
// namespace's ConfigMaps by name; namespace resolves fieldRef
// metadata.namespace.
func resolveEnv(container *corev1.Container, configMaps map[string]*corev1.ConfigMap, namespace string) environment {
	env := environment{vars: map[string]Value{}}

	for _, from := range container.EnvFrom {
		switch {
		case from.ConfigMapRef != nil:
			ref := from.ConfigMapRef
			cm, ok := configMaps[ref.Name]
			if !ok {
				if !isOptional(ref.Optional) {
					env.problems = append(env.problems,
						fmt.Sprintf("envFrom ConfigMap %s does not exist: the pod cannot start", ref.Name))
				}
				continue
			}
			for key, val := range cm.Data {
				name := from.Prefix + key
				env.vars[name] = Value{Name: name, Value: val, Source: "envFrom ConfigMap " + ref.Name}
			}
		case from.SecretRef != nil: // pragma: allowlist secret
			env.secretEnvFrom = append(env.secretEnvFrom, from.SecretRef.Name)
		}
	}

	for i := range container.Env {
		v := resolveVar(&container.Env[i], configMaps, namespace)
		if v == nil {
			continue // an optional reference to something missing: not set
		}
		env.vars[v.Name] = *v
	}

	return env
}

func resolveVar(e *corev1.EnvVar, configMaps map[string]*corev1.ConfigMap, namespace string) *Value {
	v := &Value{Name: e.Name}

	if e.ValueFrom == nil {
		v.Value, v.Source = e.Value, SourceEnv
		if strings.Contains(e.Value, "$(") {
			v.Unresolved = "uses $(VAR) expansion, which is not evaluated here"
		}
		return v
	}

	from := e.ValueFrom
	switch {
	case from.ConfigMapKeyRef != nil:
		ref := from.ConfigMapKeyRef
		v.Source = fmt.Sprintf("ConfigMap %s key %s", ref.Name, ref.Key)
		cm, ok := configMaps[ref.Name]
		if !ok {
			if isOptional(ref.Optional) {
				return nil
			}
			v.Unresolved = fmt.Sprintf("ConfigMap %s does not exist: the pod cannot start", ref.Name)
			return v
		}
		val, ok := cm.Data[ref.Key]
		if !ok {
			if isOptional(ref.Optional) {
				return nil
			}
			v.Unresolved = fmt.Sprintf("ConfigMap %s has no key %s: the pod cannot start", ref.Name, ref.Key)
			return v
		}
		v.Value = val
	case from.SecretKeyRef != nil: // pragma: allowlist secret
		v.Source = fmt.Sprintf("Secret %s key %s", from.SecretKeyRef.Name, from.SecretKeyRef.Key)
		v.Unresolved = "comes from a Secret, which the viewer does not read"
	case from.FieldRef != nil && from.FieldRef.FieldPath == "metadata.namespace":
		v.Value, v.Source = namespace, "fieldRef metadata.namespace"
	case from.FieldRef != nil:
		v.Source = "fieldRef " + from.FieldRef.FieldPath
		v.Unresolved = "depends on the pod, not the Deployment"
	default:
		v.Source = "resourceFieldRef"
		v.Unresolved = "depends on the pod, not the Deployment"
	}
	return v
}

// lookup returns the variable name, or the service default when it is unset.
// An unset variable that a Secret imported with envFrom could still define is
// flagged: the default is then only the likely value.
func (env environment) lookup(name, def string) Value {
	if v, ok := env.vars[name]; ok {
		return v
	}
	v := Value{Name: name, Value: def, Source: SourceDefault}
	if len(env.secretEnvFrom) > 0 {
		v.Unresolved = fmt.Sprintf("not set directly; envFrom Secret %s may set it", strings.Join(env.secretEnvFrom, ", "))
	}
	return v
}

func isOptional(optional *bool) bool {
	return optional != nil && *optional
}
