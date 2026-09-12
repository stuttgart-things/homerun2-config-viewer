"""
Schema definition for homerun2-config-viewer deployment configuration
"""

schema ConfigViewer:
    """Configuration for homerun2-config-viewer Kubernetes deployment"""

    # Metadata
    name: str = "homerun2-config-viewer"
    namespace: str = "homerun2"

    # Image
    image: str = "ghcr.io/stuttgart-things/homerun2-config-viewer:latest"
    imagePullPolicy: str = "IfNotPresent"

    # Replicas and resources. The viewer lists Deployments and ConfigMaps of
    # one namespace at most once per cacheTtl, so it needs little.
    replicas: int = 1
    cpuRequest: str = "25m"
    cpuLimit: str = "100m"
    memoryRequest: str = "32Mi"
    memoryLimit: str = "128Mi"

    # Viewer. The namespace it reads is its own (POD_NAMESPACE via the downward
    # API), which is also where the Role below grants read access.
    httpPort: int = 8080
    labelSelector: str = "app.kubernetes.io/part-of=homerun2"
    cacheTtl: str = "10s"
    # Severities every catcher with rules should react to. Empty disables the
    # check.
    mustReactSeverities: str = "error,critical"
    logLevel: str = "info"

    # Service
    serviceType: str = "ClusterIP"
    servicePort: int = 80

    # HTTPRoute (Gateway API)
    httpRouteEnabled: bool = False
    httpRouteParentRefName: str = ""
    httpRouteParentRefNamespace: str = ""
    httpRouteHostname: str = ""
    httpRouteAnnotations: {str:str} = {}

    # Extra environment variables, rendered into the <name>-config ConfigMap
    extraEnvVars: {str:str} = {}

    # Labels and annotations
    labels: {str:str} = {}
    annotations: {str:str} = {}

    check:
        replicas >= 0, "replicas must be >= 0"
        httpPort >= 1 and httpPort <= 65535, "httpPort must be a valid port"
        servicePort >= 1 and servicePort <= 65535, "servicePort must be a valid port"
        name != "", "name must not be empty"
        namespace != "", "namespace must not be empty"
        labelSelector != "", "labelSelector must not be empty"
        not httpRouteEnabled or httpRouteParentRefName != "", "httpRouteParentRefName is required when httpRouteEnabled"
