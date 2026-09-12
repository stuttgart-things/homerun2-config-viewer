"""
RBAC for homerun2-config-viewer: read access to Deployments and ConfigMaps in
its own namespace, and nothing else.

Namespace-scoped on purpose - a Role, not a ClusterRole. The viewer shows one
namespace. It never reads Secrets: a value from a Secret is shown as
unresolved. It never watches: it lists at most once per cacheTtl.
"""

import labels

config = labels.config

role = {
    apiVersion: "rbac.authorization.k8s.io/v1"
    kind: "Role"
    metadata: {
        name: config.name
        namespace: config.namespace
        labels: labels.commonLabels
    }
    rules: [
        {
            apiGroups: ["apps"]
            resources: ["deployments"]
            verbs: ["get", "list"]
        }
        {
            apiGroups: [""]
            resources: ["configmaps"]
            verbs: ["get", "list"]
        }
    ]
}

roleBinding = {
    apiVersion: "rbac.authorization.k8s.io/v1"
    kind: "RoleBinding"
    metadata: {
        name: config.name
        namespace: config.namespace
        labels: labels.commonLabels
    }
    roleRef: {
        apiGroup: "rbac.authorization.k8s.io"
        kind: "Role"
        name: config.name
    }
    subjects: [
        {
            kind: "ServiceAccount"
            name: config.name
            namespace: config.namespace
        }
    ]
}
