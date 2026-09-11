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
- Tests: `go test ./...` — no cluster needed; Kubernetes access is tested with `client-go`'s fake clientset
- Logging: `log/slog` (JSON/text), NOT pterm

## Components

| Component | Description |
|-----------|-------------|
| `main.go` | entrypoint, HTTP server, graceful shutdown |
| `internal/config/` | env config loading/validation, slog setup |
| `internal/handlers/` | health endpoint |
| `internal/banner/` | animated TUI startup banner |
| `dagger/main.go` | CI functions: Lint, Govulncheck, Test, SmokeTest, Build, BuildImage, ScanImage |

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
