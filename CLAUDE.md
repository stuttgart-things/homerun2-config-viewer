# CLAUDE.md

## Project

homerun2-config-viewer — Go microservice that shows which homerun2 alert triggers what in which catcher. It reads the Deployments and profile ConfigMaps of one namespace from the Kubernetes API and evaluates them with homerun-library's `routing` package: components, findings, a severity × system matrix and a dry run. Read-only; it never talks to Redis and never reads Secrets.

Background and design: stuttgart-things/homerun-library#122, build-out tracked in #11.

## Tech Stack

- **Language**: Go 1.26+
- **Library**: `homerun-library/v4` ≥ v4.3.0 — `routing` (profile parsers, DryRun, BuildMatrix, Check)
- **Kubernetes**: `client-go`, namespace-scoped, `get/list` on deployments and configmaps only
- **UI**: `embed` + `html/template` + vendored htmx + Pico CSS (as in core-catcher / demo-pitcher)
- **Build**: ko (`.ko.yaml`), no Dockerfile
- **CI**: Dagger module (`dagger/`), Taskfile
- **Deploy**: KCL manifests (`kcl/`), Kustomize OCI base
- **Infra**: GitHub Actions, semantic-release. The Release workflow is manual-only until v1 is complete (#11); it then switches to running after every image build on main, as in the sibling services.

## Git Workflow

**Branch-per-issue with PR and merge.** Every change gets its own branch, PR, and merge to main.

### Branch naming

- `fix/<issue-number>-<short-description>` for bugs
- `feat/<issue-number>-<short-description>` for features
- `test/<issue-number>-<short-description>` for test-only changes
- `chore/<issue-number>-<short-description>` for infra/CI changes

### Commit messages

- Conventional commits: `fix:`, `feat:`, `test:`, `chore:`, `ci:`, `docs:`
- End with a `Co-Authored-By:` trailer naming the Claude model when Claude authored
- Include `Closes #<issue-number>` to auto-close issues

## Code Conventions

- No Dockerfile — ko builds the image
- Config via environment variables, loaded and validated once at startup; every invalid value is reported, and startup fails
- **No silent defaults for what is shown**: a value the viewer cannot resolve (a Secret ref, an unknown component, a profile it cannot find) is shown as unresolved, never guessed
- Rule evaluation belongs in homerun-library `routing`, not here — this service collects and presents
- Service defaults (stream, consumer group, profile path, publishing modes) live in `internal/discovery/kinds.go` and `discovery.go`, each read from the service's code. When a service changes a default, change it there too
- What a catcher does when its profile file is missing (`missingProfileEffect` in `internal/discovery/profiles.go`) is read from the catchers' code too: light-catcher lights nothing, led-catcher runs with an empty profile, notification-catcher does not start
- A catcher with a profile path never gets a nil `routing.Profile` — nil means "reacts to every message". A profile that cannot be used gets a stand-in that says why
- Tests: `go test ./...` — no cluster needed; Kubernetes access is tested with `client-go`'s fake clientset
- Logging: `log/slog` (JSON/text), NOT pterm

## Components

| Component | Description |
|-----------|-------------|
| `main.go` | entrypoint, HTTP server, graceful shutdown |
| `internal/config/` | env config loading/validation, slog setup |
| `internal/handlers/` | health endpoint |
| `internal/kube/` | clientset from `KUBECONFIG` or in-cluster |
| `internal/discovery/` | Deployments → components: kind, role, streams, consumer group, notes; profile path → volume mount → ConfigMap key → parsed with homerun-library `routing`; `RoutingComponents()` for DryRun/BuildMatrix/Check |
| `internal/api/` | JSON API over the snapshot (see below) |
| `internal/web/` | HTML pages: overview, matrix, dry run (`embed` templates, vendored htmx 2.0.4, Pico CSS) |
| `internal/fixture/` | fake-cluster fixtures for tests of `api` and `web` (imported by tests only) |
| `internal/snapshot/` | TTL cache over discovery: concurrent requests share one rebuild, a failed rebuild keeps the last good snapshot and is not retried within the TTL |
| `internal/banner/` | animated TUI startup banner |
| `dagger/main.go` | CI functions: Lint, Govulncheck, Test, SmokeTest, Build, BuildImage, ScanImage |

## Pages

| Path | Shows |
|---|---|
| `/` | findings grouped by kind, streams (who publishes, who reads; unread streams highlighted), components |
| `/matrix?stream=` | severity × system for one stream; a missing reaction to a must-react severity is a highlighted gap |
| `/dryrun` | form → what every catcher would do; htmx swaps the result in place, a plain POST renders the full page |

Server-rendered; everything works without JavaScript. No snapshot is a `503` page saying why.

## API

| Method & path | Returns |
|---|---|
| `GET /api/components` | every discovered component with values, sources, profile status, start problems, notes |
| `GET /api/findings` | `routing.Check` over routed components, then viewer findings: `pod-cannot-start`, `scaled-to-zero`, `streams-unknown`, `profile-missing`, `profile-unresolved` |
| `GET /api/streams` | per stream: routed pitchers and catchers (with consumer group) |
| `GET /api/matrix?stream=<s>[&severities=a,b]` | `routing.BuildMatrix` |
| `POST /api/dryrun` | body `{"stream": "...", "message": {homerun.Message}}` → `routing.DryRun`, `pitchers`, `reachesNobody` |

Every response carries `meta`: namespace, label selector, `takenAt`, and `refreshError`/`refreshFailedAt` when the latest rebuild failed. No snapshot at all is `503` with `{"error": ...}`.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HTTP_PORT` | `8080` | Port for UI, API and `/healthz` |
| `NAMESPACE` | — | Namespace to read; falls back to `POD_NAMESPACE`, then the service account namespace. Startup fails if none is set |
| `LABEL_SELECTOR` | `app.kubernetes.io/part-of=homerun2` | Selects the homerun2 Deployments |
| `KUBECONFIG` | *(empty)* | Kubeconfig for running outside a cluster; empty means in-cluster |
| `CACHE_TTL` | `10s` | How long a namespace snapshot is reused (`0` rebuilds per request) |
| `MUST_REACT_SEVERITIES` | `error,critical` | Severities every catcher with rules should react to; empty disables the check |
| `LOG_FORMAT` | `json` | `json` or `text` |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

## Testing

```bash
# Unit tests
go test ./...

# Discovery against a real cluster (read-only; build tag keeps it out of CI)
KUBECONFIG=~/.kube/homerun2-test1 NAMESPACE=homerun2 \
  go test -tags cluster -run TestDiscover_Cluster -v ./internal/discovery/

# Lint
golangci-lint run

# The CI steps, locally via Dagger
task lint
task govulncheck
task test-dagger
task smoke-test
task build-scan-image-ko

# Run against a cluster
KUBECONFIG=~/.kube/homerun2-test1 NAMESPACE=homerun2 LOG_FORMAT=text go run .
curl localhost:8080/healthz
```

## Reference Projects

- `homerun-library` — `routing` package; any matching rule lives there
- `homerun2-light-catcher`, `homerun2-core-catcher` — sibling service structure
- `homerun2-k8s-pitcher` — Kubernetes client setup
