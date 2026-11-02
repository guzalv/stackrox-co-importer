# Compliance Operator to ACS Importer

> **Unofficial community tool.** This is a personal/community stopgap project,
> not an official Red Hat or StackRox product. It is not supported, endorsed, or
> maintained by Red Hat. Use it at your own risk. For the supported solution,
> track the Compliance Observer paradigm work in upstream StackRox.

A CLI tool that reads [Compliance Operator][co] scheduled-scan resources
(`ScanSettingBindings`, `ScanSettings`, `Profiles`) from OpenShift clusters and
creates equivalent scan configurations in
[Red Hat Advanced Cluster Security (ACS / StackRox)][acs]. It discovers the CO
resources, maps them to ACS v2 scan configurations, and creates or updates them
through the ACS API. It supports dry-run previews, multi-cluster merging,
idempotent re-runs, and structured JSON reporting.

## Quickstart

Three ways to get the tool, lowest-friction first. Each example previews with
`--dry-run`. Drop that flag to apply the changes.

### Via install script

The install script detects your OS and architecture (linux or darwin, amd64 or
arm64), downloads the matching prebuilt binary from the latest GitHub release,
verifies its SHA-256 checksum, and drops `compliance-operator-importer` in the
current directory. It installs nothing system-wide and needs no root. It needs
`curl`, `tar`, and a SHA-256 tool (`sha256sum` or `shasum`), which ship with
typical macOS and Linux.

```bash
curl -fsSL https://raw.githubusercontent.com/guzalv/stackrox-co-importer/main/install.sh | bash

export ROX_ENDPOINT="https://central.example.com:443"
export ROX_API_TOKEN="your-token-here"
./compliance-operator-importer --dry-run
```

Pick the target directory or pin a version with environment variables:

```bash
# install into a specific directory
curl -fsSL https://raw.githubusercontent.com/guzalv/stackrox-co-importer/main/install.sh | INSTALL_DIR=/usr/local/bin bash

# install a specific release instead of the latest
curl -fsSL https://raw.githubusercontent.com/guzalv/stackrox-co-importer/main/install.sh | VERSION=v0.0.13 bash
```

On macOS, a binary fetched with curl carries no quarantine attribute, so
Gatekeeper does not block it.

### Via Docker

