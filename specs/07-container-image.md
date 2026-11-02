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

### IMP-IMG-007: Install script

The repository MUST provide an `install.sh` at its root that downloads the
prebuilt binary for the host and drops it locally, so a user needs neither a Go
toolchain nor a container runtime. It is run as
`curl -fsSL https://raw.githubusercontent.com/guzalv/stackrox-co-importer/main/install.sh | bash`.

The script MUST:
- Detect the OS from `uname -s` (`Linux`->`linux`, `Darwin`->`darwin`) and the
  architecture from `uname -m` (`x86_64`/`amd64`->`amd64`, `aarch64`/`arm64`->`arm64`).
  Any other OS or architecture MUST fail with a clear message pointing to the
  releases page for a manual download.
- Resolve the latest release tag by following the `/releases/latest` HTTP
  redirect to `/releases/tag/<tag>` (no GitHub API call, so no token and no rate
  limit). A `VERSION` env var (with or without a leading `v`) MUST override this
  and skip the lookup.
- Download the matching release archive
  (`stackrox-co-importer_<version>_<os>_<arch>.tar.gz`) and `checksums.txt`, then
  verify the archive's SHA-256 against the recorded checksum. A mismatch or a
  missing checksum entry MUST abort before any binary is written.
- Extract only the `compliance-operator-importer` binary into `INSTALL_DIR`
  (default: the current directory), mark it executable, and print the resolved
  version and a run hint.
- Preflight for `curl`, `tar`, and a SHA-256 tool (`sha256sum` or `shasum`), and
  clean up its temporary download directory on exit.
- Support an `INSTALL_DIR` override and a `BASE_URL` override (default
  `https://github.com`, used for both the redirect and the asset download) so
  the script is testable against a stub server.

Note: a binary fetched with `curl` carries no `com.apple.quarantine` attribute,
so macOS Gatekeeper does not block it (unlike a browser download).

## Non-goals

- Helm chart or operator packaging.
- Signing or SBOM generation (deferred).
- Package-manager distribution (Homebrew, apt, etc.).
