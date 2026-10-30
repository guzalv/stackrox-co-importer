// Package packaging contains tests that assert the container packaging
// artifacts (Dockerfile, Makefile, .goreleaser.yaml) at the repo root satisfy
// specs/07-container-image.md. These have no build tag so they run in the
// fast `go test ./internal/...` job.
package packaging

import (
	"os"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, relPath string) string {
	t.Helper()
	data, err := os.ReadFile(relPath)
	if err != nil {
		t.Fatalf("reading %s: %v", relPath, err)
	}
	return string(data)
}

// IMP-IMG-001: the Dockerfile MUST use a sha256-digest-pinned ubi9-micro base
// (not a floating :latest tag), COPY the pre-compiled binary, run as
// USER 65534:65534, and set the importer as ENTRYPOINT.
func TestIMP_IMG_001_DockerfileBaseImageAndRuntime(t *testing.T) {
	dockerfile := readRepoFile(t, "../../Dockerfile")

	if !strings.Contains(dockerfile, "FROM registry.access.redhat.com/ubi9-micro@sha256:") {
		t.Error("Dockerfile does not pin registry.access.redhat.com/ubi9-micro by sha256 digest")
	}
	for _, line := range strings.Split(dockerfile, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "FROM") && strings.Contains(line, ":latest") {
			t.Errorf("Dockerfile FROM line uses a floating :latest tag: %q", line)
		}
	}

	if !strings.Contains(dockerfile, "COPY compliance-operator-importer /compliance-operator-importer") {
		t.Error("Dockerfile does not COPY compliance-operator-importer to /compliance-operator-importer")
	}
	if !strings.Contains(dockerfile, "USER 65534:65534") {
		t.Error("Dockerfile does not set USER 65534:65534")
	}
	if !strings.Contains(dockerfile, `ENTRYPOINT ["/compliance-operator-importer"]`) {
		t.Error(`Dockerfile does not set ENTRYPOINT ["/compliance-operator-importer"]`)
	}
}

// IMP-IMG-002: the Go binary MUST be compiled with CGO_ENABLED=0, GOOS=linux,
// and GOARCH set to each target architecture (amd64, arm64). The Makefile's
// image-push target cross-compiles per architecture; .goreleaser.yaml builds
// the release binaries the same way.
func TestIMP_IMG_002_StaticBinaryBuildFlags(t *testing.T) {
	makefile := readRepoFile(t, "../../Makefile")
	goreleaser := readRepoFile(t, "../../.goreleaser.yaml")

	if !strings.Contains(makefile, "CGO_ENABLED=0") {
		t.Error("Makefile does not set CGO_ENABLED=0 anywhere")
	}
	if !strings.Contains(goreleaser, "CGO_ENABLED=0") {
		t.Error(".goreleaser.yaml does not set CGO_ENABLED=0")
	}

	if !strings.Contains(makefile, "GOOS=linux") {
		t.Error("Makefile image-push path does not set GOOS=linux")
	}
	if !strings.Contains(goreleaser, "- linux") {
		t.Error(".goreleaser.yaml builds.goos does not list linux")
	}

	if !strings.Contains(makefile, "GOARCH=$(arch)") {
		t.Error("Makefile image-push target does not parameterize GOARCH by $(arch)")
	}
	if !strings.Contains(makefile, "ARCHS    := amd64 arm64") {
		t.Error("Makefile ARCHS variable does not list amd64 and arm64")
	}
	if !strings.Contains(goreleaser, "- amd64") || !strings.Contains(goreleaser, "- arm64") {
		t.Error(".goreleaser.yaml builds.goarch does not list both amd64 and arm64")
	}
}

