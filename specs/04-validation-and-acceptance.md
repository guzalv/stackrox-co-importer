# 04 - Validation and Acceptance Spec

This document is the acceptance test contract for real-cluster validation.

## Preconditions

- `kubectl`, `curl`, `jq` installed.
- Logged into target cluster with Compliance Operator installed.
- Central endpoint reachable from runner.
- Importer binary built locally.
- RBAC: test runner MUST be able to create and delete ScanSettingBindings and
  ScanSettings in the test namespace.

Set environment:

```bash
export ROX_ENDPOINT="https://central.stackrox.example.com:443"
export ROX_API_TOKEN="<token>"
export ROX_ADMIN_USER="admin"
export ROX_ADMIN_PASSWORD="<password>"
export CO_NAMESPACE="openshift-compliance"
export IMPORTER_BIN="./bin/co-acs-scan-importer"
# For multi-cluster: merge kubeconfigs
export KUBECONFIG="~/.kube/config:~/.kube/config-secured-cluster"
```

## Test isolation rules

Tests MUST be self-contained and safe for parallel execution:

- **IMP-ACC-018**: each test MUST create its own CO resources (ScanSettingBindings,
  ScanSettings) at setup and delete them at teardown. Tests MUST NOT depend on
  pre-existing CO resources on the cluster.
- **IMP-ACC-019**: resource names MUST include a unique test-run identifier
  (e.g., a short random suffix or UUID fragment) so that concurrent test runs
  do not collide. Example: `e2e-cis-<run-id>`.
- **IMP-ACC-020**: each test MUST clean up all resources it creates — both CO
  resources (ScanSettingBindings, ScanSettings) and ACS scan configurations —
  via `t.Cleanup` or equivalent teardown, even on failure.
- **IMP-ACC-021**: tests MUST NOT depend on execution order. Each test sets up
  its own state independently.
- **IMP-ACC-022**: the importer MUST be invoked with `--co-namespace` pointing
  to the test namespace and `--context` limiting to the test context, so that
  it only processes resources created by that test.

## Acceptance checks

### A1 - CO resource discovery

- **IMP-ACC-001**: importer test run MUST begin only if required CO resource types are listable.

Commands:

```bash
kubectl get scansettingbindings.compliance.openshift.io -n "${CO_NAMESPACE}"
kubectl get scansettings.compliance.openshift.io -n "${CO_NAMESPACE}"
kubectl get profiles.compliance.openshift.io -n "${CO_NAMESPACE}"
kubectl get tailoredprofiles.compliance.openshift.io -n "${CO_NAMESPACE}" || true
```

Pass condition:

- first 3 commands succeed (exit 0).

### A2 - ACS auth preflight

- **IMP-ACC-002**: token and endpoint MUST pass read probe.
- **IMP-ACC-013**: optional basic-auth mode MUST pass read probe in local/dev environments.

Command:

```bash
curl -ksS \
  -H "Authorization: Bearer ${ROX_API_TOKEN}" \
  "${ROX_ENDPOINT}/v2/compliance/scan/configurations?pagination.limit=1" | jq .
```

Pass condition:

- command returns valid JSON and does not contain auth error.

Optional local/dev basic-auth probe:

```bash
curl -ksS \
  -u "${ROX_ADMIN_USER}:${ROX_ADMIN_PASSWORD}" \
  "${ROX_ENDPOINT}/v2/compliance/scan/configurations?pagination.limit=1" | jq .
```

### A3 - Dry-run side-effect safety

- **IMP-ACC-003**: dry-run MUST produce no writes.

Setup:

1. Create a ScanSetting `e2e-ss-<run-id>` with schedule `0 2 * * *`.
2. Create a ScanSettingBinding `e2e-dryrun-<run-id>` referencing the ScanSetting
   and at least one profile.

Command:

```bash
"${IMPORTER_BIN}" \
  --endpoint "${ROX_ENDPOINT}" \
  --co-namespace "${CO_NAMESPACE}" \
  --dry-run \
  --report-json "/tmp/co-acs-import-dryrun.json"
```

Pass conditions:

- exit code is `0` or `2`,
- report file exists and is valid JSON,
- report shows planned actions only (no `acsScanConfigId` on create items),
- no new ACS scan configs appear (before/after snapshot),
- `problems[]` is present and contains `description` + `fixHint` for each problematic resource.

Teardown: delete the ScanSettingBinding and ScanSetting.

### A4 - Apply creates expected configs

- **IMP-ACC-004**: apply mode MUST create missing target ACS configs.

Setup:

1. Create a ScanSetting `e2e-ss-<run-id>` with schedule `0 2 * * *`.
2. Create a ScanSettingBinding `e2e-apply-<run-id>` referencing the ScanSetting
   and profile `ocp4-cis` (or any profile known to exist on the cluster).

Command:

```bash
"${IMPORTER_BIN}" \
  --endpoint "${ROX_ENDPOINT}" \
  --co-namespace "${CO_NAMESPACE}" \
  --report-json "/tmp/co-acs-import-apply.json"
```

Pass conditions:

- exit code is `0` or `2`,
- report shows `action=create` for `e2e-apply-<run-id>`,
- ACS API scan config list includes a config with `scanName=e2e-apply-<run-id>`.

Teardown: delete the ACS scan config, ScanSettingBinding, and ScanSetting.

### A5 - Idempotency on second run

- **IMP-ACC-005**: second run with same inputs MUST be no-op.

Setup:

1. Create CO resources as in A4 (`e2e-idem-<run-id>`).
2. Run importer in apply mode (first run).

