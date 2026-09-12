"""
Main entry point for homerun2-config-viewer Kubernetes deployment
Imports all resources and exports manifests
"""

import serviceaccount
import role
import configmap
import deploy
import service
import httproute

# Export all manifests (filter out None values).
# Namespace is NOT emitted, as in the other homerun2 components: the consuming
# Application uses `CreateNamespace=true`, and PR-preview namespaces are
# pre-created. See stuttgart-things/homerun2-omni-pitcher#132.
manifests = [
    m for m in [
        serviceaccount.serviceAccount
        role.role
        role.roleBinding
        configmap.configMap
        deploy.deployment
        service.service
        httproute.httproute
    ] if m
]
