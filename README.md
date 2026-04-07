# Compliance Operator to ACS Importer

A CLI tool that reads [Compliance Operator][co] scheduled-scan resources from
OpenShift clusters and creates equivalent scan configurations in
[Red Hat Advanced Cluster Security (ACS / StackRox)][acs].

## Why

Organizations running OpenShift with the Compliance Operator already have
scheduled compliance scans defined as Kubernetes resources
(`ScanSettingBindings`, `ScanSettings`, `Profiles`). When they adopt ACS for
centralized security management, they need to recreate those scan definitions
manually in ACS Central -- a tedious, error-prone process that scales poorly
across clusters.

This tool automates that migration. Point it at your clusters and your ACS
Central instance, and it will discover the existing Compliance Operator
resources, map them to ACS scan configurations, and create (or update) them
through the ACS API. It supports dry-run previews, multi-cluster merging,
idempotent re-runs, and structured JSON reporting.

## Features

- Discovers `ScanSettingBindings`, `ScanSettings`, and `Profiles` from one or
  more clusters via kubeconfig.
- Auto-discovers the ACS cluster ID from the secured-cluster admission-control
  ConfigMap (no manual cluster-ID mapping needed).
- Maps CO schedules, profiles, and bindings to ACS v2 scan configuration
  payloads.
- Merges identical bindings from multiple clusters into a single ACS scan
  config targeting all clusters.
- Create-only mode (default) skips existing scan configs; `--overwrite-existing`
  updates them in place.
- `--dry-run` previews all actions without writing to ACS.
- Structured JSON report (`--report-json`) for automation and audit.
- `--exclude <regex>` to skip specific bindings by name pattern (repeatable, Go regex).
- `--list-ssbs` to print discovered binding names without importing (no ACS credentials required).
- Retries transient ACS API failures with configurable limits.
- Static binary, no runtime dependencies. Runs as a container image on
  `amd64` and `arm64`.

## Prerequisites

### ACS / StackRox

You need a running ACS Central instance (4.x or later) accessible over HTTPS.

Install ACS Central and Secured Cluster by following the
[official documentation][acs-install]. The Secured Cluster must be registered
with Central so that the importer can auto-discover cluster IDs.

You also need one of these credential sets for the importer to authenticate
with Central:

- **API token** (recommended): generate one from the ACS UI under
  _Platform Configuration > Integrations > Authentication Tokens_.
  The token needs the `Compliance` permission.
- **Basic auth**: use the `admin` user and password. Suitable for
  development/testing but not recommended for production.

### Compliance Operator

The Compliance Operator must be installed on each OpenShift cluster whose scan
definitions you want to import. Install it from OperatorHub or follow the
[Compliance Operator documentation][co-install].

You need at least one `ScanSettingBinding` referencing a `ScanSetting` and one
or more `Profiles` or `TailoredProfiles`. The importer reads these resources
but never modifies them.

### Local tools

- Go 1.23+ (to build from source)
- `kubectl` or `oc` configured with kubeconfigs for your target clusters
- `docker` or `podman` (only if building/running the container image)

## Installation

### From source

```bash
git clone https://github.com/guzalv/stackrox-co-importer.git
cd stackrox-co-importer
make build
# Binary is at ./bin/compliance-operator-importer
```

### Container image

```bash
# Build for your local architecture
make image

# Or pull a pre-built image (when available)
docker pull ghcr.io/guzalv/stackrox-co-importer:latest
```

## Usage

### Quick start

```bash
# Set credentials
export ROX_ENDPOINT="https://central.example.com:443"
export ROX_API_TOKEN="your-token-here"

# Preview what would be imported (no changes made)
./bin/compliance-operator-importer --dry-run

# Import scan configurations
./bin/compliance-operator-importer
```

### Authentication

The importer infers the authentication mode from environment variables:

| Variable             | Auth mode | Notes                                 |
|----------------------|-----------|---------------------------------------|
| `ROX_API_TOKEN`      | Token     | Recommended. Set the token value.     |
| `ROX_ADMIN_PASSWORD` | Basic     | Used with `ROX_ADMIN_USER` (default: `admin`). |

Setting both `ROX_API_TOKEN` and `ROX_ADMIN_PASSWORD` is an error (ambiguous).

### Flags

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

### Exit codes

| Code | Meaning                                        |
|------|------------------------------------------------|
| `0`  | All bindings processed successfully.           |
| `1`  | Fatal configuration or preflight error.        |
| `2`  | Partial success (some bindings failed).        |

### Multi-cluster usage

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
matching profiles and schedule, the importer merges them into a single ACS
scan configuration targeting all clusters.

### Selective import with --exclude

Skip specific bindings by name using Go regular expressions. Multiple patterns
are OR-ed — a binding is excluded if any pattern matches its name.

```bash
# Skip a specific binding by exact name
./bin/compliance-operator-importer --exclude "ocp4-cis-node"

# Skip all ocp4-* bindings
./bin/compliance-operator-importer --exclude "ocp4-.*"

# Combine multiple patterns
./bin/compliance-operator-importer --exclude "ocp4-cis-node" --exclude "rhcos4-.*"
```

### Discover bindings without importing (--list-ssbs)

Print all discovered `ScanSettingBindings` (`namespace/name`, sorted) without
contacting ACS. Useful for understanding what the importer would process, and
ACS credentials are not required.

```bash
# List all discovered SSBs
./bin/compliance-operator-importer --list-ssbs

# List with --exclude filtering applied
./bin/compliance-operator-importer --list-ssbs --exclude "ocp4-.*"
```

### JSON report

Use `--report-json` to produce a machine-readable report:

```bash
./bin/compliance-operator-importer --report-json report.json
```

The report includes metadata, counts (discovered/created/skipped/failed), per-
binding details, and a `problems` array with descriptions and fix hints for
any issues encountered.

## Development

### First-time setup

After cloning, activate the pre-commit hooks (runs lint + unit tests before
every commit):

```bash
make setup
```

This is a one-time step per clone. Without it, lint and test errors will only
be caught by CI after the push.

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

The project follows spec-driven development: `.feature` files in `specs/` are
executable via [Godog][godog] and serve as both specification and test suite.

### CI pipeline

Every push to `main` runs the following pipeline:

1. **Lint** -- golangci-lint with a curated set of linters.
2. **Test** -- Godog scenarios and unit tests.
3. **Build** -- binary compilation and container image build.
4. **E2E** -- spins up a kind cluster with StackRox (lightweight mode) and
   fake Compliance Operator CRDs, then runs acceptance tests against real
   APIs.
5. **Release** -- if e2e passes on `main`, auto-increments the patch version,
   creates a Git tag, and publishes multi-arch binaries and container images
   via GoReleaser.

Pull requests run steps 1-4 but skip the release.

### Releases

Releases are fully automated. Every commit to `main` that passes e2e tests
produces a new patch release with:

- Multi-arch binaries (linux/darwin, amd64/arm64)
- Multi-arch container images on `ghcr.io/guzalv/co-acs-importer`
- SHA256 checksums

No manual tagging or release action is needed.

## License

See [LICENSE](LICENSE) for details.

[co]: https://docs.openshift.com/container-platform/latest/security/compliance_operator/co-overview.html
[co-install]: https://docs.openshift.com/container-platform/latest/security/compliance_operator/co-install.html
[acs]: https://www.redhat.com/en/technologies/cloud-computing/openshift/advanced-cluster-security-kubernetes
[acs-install]: https://docs.openshift.com/acs/installing/install-ocp-operator.html
[godog]: https://github.com/cucumber/godog
[golangci]: https://golangci-lint.run/welcome/install-local/
