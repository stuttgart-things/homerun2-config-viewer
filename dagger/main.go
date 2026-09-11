// Dagger CI module for homerun2-config-viewer
//
// Provides lint, vulnerability check, tests, a smoke test of the built
// binary, image build and image scanning. Delegates to the stuttgart-things
// Dagger modules where they fit.

package main

import (
	"context"
	"dagger/dagger/internal/dagger"
	"fmt"
)

type Dagger struct{}

// Lint runs golangci-lint on the source code
func (m *Dagger) Lint(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="500s"
	timeout string,
) *dagger.Container {
	return dag.Go().Lint(src, dagger.GoLintOpts{
		Timeout: timeout,
	})
}

// Govulncheck reports vulnerabilities from the Go vulnerability database that
// are reachable from this module's code. It complements the Trivy image scan:
// Trivy covers the base image, this covers the Go module graph.
func (m *Dagger) Govulncheck(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="1.26.6"
	goVersion string,
	// +optional
	// +default="latest"
	govulncheckVersion string,
	// +optional
	// +default=false
	failOnVuln bool,
) *dagger.File {
	const reportPath = "/tmp/govulncheck-report.txt"

	// govulncheck exits 3 when it finds something. Capture the report first and
	// always echo it, so the run is readable whether or not it is a hard gate.
	script := fmt.Sprintf(`
set -u
govulncheck ./... > %[1]s 2>&1
code=$?
cat %[1]s
if [ "%[2]t" = "true" ] && [ "$code" -ne 0 ]; then
  echo "govulncheck found vulnerabilities (exit $code)" >&2
  exit "$code"
fi
exit 0
`, reportPath, failOnVuln)

	return dag.Container().
		From("golang:"+goVersion).
		WithEnvVariable("GOTOOLCHAIN", "auto").
		WithDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"go", "install", "golang.org/x/vuln/cmd/govulncheck@" + govulncheckVersion}).
		WithExec([]string{"sh", "-c", script}).
		File(reportPath)
}

// Test runs the Go test suite. No cluster is needed: Kubernetes access is
// tested against client-go's fake clientset.
//
// It returns the test output and fails the call when a test fails.
func (m *Dagger) Test(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="1.26.6"
	goVersion string,
	// +optional
	// +default="./..."
	testPath string,
) (string, error) {
	return goContainer(src, goVersion).
		WithExec([]string{"go", "test", "-race", "-cover", testPath}).
		Stdout(ctx)
}

// Build compiles the Go binary
func (m *Dagger) Build(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="main"
	binName string,
	// +optional
	// +default=""
	ldflags string,
	// +optional
	// +default="1.26.6"
	goVersion string,
	// +optional
	// +default="linux"
	os string,
	// +optional
	// +default="amd64"
	arch string,
) *dagger.Directory {
	return dag.Go().BuildBinary(src, dagger.GoBuildBinaryOpts{
		GoVersion:  goVersion,
		Os:         os,
		Arch:       arch,
		BinName:    binName,
		Ldflags:    ldflags,
		GoMainFile: "main.go",
	})
}

// SmokeTest builds the binary, starts it and checks that /healthz answers.
// The viewer needs no cluster to start - only to show something - so this
// catches a binary that does not start or does not serve, without one.
func (m *Dagger) SmokeTest(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="1.26.6"
	goVersion string,
) (string, error) {
	bin := m.Build(ctx, src, "homerun2-config-viewer", "", goVersion, "linux", "amd64").
		File("homerun2-config-viewer")

	viewer := dag.Container().
		From("alpine:3.22").
		WithFile("/usr/local/bin/homerun2-config-viewer", bin).
		WithEnvVariable("NAMESPACE", "homerun2").
		WithEnvVariable("LOG_FORMAT", "text").
		WithEnvVariable("HTTP_PORT", "8080").
		WithExposedPort(8080).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{"/usr/local/bin/homerun2-config-viewer"},
		})

	return dag.Container().
		From("alpine:3.22").
		WithServiceBinding("viewer", viewer).
		WithExec([]string{"sh", "-c", `
set -eu
for i in $(seq 1 30); do
  if body=$(wget -qO- http://viewer:8080/healthz); then
    echo "$body"
    echo "$body" | grep -q '"status":"healthy"'
    exit 0
  fi
  sleep 1
done
echo "viewer did not answer /healthz within 30s" >&2
exit 1
`}).
		Stdout(ctx)
}

// BuildImage builds a container image using ko and optionally pushes it
func (m *Dagger) BuildImage(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="ko.local/homerun2-config-viewer"
	repo string,
	// +optional
	// +default="false"
	push string,
) (string, error) {
	return dag.Go().KoBuild(ctx, src, dagger.GoKoBuildOpts{
		Repo: repo,
		Push: push,
	})
}

// ScanImage scans a container image for vulnerabilities using Trivy
func (m *Dagger) ScanImage(
	ctx context.Context,
	imageRef string,
	// +optional
	// +default="HIGH,CRITICAL"
	severity string,
) *dagger.File {
	return dag.Trivy().ScanImage(imageRef, dagger.TrivyScanImageOpts{
		Severity: severity,
	})
}

func goContainer(src *dagger.Directory, goVersion string) *dagger.Container {
	return dag.Container().
		From("golang:"+goVersion).
		WithDirectory("/src", src).
		WithWorkdir("/src").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("gomod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("gobuild"))
}
