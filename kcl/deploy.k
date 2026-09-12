"""
Deployment for homerun2-config-viewer
"""

import labels

config = labels.config

_probe = {
    httpGet: {
        path: "/healthz"
        port: "http"
    }
}

deployment = {
    apiVersion: "apps/v1"
    kind: "Deployment"
    metadata: {
        name: config.name
        namespace: config.namespace
        labels: labels.commonLabels
    }
    spec: {
        replicas: config.replicas
        selector: {
            matchLabels: labels.selectorLabels
        }
        strategy: {
            type: "RollingUpdate"
            rollingUpdate: {
                maxSurge: 1
                maxUnavailable: 0
            }
        }
        template: {
            metadata: {
                labels: labels.commonLabels
            }
            spec: {
                serviceAccountName: config.name
                # The viewer reads the Kubernetes API with this token.
                automountServiceAccountToken: True
                containers: [
                    {
                        name: config.name
                        image: config.image
                        imagePullPolicy: config.imagePullPolicy
                        ports: [
                            {
                                name: "http"
                                containerPort: config.httpPort
                                protocol: "TCP"
                            }
                        ]
                        env: [
                            # The namespace the viewer reads is its own - the
                            # one its Role grants access to.
                            {
                                name: "POD_NAMESPACE"
                                valueFrom: {
                                    fieldRef: {
                                        fieldPath: "metadata.namespace"
                                    }
                                }
                            }
                            {
                                name: "HTTP_PORT"
                                value: str(config.httpPort)
                            }
                            {
                                name: "LABEL_SELECTOR"
                                value: config.labelSelector
                            }
                            {
                                name: "CACHE_TTL"
                                value: config.cacheTtl
                            }
                            {
                                name: "MUST_REACT_SEVERITIES"
                                value: config.mustReactSeverities
                            }
                        ]
                        envFrom: [
                            {
                                configMapRef: {
                                    name: "{}-config".format(config.name)
                                }
                            }
                        ]
                        resources: {
                            requests: {
                                cpu: config.cpuRequest
                                memory: config.memoryRequest
                            }
                            limits: {
                                cpu: config.cpuLimit
                                memory: config.memoryLimit
                            }
                        }
                        # /healthz does not call the Kubernetes API, so a slow
                        # API server never makes the viewer look dead. The
                        # viewer waits for nothing at startup; the startup
                        # probe only keeps the probes in step with the other
                        # homerun2 services.
                        startupProbe: _probe | {
                            periodSeconds: 5
                            timeoutSeconds: 3
                            failureThreshold: 12
                        }
                        livenessProbe: _probe | {
                            periodSeconds: 10
                            timeoutSeconds: 5
                            failureThreshold: 3
                        }
                        readinessProbe: _probe | {
                            periodSeconds: 5
                            timeoutSeconds: 3
                            failureThreshold: 3
                        }
                        securityContext: {
                            readOnlyRootFilesystem: True
                            runAsNonRoot: True
                            allowPrivilegeEscalation: False
                            capabilities: {
                                drop: ["ALL"]
                            }
                        }
                        volumeMounts: [
                            {
                                name: "tmp"
                                mountPath: "/tmp"
                            }
                        ]
                    }
                ]
                securityContext: {
                    runAsNonRoot: True
                    runAsUser: 65532
                    runAsGroup: 65532
                    fsGroup: 65532
                    seccompProfile: {
                        type: "RuntimeDefault"
                    }
                }
                volumes: [
                    {
                        name: "tmp"
                        emptyDir: {}
                    }
                ]
            }
        }
    }
}
