# 05 - Traceability Matrix

Use this matrix to ensure complete implementation coverage.

|Requirement ID|Spec source|Test level|Notes|
|---|---|---|---|
|IMP-CLI-001..034|`01-cli-and-config-contract.md`|Table-driven Go tests in `internal/config/`|CLI parsing, preflight, auth modes, multi-cluster, --overwrite-existing, --exclude, --list-ssbs, --delete-existing/--yes/--backup-dir/--settle-timeout|
|IMP-MAP-001..021, IMP-MAP-020a|`02-co-to-acs-mapping.feature`|Godog scenarios in `features/`|Mapping, schedule, cluster auto-discovery, SSB merging, merge conflict console output|
|IMP-MAP-017a|`02-co-to-acs-mapping.feature`|Go unit test in `internal/acs/`|ACS /v1/clusters wire format: providerMetadata nested under status|
|IMP-MAP-018a|`02-co-to-acs-mapping.feature`|Go unit test in `internal/cofetch/`|helm Secret .data is base64 via dynamic client; decode + trim; ConfigMap .data is plain text|
|IMP-IDEM-001..009|`03-idempotency-dry-run-retries.feature`|Godog scenarios in `features/`|Idempotency, overwrite mode (PUT), dry-run reporting|
|IMP-ERR-001..004|`03-idempotency-dry-run-retries.feature`|Godog scenarios in `features/`|Retry classes, skip-on-error behavior, exit code outcomes|
|IMP-ACC-001..027|`04-validation-and-acceptance.md`|e2e tests in `e2e/`|Real cluster, ACS verification, multi-cluster merge, auto-discovery, --exclude, --list-ssbs|
|IMP-IMG-001..005|`07-container-image.md`|Build + smoke|Dockerfile, static binary, multi-arch manifest, image size|
|IMP-IMG-007|`07-container-image.md`|Go integration test in `internal/install/`|`install.sh` resolves the latest release via the redirect, downloads the OS/arch archive, verifies its SHA-256, extracts the binary; checksum mismatch aborts without writing a binary|
|IMP-CLI-028..029 (runtime)|`06-exclude-and-list-ssbs.feature`|Godog scenarios in `features/`|--exclude filtering, --list-ssbs output|
|IMP-CLI-020a|`01-cli-and-config-contract.md`|Go integration test in `internal/run/`|Collected problems + fix hints printed to the console, independent of --report-json|
|IMP-ADEXT-001..015|`08-adopt-external-ssb.feature`|Godog scenarios in `features/`|Detect/back up/remove externally-managed SSBs blocking ACS scan config create or update, settle-timeout self-heal guard, multi-cluster all-clear gate, resume, orphaned ScanSetting info note. Replaces the retired IMP-ADOPT-001..008 (patch-after-create, invalidated by the always-on external-SSB conflict check)|
|IMP-ADEXT-016|`08-adopt-external-ssb.feature`|Go integration test in `internal/run/`|Backup write failure (unwritable --backup-dir) fails the scan with a remediation hint, not a raw OS error|
|IMP-ADEXT-017|`08-adopt-external-ssb.feature`|Go unit test in `internal/adoptexternal/`|plan.json accumulates every backed-up scan (WriteBackup merges, does not overwrite), so re-run after manual deletion recreates all scans|
|IMP-ADEXT-018|`08-adopt-external-ssb.feature`|Go integration test in `internal/run/`|Confirmation is batched: multiple blocked scans produce a single delete-confirmation prompt, not one per scan|
|IMP-ADEXT-019|`08-adopt-external-ssb.feature`|Go integration test in `internal/run/`|Adopt-external delete/exists routed by unique ACS cluster ID, not kubecontext name, so colliding context names (e.g. both "admin") delete against the correct cluster|

## Coverage rule

`hack/check-spec-coverage.sh` enforces that each requirement ID appears in at least
one of: Godog step definition comment, Go test name, or test comment.

For each requirement ID:
- at least one test/step definition containing that ID, and
- `go test` must pass for the scenario covering that ID.
