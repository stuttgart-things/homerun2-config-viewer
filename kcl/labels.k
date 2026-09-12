"""
Common labels for homerun2-config-viewer resources
"""

import schema

# Get command-line options
_image = option("config.image")
_namespace = option("config.namespace")
_replicas = option("config.replicas")
_labelSelector = option("config.labelSelector")
_cacheTtl = option("config.cacheTtl")
_mustReactSeverities = option("config.mustReactSeverities")
_logLevel = option("config.logLevel")
_httpRouteEnabled = option("config.httpRouteEnabled")
_httpRouteParentRefName = option("config.httpRouteParentRefName")
_httpRouteParentRefNamespace = option("config.httpRouteParentRefNamespace")
_httpRouteHostname = option("config.httpRouteHostname")
_httpRouteAnnotations = option("config.httpRouteAnnotations") or {}
_extraEnvVars = option("config.extraEnvVars") or {}

# Config instance with command-line overrides
config: schema.ConfigViewer = schema.ConfigViewer {
    if _image:
        image = _image
    if _namespace:
        namespace = _namespace
    if _replicas != None:
        replicas = int(_replicas)
    if _labelSelector:
        labelSelector = _labelSelector
    if _cacheTtl:
        cacheTtl = _cacheTtl
    # An empty value is meaningful here: it disables the check.
    if _mustReactSeverities != None:
        mustReactSeverities = _mustReactSeverities
    if _logLevel:
        logLevel = _logLevel
    if _httpRouteEnabled:
        httpRouteEnabled = _httpRouteEnabled in [True, "True", "true"]
    if _httpRouteParentRefName:
        httpRouteParentRefName = _httpRouteParentRefName
    if _httpRouteParentRefNamespace:
        httpRouteParentRefNamespace = _httpRouteParentRefNamespace
    if _httpRouteHostname:
        httpRouteHostname = _httpRouteHostname
    if _httpRouteAnnotations:
        httpRouteAnnotations = _httpRouteAnnotations
    if _extraEnvVars:
        extraEnvVars = _extraEnvVars
}

# Common labels applied to all resources. part-of homerun2 like every other
# component: the viewer then lists itself, as a component it does not know.
commonLabels = {
    "app.kubernetes.io/name": config.name
    "app.kubernetes.io/instance": config.name
    "app.kubernetes.io/component": "config-viewer"
    "app.kubernetes.io/part-of": "homerun2"
    **config.labels
}

# Selector labels for pods
selectorLabels = {
    "app.kubernetes.io/name": config.name
    "app.kubernetes.io/instance": config.name
}
