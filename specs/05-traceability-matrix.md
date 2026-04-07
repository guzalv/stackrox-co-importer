# 05 - Traceability Matrix

Use this matrix to ensure complete implementation coverage.

|Requirement ID|Spec source|Test level|Notes|
|---|---|---|---|
|IMP-CLI-001..029|`01-cli-and-config-contract.md`|Table-driven Go tests in `internal/config/`|CLI parsing, preflight, auth modes, multi-cluster, --overwrite-existing, --exclude, --list-ssbs|
|IMP-MAP-001..021, IMP-MAP-020a|`02-co-to-acs-mapping.feature`|Godog scenarios in `features/`|Mapping, schedule, cluster auto-discovery, SSB merging, merge conflict console output|
|IMP-ADOPT-001..008|`02-co-to-acs-mapping.feature`|Godog scenarios in `features/`|SSB adoption workflow after ACS scan config creation|
|IMP-IDEM-001..009|`03-idempotency-dry-run-retries.feature`|Godog scenarios in `features/`|Idempotency, overwrite mode (PUT), dry-run reporting|
|IMP-ERR-001..004|`03-idempotency-dry-run-retries.feature`|Godog scenarios in `features/`|Retry classes, skip-on-error behavior, exit code outcomes|
|IMP-ACC-001..017|`04-validation-and-acceptance.md`|e2e tests in `e2e/`|Real cluster, ACS verification, multi-cluster merge, auto-discovery|
|IMP-IMG-001..006|`07-container-image.md`|Build + smoke|Dockerfile, static binary, multi-arch manifest, image size|
|IMP-CLI-028..029 (runtime)|`06-exclude-and-list-ssbs.feature`|Godog scenarios in `features/`|--exclude filtering, --list-ssbs output|

## Coverage rule

`hack/check-spec-coverage.sh` enforces that each requirement ID appears in at least
one of: Godog step definition comment, Go test name, or test comment.

For each requirement ID:
- at least one test/step definition containing that ID, and
- `go test` must pass for the scenario covering that ID.
