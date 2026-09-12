# CI/CD

## Dagger functions

`dagger/main.go`, wrapping the stuttgart-things `go` and `trivy` modules:

| Function | Does | Task |
|---|---|---|
| `lint` | golangci-lint with `.golangci.yaml` | `task lint` |
| `govulncheck` | reachable Go vulnerabilities; `--fail-on-vuln` makes it a gate | `task govulncheck` |
| `test` | `go test -race -cover ./...` — no cluster, no services | `task test-dagger` |
| `smoke-test` | builds the binary, starts it without a cluster, expects `/healthz` healthy and `503` from `/api/components` and `/` | `task smoke-test` |
| `build` | the binary | `task build-output-binary` |
| `build-image` | ko image (optionally pushed) | `task build-scan-image-ko` |
| `scan-image` | Trivy HIGH/CRITICAL | `task build-scan-image-ko` |

## Workflows

| Workflow | Trigger | Does |
|---|---|---|
| CI - Dagger Build & Test | push/PR to main | lint → govulncheck (blocking) → test → smoke test |
| Build, Push & Scan Container Image | push/PR to main | ko build + scan; PRs push `ghcr.io/…:pr-<n>` |
| Push PR Kustomize OCI | PR | kustomize base `ghcr.io/…-kustomize:pr-<n>-<sha>` |
| Run Repository Linting | push/PR | YAML and Markdown linting |
| Cleanup PR Artifacts | PR closed | deletes the PR-tagged image and kustomize versions |
| Release | after a successful image build on main (or by hand) | semantic-release; image with version tag and `:latest`; kustomize base with the version pinned |
| Deploy Pages | after Release | TechDocs site |

### Release

Releases follow conventional commits: `feat:` → minor, `fix:` → patch.

The Release workflow runs after every successful image build on main, as in the other homerun2 services: a merge with `feat:` or `fix:` commits is released right away. `task trigger-release` starts it by hand, for example to retry a failed run. It ran by hand only until v1 was complete (#11), so the unfinished viewer was not published as v1.0.0.

The image's build date (`/healthz` `date`) comes from `BUILD_DATE`, which the release and ko-build workflow templates set; `.ko.yaml` falls back to `DATE`, then `unknown`.

## Local checks before a PR

```bash
go test -race ./...
golangci-lint run && golangci-lint run --build-tags cluster
pre-commit run --all-files
task smoke-test
```
