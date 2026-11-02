# 07 - Container Image Packaging

This spec defines how the importer is packaged and distributed as a
multi-architecture container image.

## Design decisions

- Final runtime stage uses `ubi9-micro` as base (includes CA certificates),
  pinned by sha256 digest for reproducible builds (no silent base-image drift).
  A minimal prep stage (also pinned by digest) MAY be used to create fixtures the
  shell-less `ubi9-micro` cannot, such as the writable `/backups` directory. The
  shipped image still contains only the `ubi9-micro` layer plus the binary and
  the empty `/backups` directory.
- Multi-arch: `linux/amd64` and `linux/arm64`.
- Static binary: `CGO_ENABLED=0`, pure Go.
- Non-root: runs as UID `65534` (nobody).

## Requirements

### IMP-IMG-001: Dockerfile

The Dockerfile MUST:
- Use `registry.access.redhat.com/ubi9-micro` as the final runtime base, pinned
  by sha256 digest (not a floating `:latest` tag), so builds are reproducible and
  the shipped image never goes silently stale. A comment MUST record the
  human-readable version/tag the digest corresponds to. The digest MUST be
  refreshed periodically (manually, or via a bot such as Renovate/Dependabot) to
  pick up base-image security updates.
- COPY the pre-compiled binary as `/compliance-operator-importer`.
- Provide a writable `/backups` directory owned by `65534:65534` (the default
  `--backup-dir` for the container, see IMP-CLI-032). Because `ubi9-micro` has no
  shell, this directory is created in a minimal prep stage (also pinned by sha256
  digest) and copied in with `COPY --chown=65534:65534`.
- Set `ENV CO_IMPORTER_BACKUP_DIR=/backups` so the container defaults
  `--backup-dir` to the writable volume mount point (IMP-CLI-032).
- Set `USER 65534:65534`.
- Set `ENTRYPOINT ["/compliance-operator-importer"]`.

### IMP-IMG-002: Static binary

The Go binary MUST be compiled with:
- `CGO_ENABLED=0`
- `GOOS=linux`
- `GOARCH` set to the target architecture (`amd64` or `arm64`)

### IMP-IMG-003: Multi-architecture support

The build MUST:
- Build the Go binary once per target architecture.
- Build a container image per architecture, tagged with an `-$ARCH` suffix
  (e.g. `$IMAGE:$TAG-amd64`, `$IMAGE:$TAG-arm64`).
- Create a multi-arch manifest list under the plain tag
  (`$IMAGE:$TAG`) combining all architecture-specific images.
- Support at least `linux/amd64` and `linux/arm64`.

### IMP-IMG-004: Build targets

The Makefile MUST provide:
- `make image` — build container image for the host architecture.
- `make image-push` — build and push multi-arch images + manifest.
- Image name configurable via `IMAGE` env var with a placeholder default.
- Tag configurable via `TAG` env var (default: `latest`).

### IMP-IMG-005: Image metadata

The image MUST include OCI labels:
- `org.opencontainers.image.title=stackrox-co-importer`
- `org.opencontainers.image.description=Compliance Operator to ACS scan configuration importer`
- `org.opencontainers.image.source=https://github.com/guzalv/stackrox-co-importer`

## Non-goals

- CI/CD pipeline integration (future work).
- Helm chart or operator packaging.
- Signing or SBOM generation (deferred).