// IMP-IMG-003: the build MUST produce one image per architecture tagged
// with an -$ARCH suffix, and a multi-arch manifest list under the plain tag,
// supporting at least linux/amd64 and linux/arm64. Asserts the actual
// mechanisms present: the Makefile's image-push target (per-arch docker build
// + push, then `docker manifest create`/`push`), and .goreleaser.yaml's
// per-arch docker image_templates plus docker_manifests.
func TestIMP_IMG_003_MultiArchManifest(t *testing.T) {
	makefile := readRepoFile(t, "../../Makefile")
	goreleaser := readRepoFile(t, "../../.goreleaser.yaml")

	// Makefile: per-arch image build+push, then a manifest combining them.
	if !strings.Contains(makefile, "docker build --platform linux/$(arch) -t $(IMAGE):$(TAG)-$(arch)") {
		t.Error("Makefile image-push does not build a per-arch image tagged $(IMAGE):$(TAG)-$(arch)")
	}
	if !strings.Contains(makefile, "docker push $(IMAGE):$(TAG)-$(arch)") {
		t.Error("Makefile image-push does not push the per-arch image")
	}
	if !strings.Contains(makefile, "docker manifest create $(IMAGE):$(TAG)") {
		t.Error("Makefile image-push does not create a multi-arch manifest under the plain tag")
	}
	if !strings.Contains(makefile, "docker manifest push $(IMAGE):$(TAG)") {
		t.Error("Makefile image-push does not push the multi-arch manifest")
	}

	// .goreleaser.yaml: per-arch docker build entries + docker_manifests
	// combining them under plain version/latest tags.
	if !strings.Contains(goreleaser, "goarch: amd64") || !strings.Contains(goreleaser, "goarch: arm64") {
		t.Error(".goreleaser.yaml dockers section does not define both amd64 and arm64 builds")
	}
	if !strings.Contains(goreleaser, "docker_manifests:") {
		t.Error(".goreleaser.yaml does not define docker_manifests")
	}
	if !strings.Contains(goreleaser, "-amd64") || !strings.Contains(goreleaser, "-arm64") {
		t.Error(".goreleaser.yaml image_templates do not use -amd64/-arm64 suffixes")
	}
}

// IMP-IMG-004: the Makefile MUST provide `image` and `image-push` targets,
// with IMAGE and TAG configurable via env vars (IMAGE default is a
// placeholder, TAG defaults to "latest").
func TestIMP_IMG_004_MakefileTargetsAndVars(t *testing.T) {
	makefile := readRepoFile(t, "../../Makefile")

	if !strings.Contains(makefile, "\nimage: build\n") && !strings.Contains(makefile, "\nimage:") {
		t.Error("Makefile does not define an `image:` target")
	}
	if !strings.Contains(makefile, "\nimage-push:") {
		t.Error("Makefile does not define an `image-push:` target")
	}
	if !strings.Contains(makefile, "IMAGE    ?=") && !strings.Contains(makefile, "IMAGE ?=") {
		t.Error("Makefile does not define a configurable IMAGE variable (IMAGE ?= ...)")
	}
	if !strings.Contains(makefile, "TAG      ?= latest") && !strings.Contains(makefile, "TAG ?= latest") {
		t.Error("Makefile does not default TAG to latest via TAG ?= latest")
	}
}

// IMP-IMG-005: the image MUST carry the three required OCI labels, with
// source matching the Dockerfile's actual repository URL.
func TestIMP_IMG_005_OCILabels(t *testing.T) {
	dockerfile := readRepoFile(t, "../../Dockerfile")

	wantLabels := []string{
		`LABEL org.opencontainers.image.title="stackrox-co-importer"`,
		`LABEL org.opencontainers.image.description="Compliance Operator to ACS scan configuration importer"`,
	}
	for _, label := range wantLabels {
		if !strings.Contains(dockerfile, label) {
			t.Errorf("Dockerfile missing label: %s", label)
		}
	}

	const wantSource = "github.com/guzalv/stackrox-co-importer"
	if !strings.Contains(dockerfile, "org.opencontainers.image.source=\"https://"+wantSource+"\"") {
		t.Errorf("Dockerfile org.opencontainers.image.source does not match expected repository %q", wantSource)
	}
}
