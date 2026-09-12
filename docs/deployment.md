# Deployment

## Artifacts

| Artifact | Reference |
|---|---|
| Container image | `ghcr.io/stuttgart-things/homerun2-config-viewer:<version>` (and `:latest` from the first release) |
| Kustomize base (OCI) | `ghcr.io/stuttgart-things/homerun2-config-viewer-kustomize:<version>` |

Both are published by the Release workflow. The kustomize base pins the released version into the image.

## KCL manifests

`kcl/` renders:

| Resource | Notes |
|---|---|
| ServiceAccount | `homerun2-config-viewer` |
| Role + RoleBinding | `get`/`list` on `deployments` (apps) and `configmaps` in the release namespace — nothing else |
| ConfigMap `<name>-config` | `LOG_LEVEL` + `extraEnvVars`, loaded via `envFrom` |
| Deployment | env `POD_NAMESPACE` (downward API), `HTTP_PORT`, `LABEL_SELECTOR`, `CACHE_TTL`, `MUST_REACT_SEVERITIES`; probes on `/healthz`; non-root, read-only root filesystem, all capabilities dropped |
| Service | port 80 → `http` |
| HTTPRoute | only with `config.httpRouteEnabled`; requires `config.httpRouteParentRefName` |

The namespace the viewer reads is its own (`POD_NAMESPACE`), which is exactly the namespace its Role covers. It lists itself as the `config-viewer` component.

### Options

Passed as `config.<option>` in a profile file (see `tests/kcl-deploy-profile.yaml`) or with `-D`:

| Option | Default |
|---|---|
| `image` | `ghcr.io/stuttgart-things/homerun2-config-viewer:latest` |
| `namespace` | `homerun2` |
| `replicas` | `1` |
| `labelSelector` | `app.kubernetes.io/part-of=homerun2` |
| `cacheTtl` | `10s` |
| `mustReactSeverities` | `error,critical` (an empty value disables the check) |
| `logLevel` | `info` |
| `httpRouteEnabled`, `httpRouteParentRefName`, `httpRouteParentRefNamespace`, `httpRouteHostname`, `httpRouteAnnotations` | disabled |
| `extraEnvVars` | `{}` |

### Render

```bash
task render-manifests-local                 # local kcl + yq
task render-manifests-quick                 # via the dagger kcl module
kcl run kcl -D config.namespace=homerun2 -D config.cacheTtl=30s
```

A profile file holds flat `config.*` keys. `kcl run -Y` expects `kcl_options` instead and would silently ignore such a file, which is why `render-manifests-local` converts it with `yq` first — the same conversion the dagger kcl module does.

### Push the kustomize base

```bash
export GITHUB_USER=... GITHUB_TOKEN=...
task push-kustomize-base
```

## Verifying RBAC

After deploying:

```bash
SA=system:serviceaccount:homerun2:homerun2-config-viewer
kubectl auth can-i list deployments.apps -n homerun2 --as=$SA   # yes
kubectl auth can-i list configmaps      -n homerun2 --as=$SA   # yes
kubectl auth can-i get secrets          -n homerun2 --as=$SA   # no
kubectl auth can-i watch deployments.apps -n homerun2 --as=$SA # no
kubectl auth can-i list deployments.apps -A --as=$SA           # no
```

A `503` page saying *forbidden* means the Role or RoleBinding is missing or in another namespace.

## GitOps

The homerun2 components are deployed from:

- **stuttgart-things/flux**, as a component under `apps/homerun2/components/` (an OCIRepository on the kustomize base plus a Kustomization with image and route patches) and a platform Kustomization under `apps/platform/components/`.
- **stuttgart-things/argocd**, through the `apps/homerun2/install` chart.

Apps for the viewer follow the same pattern once the first release is published.