Command (second run):

```bash
"${IMPORTER_BIN}" \
  --endpoint "${ROX_ENDPOINT}" \
  --co-namespace "${CO_NAMESPACE}" \
  --report-json "/tmp/co-acs-import-second-run.json"
```

Pass conditions:

- report shows `action=skip` for `e2e-idem-<run-id>`,
- `counts.create` is 0 on second run,
- no net changes in ACS config list between first and second run.

Teardown: delete ACS scan config, ScanSettingBinding, and ScanSetting.

### A6 - Existing config behavior

- **IMP-ACC-006**: without `--overwrite-existing`, existing scan names MUST be skipped
  and recorded in `problems[]`.
- **IMP-ACC-014**: with `--overwrite-existing`, existing scan names MUST be updated via PUT.

Setup:

1. Create CO resources (`e2e-exist-<run-id>`).
2. Run importer in apply mode to create the ACS scan config.

Procedure (create-only / IMP-ACC-006):

1. Re-run importer without `--overwrite-existing`.
2. Verify that config is skipped and `problems[]` has category `conflict`.

Procedure (overwrite / IMP-ACC-014):

1. Re-run importer with `--overwrite-existing`.
2. Verify that report shows `action=update`, not `skip`.

Teardown: delete ACS scan config, ScanSettingBinding, and ScanSetting.

### A8 - Multi-cluster merge

- **IMP-ACC-015**: when the same SSB name exists on multiple source clusters with matching
  profiles and schedule, importer MUST create one scan config targeting all resolved cluster IDs.
- **IMP-ACC-016**: when the same SSB name exists on multiple source clusters with different
  profiles or schedule, importer MUST error for that SSB name.

### A9 - Auto-discovery

- **IMP-ACC-017**: importer MUST auto-discover the ACS cluster ID from the admission-control
  ConfigMap's `cluster-id` key.

Setup:

1. Create CO resources (`e2e-disco-<run-id>`).

Command:

```bash
"${IMPORTER_BIN}" \
  --endpoint "${ROX_ENDPOINT}" \
  --co-namespace "${CO_NAMESPACE}" \
  --dry-run \
  --report-json "/tmp/co-acs-import-disco.json"
```

Pass condition: report shows `discovered > 0` (importer resolved cluster ID
without explicit flag).

Teardown: delete ScanSettingBinding and ScanSetting.

### A7 - Failure paths

- **IMP-ACC-007**: invalid token MUST fail-fast with exit code `1`.
  No CO resource setup needed — this tests preflight auth failure.
- **IMP-ACC-008**: missing referenced ScanSetting MUST fail only that binding
  (partial run exit code `2` when others succeed).

Setup for IMP-ACC-008:

1. Create a ScanSettingBinding `e2e-broken-<run-id>` referencing a ScanSetting
   `does-not-exist-<run-id>` (do NOT create the ScanSetting).
2. Create a valid ScanSettingBinding `e2e-valid-<run-id>` with its ScanSetting.

Pass condition: report shows `e2e-broken-<run-id>` failed, `e2e-valid-<run-id>`
processed, exit code `2`.

Teardown: delete all created resources.

- **IMP-ACC-009**: transient ACS failures MUST follow retry policy and record attempt counts.
- **IMP-ACC-012**: all per-resource problems MUST be emitted in `problems[]` with remediation hint.

### A10 - --list-ssbs discovery

- **IMP-ACC-023**: `--list-ssbs` MUST print `namespace/name` for each discovered SSB to stdout,
  sorted lexicographically, and exit 0.
- **IMP-ACC-024**: `--list-ssbs` MUST succeed without ACS credentials (no `ROX_API_TOKEN` or
  `ROX_ADMIN_PASSWORD` set).
- **IMP-ACC-025**: `--list-ssbs` combined with `--exclude` MUST exclude matching SSBs from output.

Command:

```bash
# List all SSBs (no ACS creds needed)
"${IMPORTER_BIN}" --list-ssbs

# List with exclusion
"${IMPORTER_BIN}" --list-ssbs --exclude "ocp4-.*"
```

Pass conditions:

- exit code is `0`,
- stdout contains `<namespace>/<name>` lines, one per SSB, sorted,
- with `--exclude`, matching SSBs are absent from output.

### A11 - --exclude filtering

- **IMP-ACC-026**: `--exclude <regex>` MUST skip SSBs whose names match the pattern, counting
  them as discovered but not processing them (not in `created`, `skipped`, or `failed` counts).
- **IMP-ACC-027**: multiple `--exclude` patterns MUST be OR-ed.

Setup:

1. Create two ScanSettingBindings: `e2e-include-<run-id>` and `e2e-exclude-<run-id>`.

Command:

```bash
"${IMPORTER_BIN}" \
  --endpoint "${ROX_ENDPOINT}" \
  --co-namespace "${CO_NAMESPACE}" \
  --dry-run \
  --exclude "e2e-exclude-.*" \
  --report-json "/tmp/co-acs-import-exclude.json"
```

Pass conditions:

- `e2e-include-<run-id>` appears in `items[]` with expected action,
- `e2e-exclude-<run-id>` does NOT appear in `items[]`,
- `counts.discovered` reflects total SSBs found before filtering.

Teardown: delete both ScanSettingBindings and their ScanSettings.

## Non-goal compliance checks

- **IMP-ACC-010**: no code changes in Sensor/Central runtime paths are required to run importer.
- **IMP-ACC-011**: importer MUST not mutate Compliance Operator resources.
