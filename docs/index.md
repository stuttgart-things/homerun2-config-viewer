# Homerun2 Config Viewer

Read-only view of which homerun2 alert triggers what in which catcher. It collects the streams, consumer groups and catcher profiles of one namespace from the Kubernetes API and evaluates them with homerun-library's `routing` package. It never talks to Redis, never reads Secrets and never publishes a message.

![Overview](images/overview.png)

## What it answers

- **Does anybody read this stream?** A pitcher on a stream no catcher reads still reports success.
- **What happens to an `error` from `github`?** For every catcher reading the stream: the WLED effect, LED display or notification output it triggers, or that it ignores the message.
- **Which severities fall through?** A catcher with rules that does not react to `error` or `critical` is a gap.
- **Is the configuration even loadable?** A missing profile ConfigMap, an unknown WLED effect, a severity with no color.

## How it works

```
Kubernetes API (get/list, one namespace)
  Deployments  app.kubernetes.io/part-of=homerun2
  ConfigMaps   all of them - the ones a Deployment references are not always labeled
        │
        ▼
internal/discovery
  component label        →  service kind, role
  env + envFrom          →  streams, consumer group, profile path   (as the kubelet resolves them)
  profile path           →  volume mount → ConfigMap key → parsed profile
        │
        ▼
internal/snapshot       one rebuild per CACHE_TTL, shared by concurrent requests
        │
        ▼
homerun-library routing  DryRun · BuildMatrix · Check
        │
        ├── internal/web  /, /matrix, /dryrun
        └── internal/api  /api/components, /api/findings, /api/streams, /api/matrix, /api/dryrun
```

## Known components

| `app.kubernetes.io/component` | Service | Role | Default stream | Profile |
|---|---|---|---|---|
| `api` | omni-pitcher | pitcher | `messages`, or the streams its `ROUTES_CONFIG` routes to (nothing with `PITCHER_MODE=file`) | — |
| `pitcher` | git-pitcher | pitcher | `messages` | — |
| `pitcher` | demo-pitcher | pitcher | `homerun` for `PITCH_TARGET` `redis`/`both`; with `omni-pitcher`/`both` it posts to `OMNI_PITCHER_URL`/`OMNI_PITCHER_API_PATH` (default `http://localhost:4000/generic`) | — |
| `watcher` | k8s-pitcher | pitcher | its profile's `spec.redis.stream`, or it posts to `spec.pitcher.addr` | `-profile` |
| `consumer` | core-catcher | catcher | `messages` | none: reacts to everything |
| `light-catcher` | light-catcher | catcher | `messages` | `PROFILE_PATH` |
| `led-catcher` | led-catcher | catcher | `messages` | `PROFILE_PATH` |
| `notifier` | notification-catcher | catcher | **`alerts`** | `CONFIG_PATH` |
| `analytics` | scout | — | — | — |
| `wled-mock`, `config-viewer` | wled-mock, this viewer | — | — | — |

Each default above is read from the service's code.

## Quick start

```bash
KUBECONFIG=~/.kube/my-cluster NAMESPACE=homerun2 LOG_FORMAT=text go run .
```

Then open <http://localhost:8080>. Configuration, RBAC and limits are in the [README](https://github.com/stuttgart-things/homerun2-config-viewer#readme).
