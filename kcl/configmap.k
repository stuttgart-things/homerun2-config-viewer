"""
ConfigMap for homerun2-config-viewer, loaded into the container with envFrom
"""

import labels

config = labels.config

configMap = {
    apiVersion: "v1"
    kind: "ConfigMap"
    metadata: {
        name: "{}-config".format(config.name)
        namespace: config.namespace
        labels: labels.commonLabels
    }
    data: {
        LOG_LEVEL: config.logLevel
        **config.extraEnvVars
    }
}