Pass credentials as env vars. Mount your kubeconfig so the tool can read
Compliance Operator resources. Mount a writable volume at `/backups` so backups
survive the `--rm` container. See [Container image](#container-image) for the
backup volume, TLS, and podman details.

```bash
docker run --rm \
  -e ROX_ENDPOINT="https://central.example.com:443" \
  -e ROX_API_TOKEN="your-token-here" \
  -v "$HOME/.kube/config:/kubeconfig:ro" \
  -v "$PWD/co-backups:/backups" \
  -e KUBECONFIG=/kubeconfig \
  ghcr.io/guzalv/stackrox-co-importer:latest --dry-run
```

### Via compiled binary

Requires Go 1.26+.

```bash
git clone https://github.com/guzalv/stackrox-co-importer.git
cd stackrox-co-importer
make build
# Binary is at ./bin/compliance-operator-importer

export ROX_ENDPOINT="https://central.example.com:443"
export ROX_API_TOKEN="your-token-here"
./bin/compliance-operator-importer --dry-run
```

**TLS note:** most ACS Central deployments use a self-signed certificate. When
you run the binary directly (install script or compiled binary), either trust
Central's CA with `--ca-cert-file ./central-ca.pem` or skip verification with
`--insecure-skip-verify` (dev/test only). For the Docker path, see
[Container image](#container-image).

## Why

Organizations running OpenShift with the Compliance Operator already have
scheduled compliance scans defined as Kubernetes resources. Adopting ACS for
centralized security management means recreating those scan definitions in ACS
Central by hand, which scales poorly across clusters. This tool automates that
migration.

## Features

- Discovers `ScanSettingBindings`, `ScanSettings`, and `Profiles` from one or
  more clusters via kubeconfig.
- Auto-discovers the ACS cluster ID from the secured-cluster admission-control
  ConfigMap. No manual cluster-ID mapping needed.
- Maps CO schedules, profiles, and bindings to ACS v2 scan configuration
  payloads.
- Merges identical bindings from multiple clusters into one ACS scan config
  targeting all clusters.
- Create-only by default. `--overwrite-existing` updates existing configs in
  place.
- `--dry-run` previews all actions without writing to ACS.
- `--report-json` writes a structured report for automation and audit.
- `--exclude <regex>` skips bindings by name pattern (repeatable, Go regex).
- `--list-ssbs` prints discovered binding names without importing (no ACS
  credentials required).
- Retries transient ACS API failures with configurable limits.
- Static binary, no runtime dependencies. Runs as a container image on `amd64`
  and `arm64`.

## Prerequisites

### ACS / StackRox

You need a running ACS Central instance (4.x or later) reachable over HTTPS.
Install ACS Central and Secured Cluster by following the
[official documentation][acs-install]. The Secured Cluster must be registered
with Central so the importer can auto-discover cluster IDs.

You also need one credential set for the importer to authenticate with Central:

- **API token** (recommended): generate one from the ACS UI under
  _Platform Configuration > Integrations > Authentication Tokens_. The token
  needs the `Compliance` permission.
- **Basic auth**: the `admin` user and password. Suitable for development and
  testing, not for production.

### Compliance Operator

The Compliance Operator must be installed on each OpenShift cluster whose scan
definitions you want to import. Install it from OperatorHub or follow the
[Compliance Operator documentation][co-install]. You need at least one
`ScanSettingBinding` referencing a `ScanSetting` and one or more `Profiles` or
`TailoredProfiles`. The importer reads these resources but never modifies them.

### Local tools

- Go 1.26+ (to build from source)
- `kubectl` or `oc` configured with kubeconfigs for your target clusters
- `docker` or `podman` (only to build or run the container image)

## Authentication

The importer infers the authentication mode from environment variables:

| Variable             | Auth mode | Notes                                 |
|----------------------|-----------|---------------------------------------|
| `ROX_API_TOKEN`      | Token     | Recommended. Set the token value.     |
| `ROX_ADMIN_PASSWORD` | Basic     | Used with `ROX_ADMIN_USER` (default: `admin`). |

If both are set, the importer uses token auth and prints a warning to stderr. If
neither is set, it exits with an error listing both options. (`--list-ssbs`
needs no credentials.)

## Flags

| Flag                    | Default                | Description                                 |
|-------------------------|------------------------|---------------------------------------------|
| `--endpoint`            | `$ROX_ENDPOINT`        | ACS Central endpoint (HTTPS).               |
| `--co-namespace`        | `openshift-compliance` | Namespace to read CO resources from.        |
| `--co-all-namespaces`   | `false`                | Read CO resources from all namespaces.      |
| `--context`             | all contexts           | Kubernetes context to process (repeatable). |
| `--exclude <regex>`     |                        | Exclude SSBs whose names match this Go regex (repeatable, OR-ed). |
| `--list-ssbs`           | `false`                | Print `namespace/name` of all discovered SSBs and exit. No ACS credentials required. |
| `--dry-run`             | `false`                | Preview actions without writing to ACS.     |
| `--overwrite-existing`  | `false`                | Update existing ACS scan configs on match.  |
| `--report-json <path>`  |                        | Write structured JSON report to file.       |
| `--request-timeout`     | `30s`                  | Timeout per HTTP request.                   |
| `--max-retries`         | `5`                    | Max retry attempts for transient failures.  |
| `--ca-cert-file`        |                        | Path to PEM CA certificate bundle.          |
| `--insecure-skip-verify`| `false`                | Skip TLS certificate verification.          |
| `--username`            | `admin`                | Username for basic auth mode.               |
| `--delete-existing`     | `false`                | Delete externally-managed (e.g. GitOps) `ScanSettingBindings` that block a create, after confirmation. See [Adopting externally-managed ScanSettingBindings](#adopting-externally-managed-scansettingbindings). |
| `--yes` / `-y`          | `false`                | Skip the confirmation prompt for `--delete-existing`. |
| `--backup-dir <path>`   | `./co-importer-backup-<timestamp>` | Where backups and the resumable plan (`plan.json`) are written. Reusing a prior run's value resumes from it. |
| `--settle-timeout`      | `5s`                   | How long to re-check a deleted `ScanSettingBinding` for GitOps self-heal before creating. |
| `--help` / `-h`         |                        | Print usage and exit 0.                     |
| `--version`             |                        | Print version and exit 0.                   |

## Exit codes

| Code | Meaning                                        |
|------|------------------------------------------------|
| `0`  | All bindings processed successfully.           |
| `1`  | Fatal configuration or preflight error.        |
| `2`  | Some bindings failed, **or** a create was skipped because it was blocked by an external ScanSettingBinding (a `gitops` problem). A requested import did not fully happen — see `problems[]`. |

## Multi-cluster usage

The importer processes all contexts in your kubeconfig by default. Each
kubeconfig file is loaded independently (no credential merging), so identically
named contexts in different files are handled correctly.

```bash
export KUBECONFIG="$HOME/.kube/cluster-a:$HOME/.kube/cluster-b"
./bin/compliance-operator-importer --dry-run
```

To limit to specific contexts:

```bash
./bin/compliance-operator-importer --context cluster-a --context cluster-b
```

When the same `ScanSettingBinding` name exists on multiple clusters with
matching profiles and schedule, the importer merges them into one ACS scan
configuration targeting all clusters.

## Selective import with --exclude

Skip bindings by name using Go regular expressions. Multiple patterns are OR-ed.
A binding is excluded if any pattern matches its name.

```bash
# Skip a specific binding by exact name
./bin/compliance-operator-importer --exclude "ocp4-cis-node"

# Skip all ocp4-* bindings
./bin/compliance-operator-importer --exclude "ocp4-.*"

# Combine multiple patterns
./bin/compliance-operator-importer --exclude "ocp4-cis-node" --exclude "rhcos4-.*"
```

## Discover bindings without importing (--list-ssbs)

Print all discovered `ScanSettingBindings` (`namespace/name`, sorted) without
contacting ACS. ACS credentials are not required.

```bash
# List all discovered SSBs
./bin/compliance-operator-importer --list-ssbs

# List with --exclude filtering applied
./bin/compliance-operator-importer --list-ssbs --exclude "ocp4-.*"
```

## Adopting externally-managed ScanSettingBindings

RHACS 4.11+/5.0+ Central refuses to create (or update) a scan configuration
whose profiles overlap an existing `ScanSettingBinding` on the target cluster
that ACS doesn't manage itself (one without the
`app.kubernetes.io/name: stackrox` label — typically one you authored directly
or one managed by a GitOps tool like Argo CD or Flux). Since the importer's
whole job is to turn exactly such a binding into an ACS scan config, **every
create is blocked by this check unless the binding is removed first.**

By default, the importer never touches that binding. It reports the conflict and
skips:

```bash
./bin/compliance-operator-importer
# ...
#   [WARN] skipped "cis-weekly": blocked by external ScanSettingBinding(s); see problems for remediation
```

The `problems[]` entry (category `gitops`) names the blocking binding(s) and how
to proceed. You have two options.

**Option 1 — let the importer remove it, with confirmation:**

```bash
./bin/compliance-operator-importer --delete-existing
# Delete external ScanSettingBinding(s) openshift-compliance/cis-weekly (context my-cluster)
# blocking ACS scan configuration "cis-weekly"? [y/N]
```

Add `--yes` to skip the interactive prompt. This is required for
non-interactive use. The importer refuses `--delete-existing` without `--yes`
when stdin is not a terminal. Before deleting anything, the importer writes a
YAML record of every binding (and its referenced `ScanSetting`) it is about to
delete, plus a resumable plan, to `--backup-dir` (default: a timestamped
directory in the current working directory). The record holds the identity and
key fields, not a round-trippable Kubernetes manifest.

**In a container**, the interactive prompt needs a TTY. Add `-it` so the `[y/N]`
confirmation can be shown:

```bash
podman run --rm -it ... ghcr.io/guzalv/stackrox-co-importer:latest --delete-existing
```

Without a TTY — the default for `docker run`, and always in CI — the importer
refuses `--delete-existing` alone. Pass `--yes` instead to delete the blocking
binding(s) without prompting:

```bash
podman run --rm ... ghcr.io/guzalv/stackrox-co-importer:latest --delete-existing --yes
```

**Option 2 — remove it yourself** (delete it from your GitOps source and let the
tool prune it, or `oc delete` it directly), then re-run the importer with no
extra flags. A plain run already wrote the backup and plan on its first pass, so
a re-run **resumes from that plan** instead of re-discovering the binding (which
is gone by then). Point `--backup-dir` at the same directory used the first time,
or keep the default path, which stays stable as long as you reuse it.

**If your binding is managed by a GitOps tool with self-healing enabled**
(Argo CD `selfHeal: true`, Flux, etc.), you **must pause its reconciliation for
that resource first**. Deleting a binding a GitOps tool actively reconciles only
wins a race the tool usually wins back in about a second. The importer guards
against this: after deleting, it re-checks for `--settle-timeout` (default `5s`)
to see whether the binding reappeared. If it does, the create is skipped with a
`gitops` problem telling you to pause reconciliation and re-run. This is a safety
net against forgetting, not a sanctioned way to skip pausing. A GitOps tool with
a slower reconcile loop can still recreate the old binding minutes later, after
ACS has created its own. The only reliable fix is pausing reconciliation for that
resource before running `--delete-existing`. The importer has no
GitOps-tool-specific integration by design. This is a manual prerequisite.

Once adopted, ACS/Sensor creates its own `ScanSetting`+`ScanSettingBinding`
(labeled `app.kubernetes.io/name: stackrox`) reusing the same name. The importer
never processes stackrox-labeled bindings as import sources, so a re-run sees
nothing left to do for it. The original `ScanSetting` the deleted binding
referenced is left in place (it may be shared by other bindings). The importer
flags it as a `problems[]` entry with category `orphan` so you can decide whether
to clean it up.

## JSON report

Use `--report-json` for a machine-readable report:

```bash
./bin/compliance-operator-importer --report-json report.json
```

The report includes metadata, counts (`discovered`, `create`, `skip`, `failed`),
per-binding details, and a `problems` array with descriptions and fix hints.

`discovered` is the total number of bindings found before filtering, so it
includes bindings that produce no `items[]` entry — those removed by `--exclude`
and the stackrox-labeled bindings the importer never re-imports. As a result
`discovered` can be larger than `create + skip + failed`.

## Container image

Build the image locally or pull the pre-built one:

```bash
# Build for your local architecture
make image

# Or pull the pre-built image
docker pull ghcr.io/guzalv/stackrox-co-importer:latest
```

Run it with credentials as env vars and your kubeconfig mounted. Mount a
writable volume at `/backups` too. On an apply run the importer backs up any
blocking Compliance Operator `ScanSettingBindings` there before touching them,
and that backup must survive the `--rm` container so you can resume after
removing them. The container defaults `--backup-dir` to `/backups`, so you only
need the mount:

```bash
docker run --rm \
  -e ROX_ENDPOINT="https://central.example.com:443" \
  -e ROX_API_TOKEN="your-token-here" \
  -v "$HOME/.kube/config:/kubeconfig:ro" \
  -v "$PWD/co-backups:/backups" \
  -e KUBECONFIG=/kubeconfig \
  ghcr.io/guzalv/stackrox-co-importer:latest --dry-run
```

The `/backups` volume must be writable by the container's non-root user (UID
65534):

- With podman, append `:U` to the mount (`-v "$PWD/co-backups:/backups:U"`) to
  chown it automatically.
- With Docker, make the host directory writable by UID 65534 first (e.g.
  `mkdir -p co-backups && chmod 777 co-backups`).

To trust a self-signed Central CA, mount the CA file too and add
`--ca-cert-file /ca.pem` (or pass `--insecure-skip-verify` for dev/test).

## Development

### First-time setup

After cloning, activate the pre-commit hooks (runs lint + unit tests before
every commit):

```bash
make setup
```

This is a one-time step per clone. Without it, lint and test errors are only
caught by CI after the push.

### Running tests

```bash
make test            # Godog scenarios + unit tests
make test-verbose    # Step-by-step Godog output
make test-cover      # Unit tests with coverage report
make test-e2e        # Acceptance tests against a real cluster
```

### Linting

Install [golangci-lint][golangci] and run:

```bash
make lint
```

Linter configuration is in `.golangci.yml`.

### Project structure

```
cmd/importer/       CLI entry point
internal/           Production code (config, mapping, reconcile, ...)
specs/              Specification files (source of truth)
features/           Godog step definitions (BDD test glue)
e2e/                Acceptance tests against real clusters
hack/               Helper scripts
```

The project follows spec-driven development. `.feature` files in `specs/` are
executable via [Godog][godog] and serve as both specification and test suite.

### CI pipeline

Every push to `main` runs this pipeline:

1. **Lint** — golangci-lint with a curated set of linters.
2. **Test** — Godog scenarios and unit tests.
3. **Build** — binary compilation and container image build.
4. **E2E** — starts a kind cluster with StackRox (lightweight mode) and the real
   Compliance Operator (generic-platform mode, falls back to CRDs-only if the
   operator install fails), then runs acceptance tests against real APIs.
5. **Release** — if e2e passes on `main`, auto-increments the patch version,
   creates a Git tag, and publishes multi-arch binaries and container images via
   GoReleaser.

Pull requests run steps 1-4 but skip the release.

### Releases

Releases are automated. Every commit to `main` that passes e2e tests produces a
new patch release with:

- Multi-arch binaries (linux/darwin, amd64/arm64)
- Multi-arch container images on `ghcr.io/guzalv/stackrox-co-importer`
- SHA256 checksums
- An `install.sh` one-line installer (see [Quickstart](#quickstart))

No manual tagging or release action is needed.

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE) for details.

This is an unofficial community project, not affiliated with or endorsed by Red
Hat or the StackRox project. It is provided as-is, without warranty or support.

[co]: https://docs.openshift.com/container-platform/latest/security/compliance_operator/co-overview.html
[co-install]: https://docs.openshift.com/container-platform/latest/security/compliance_operator/co-install.html
[acs]: https://www.redhat.com/en/technologies/cloud-computing/openshift/advanced-cluster-security-kubernetes
[acs-install]: https://docs.openshift.com/acs/installing/install-ocp-operator.html
[godog]: https://github.com/cucumber/godog
[golangci]: https://golangci-lint.run/welcome/install-local/
