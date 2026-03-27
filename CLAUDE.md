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

### Rules for AI agents

- **Never modify `.feature` files** unless the spec itself is wrong (discuss first).
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
