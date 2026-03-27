# Compliance Operator to ACS Importer — Spec-Driven Development

## What this project does

Standalone CLI tool that reads Compliance Operator scheduled-scan resources from
Kubernetes clusters and creates equivalent ACS compliance scan configurations
through the ACS API.

## Spec-driven development workflow

**Specs ARE tests.** The `.feature` files in `specs/` are executable via
[Godog](https://github.com/cucumber/godog). Every scenario is a test case.

### The loop

1. **Pick a pending scenario** — run `make test` and look for `Pending` steps.
2. **Implement the step definition** — in `features/*_steps_test.go`, replace
   `return godog.ErrPending` with real logic.
3. **Write production code** — in `internal/`, create the minimal code needed
   to make the step pass.
4. **Run tests** — `make test`. The scenario should go from Pending to Passing.
5. **Repeat** for the next step/scenario.

### The cardinal rule

**Never change production code unless a test is failing (or pending) that
demands the change.** This applies to all situations — initial implementation,
bug fixes, and real-cluster validation failures alike. If all tests are green
but the code is wrong, the problem is in the specs or tests, not just the code.

### Rules for AI agents

- **Specs are the source of truth.** Production code implements specs, not the
  other way around. If code and spec disagree, the spec wins until explicitly
  changed.
- **One scenario at a time.** Don't batch-implement multiple scenarios — the
  test feedback loop is the safety net.
- **Step definitions are glue code.** They set up state, call production code
  in `internal/`, and assert results. Keep them thin.
- **Production code goes in `internal/`.** Step definitions import from there.
- **Traceability:** when implementing a step, add a comment with the `IMP-*` ID
  from the feature file (e.g., `// IMP-MAP-001`).
- **Table-driven Go tests** for unit-level specs that don't have Gherkin scenarios
  (e.g., CLI parsing from `01-cli-and-config-contract.md`). Place these in
  `internal/<pkg>/*_test.go` and name them `TestIMP_CLI_001_*`.
- **Don't add features not in the specs.** If you think something is missing,
  flag it — don't implement it speculatively.

### When something breaks: the corrective flow

When real-cluster validation, a bug report, or any other feedback reveals that
the code is wrong, **do not jump straight to fixing the code.** Follow this
flow:

1. **Diagnose:** is the spec wrong, or the implementation?
2. **If the spec is wrong or incomplete:** update the `.feature` file or
   markdown spec first. Discuss with the user before changing specs. Run tests
   — they should now fail (red). Then fix code to make them green.
3. **If the spec is right but the implementation is wrong:** run the existing
   tests. They should already be failing. If they are green despite wrong
   behavior, the tests are too weak — **strengthen the test first** (add
   assertions, tighten scenarios) so it goes red, then fix code.
4. **Never fix code while all tests are green.** A green suite with wrong
   runtime behavior means the test is not covering the real problem. Fix the
   test gap first.

### Real-cluster validation checkpoints

Godog scenarios test logic with fakes. But fakes can mask real problems: wrong API
paths, TLS issues, unexpected K8s RBAC, payload shape mismatches. To catch these
early, AI agents MUST validate against a real cluster at these checkpoints:

| After completing...                          | Run against real cluster            |
|----------------------------------------------|-------------------------------------|
| ACS client (`internal/acs/`)                 | Preflight probe: can you auth and list scan configs? |
| K8s CO fetcher (`internal/cofetch/`)         | Can you list SSBs, ScanSettings, Profiles from the real cluster? |
| Cluster ID discovery (`internal/discover/`)  | Does auto-discovery resolve the correct ACS cluster ID? |
| First full mapping pipeline                  | `--dry-run`: does the output make sense for real CO resources? |
| Reconcile/create path (`internal/reconcile/`)| Apply mode: does the created ACS scan config appear in Central? |
| Idempotency                                  | Second run: does it skip with no errors? |
| Adoption workflow                            | Does SSB patching work against the real cluster? |

**How to validate:** build the binary (`make build`) and run it against the test
cluster. Compare output/report against what you see in the ACS API and kubectl.
Don't just check exit code — inspect the created resources.

**When validation fails:** follow the corrective flow above. Do not fix code
directly — identify whether the spec or the test is the gap, fix top-down
(spec → test → code), and re-validate.

**Minimum rule:** never consider a feature area "done" without at least one
successful run against the real cluster. If the cluster is unreachable, flag it
as a blocker — don't silently skip.

**Test cluster environment:**

```bash
export ROX_ENDPOINT="https://central-stackrox.apps.ga-ocp4-cron.ocp.infra.rox.systems"
export ROX_ADMIN_USER="admin"
export ROX_ADMIN_PASSWORD="admin"
export CO_NAMESPACE="openshift-compliance"
export KUBECONFIG="$HOME/.kube/config:$HOME/.kube/config-secured-cluster"
```

See `specs/04-validation-and-acceptance.md` for the full acceptance check procedure.

## Project structure

```
specs/                          # Source of truth
  00-spec-process.md            # How SDD works in this project
  01-cli-and-config-contract.md # CLI contract (IMP-CLI-* IDs)
  02-co-to-acs-mapping.feature  # Executable: mapping scenarios
  03-idempotency-dry-run-retries.feature  # Executable: idempotency scenarios
  04-validation-and-acceptance.md  # Acceptance criteria (IMP-ACC-* IDs)
  05-traceability-matrix.md     # Cross-reference of all IMP-* IDs
  07-container-image.md         # Container packaging (IMP-IMG-* IDs)

features/                       # Godog step definitions (test-only package)
  features_test.go              # TestFeatures — Godog entry point
  scenario_test.go              # InitializeScenario — wires all steps
  mapping_steps_test.go         # Steps for 02-co-to-acs-mapping.feature
  idempotency_steps_test.go     # Steps for 03-idempotency-dry-run-retries.feature

internal/                       # Production code (starts empty)
  # Packages created as needed when implementing steps

cmd/importer/                   # CLI entry point
  main.go

e2e/                            # Acceptance tests against real clusters
hack/                           # Helper scripts
```

## Commands

```bash
make test               # Run all Godog scenarios + Go tests
make test-verbose       # Same, with step-by-step output
make test-e2e           # Run acceptance tests against real cluster (needs env vars)
make smoke              # Build + dry-run against real cluster (quick validation)
make spec-coverage      # Check all IMP-* IDs appear in tests
make build              # Build the importer binary
make lint               # Run golangci-lint (if configured)
```

## Spec files reference

| Spec | Format | IDs | How tested |
|------|--------|-----|------------|
| `01-cli-and-config-contract.md` | Markdown | IMP-CLI-* | Table-driven Go tests in `internal/config/` |
| `02-co-to-acs-mapping.feature` | Gherkin | IMP-MAP-*, IMP-ADOPT-* | Godog scenarios in `features/` |
| `03-idempotency-dry-run-retries.feature` | Gherkin | IMP-IDEM-*, IMP-ERR-* | Godog scenarios in `features/` |
| `04-validation-and-acceptance.md` | Markdown | IMP-ACC-* | e2e tests in `e2e/` |
| `07-container-image.md` | Markdown | IMP-IMG-* | Build + smoke tests |

## Dependencies

- Go 1.23+
- [Godog](https://github.com/cucumber/godog) — Gherkin test runner
- No external test frameworks beyond `testing` + Godog
