# 00 - Spec-Driven Development Process

## Purpose

Specs ARE tests. Gherkin `.feature` files in this directory are executed directly
by [Godog](https://github.com/cucumber/godog) via `go test`. Markdown `.md` specs
define contracts and acceptance criteria with testable requirement IDs (`IMP-*`).

## Core principles

- **Specs are executable:** `.feature` files run as Go tests via Godog. A scenario
  that passes means the behavior is implemented and verified.
- **Single source of truth:** these specs replace ad-hoc task notes; step definitions
  and production code trace back to them.
- **Behavior over implementation:** specs describe externally observable outcomes,
  not internal algorithms.
- **Contract-first boundaries:** external interfaces (CLI, ACS API payload shape,
  report output) are specified explicitly in markdown with `IMP-*` IDs.
- **Low brittleness assertions:** tests assert fields that matter to consumers,
  avoid incidental details.

## Requirement key words

- `MUST`: mandatory behavior.
- `SHOULD`: strongly recommended unless justified deviation.
- `MAY`: optional.

## Traceability model

Every requirement gets an ID:
- `IMP-CLI-*` for CLI/config contract
- `IMP-MAP-*` for CO -> ACS mapping
- `IMP-IDEM-*` for idempotency/conflicts
- `IMP-ERR-*` for errors/retries/reporting
- `IMP-ACC-*` for acceptance/runtime checks
- `IMP-ADOPT-*` for SSB adoption workflow
- `IMP-IMG-*` for container image packaging

Step definitions and Go tests MUST annotate requirement IDs in comments or test names.

## How specs become tests

### Gherkin features (`.feature` files)

1. Write scenarios in `specs/*.feature`.
2. Step definitions live in `features/*_test.go`.
3. `go test ./features/...` runs all scenarios via Godog.
4. Undefined steps are reported as pending — this is the starting state.
5. AI agents implement step definitions and production code to turn them green.

### Markdown contract specs (`.md` files)

1. Specs define `IMP-*` requirements with concrete expected behavior.
2. Go tests in `internal/*/` reference these IDs in test names: `TestIMP_CLI_001_*`.
3. `hack/check-spec-coverage.sh` enforces every `IMP-*` ID appears in at least one test.

## AI agent workflow

### Forward path (implementing a new scenario)

```
          ┌─────────────────┐
          │  Pick pending    │
          │  scenario        │
          └────────┬─────────┘
                   │
          ┌────────▼─────────┐
          │  go test (RED)   │   ← steps pending / undefined
          └────────┬─────────┘
                   │
          ┌────────▼─────────┐
          │  Implement step  │   ← AI agent writes step defs +
          │  definitions +   │     production code
          │  production code │
          └────────┬─────────┘
                   │
          ┌────────▼─────────┐
          │  go test (GREEN) │   ← scenarios pass
          └────────┬─────────┘
                   │
          ┌────────▼─────────┐
          │  Refactor        │   ← simplify, no behavior change
          └──────────────────┘
```

### Corrective path (when validation or feedback reveals a bug)

**Never change production code unless a test is failing that demands the
change.** If all tests are green but behavior is wrong, the tests have a gap.

```
          ┌──────────────────┐
          │  Bug / validation│
          │  failure found   │
          └────────┬─────────┘
                   │
          ┌────────▼─────────┐
          │  Diagnose: spec  │
          │  wrong, or impl? │
          └───┬──────────┬───┘
              │          │
     spec wrong    spec right, impl wrong
              │          │
   ┌──────────▼──┐  ┌────▼──────────────┐
   │ Update spec │  │ Tests green but   │
   │ (discuss    │  │ behavior wrong?   │
   │  first)     │  │ Strengthen test   │
   └──────┬──────┘  │ until RED         │
          │         └────┬──────────────┘
          │              │
          └──────┬───────┘
                 │
        ┌────────▼─────────┐
        │  go test (RED)   │   ← test now captures the real problem
        └────────┬─────────┘
                 │
        ┌────────▼─────────┐
        │  Fix production  │
        │  code            │
        └────────┬─────────┘
                 │
        ┌────────▼─────────┐
        │  go test (GREEN) │
        └────────┬─────────┘
                 │
        ┌────────▼─────────┐
        │  Re-validate     │
        └──────────────────┘
```

## Spec execution levels

### Unit-level (Godog + table-driven tests)
- Parsing/validation (flags, env, config file).
- Mapping translation (CO objects -> ACS payload).
- Diff/idempotency logic.
- Retry classification.

### Integration-level (Godog scenarios)
- Kubernetes read path for CO resources.
- ACS API client interactions (GET/POST/PUT).
- Dry-run no-write guarantees.

### Acceptance-level (e2e against real cluster)
- End-to-end execution against real cluster and ACS endpoint.
- Idempotency second-run no-op behavior.

## Quality gates

Before merging implementation:

1. `MUST` requirements implemented.
2. All Godog scenarios pass.
3. `hack/check-spec-coverage.sh` passes.
4. Dry-run validated as side-effect free.
5. Real-cluster acceptance checks pass.
6. No product runtime code path changes in Sensor/Central.
