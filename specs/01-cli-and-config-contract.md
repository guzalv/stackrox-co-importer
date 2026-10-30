# 01 - CLI and Config Contract

## Goal

Define the importer interface so it can be implemented and tested predictably.

## Inputs contract

### Required inputs

- **IMP-CLI-001**: importer MUST accept Central endpoint (`--endpoint` or `ROX_ENDPOINT`).
  - if the value does not contain a scheme, importer MUST prepend `https://`.
  - if the value starts with `http://`, importer MUST error.
- **IMP-CLI-002**: importer MUST support auth modes, auto-inferred from available credentials
  (no explicit `--auth-mode` flag, no env-var-name indirection):
  - token mode: when `ROX_API_TOKEN` is set,
  - basic mode: when `ROX_ADMIN_PASSWORD` is set,
  - if both are set: token mode is used; a warning is emitted to stderr,
  - if neither is set: error with help text listing both options.
- **IMP-CLI-003**: importer MUST load each kubeconfig file independently (no merging):
  - file discovery follows standard kubectl rules: `KUBECONFIG` env var (colon-separated
    paths) or `~/.kube/config`.
  - each file in the `KUBECONFIG` path is loaded in isolation. Contexts, users, and
    clusters defined in one file never interact with entries in another file. This
    prevents credential collisions when multiple files define the same user name
    (e.g. `admin`) with different certificates.
  - by default, the importer iterates **all contexts** across all files, treating
    each context as a separate source cluster.
  - when the same context name appears in multiple files, both are processed
    independently with their own credentials.
  - **IMP-CLI-003a**: when a report or console message identifies a source cluster
    (e.g. a cross-cluster conflict message, IMP-MAP-020a), it MUST identify each
    side unambiguously even when two sources share the same context name. The
    bare context name alone is NOT sufficient. The message MUST include a
    distinguishing identifier — the originating kubeconfig file path, the resolved
    ACS cluster ID, or both — so the two sides are distinguishable.
  - `--context <name>` (repeatable, optional): filters which contexts to use. Matches
    against context names across all files. When given, only matching contexts are
    processed; all others are skipped.
  - for each context, the ACS cluster ID is auto-discovered (see IMP-MAP-016..018).
- **IMP-CLI-004**: importer MUST support namespace scope:
  - `--co-namespace <ns>` (default `openshift-compliance`) for single namespace, or
  - `--co-all-namespaces` for cluster-wide scan.

### Optional inputs

- **IMP-CLI-006**: importer default mode is create-only; `--overwrite-existing` enables update mode.
- **IMP-CLI-007**: `--dry-run` MUST disable all ACS write operations.
- **IMP-CLI-008**: `--report-json <path>` for structured report output.
- **IMP-CLI-009**: `--request-timeout <duration>` default `30s`.
- **IMP-CLI-010**: `--max-retries <int>` default `5`, min `0`.
- **IMP-CLI-011**: `--ca-cert-file <path>` optional.
- **IMP-CLI-012**: `--insecure-skip-verify` default false; MUST require explicit flag.
- **IMP-CLI-028**: `--exclude <regex>` (repeatable, optional): excludes SSBs whose names match any
  of the given Go regular expressions.
  - patterns are compiled at startup; an invalid regex MUST cause a config parse error.
  - multiple patterns are OR-ed: an SSB is excluded if its name matches any one pattern.
  - filtering applies after SSBs are fetched and before any ACS operations.
  - filtering also applies when `--list-ssbs` is used.
- **IMP-CLI-029**: `--list-ssbs` (boolean, default false): when set, importer fetches SSBs from all
  configured cluster sources, prints `<namespace>/<name>` for each discovered SSB to stdout (one per
  line, sorted), then exits with code 0.
  - `--exclude` filtering applies to the list.
  - ACS preflight is skipped (no ACS credentials required).
  - `--endpoint`/`ROX_ENDPOINT` is NOT required: `--list-ssbs` never contacts ACS, so
    config parsing MUST succeed with no endpoint set.
  - `--dry-run` has no additional effect (the flag is implicitly read-only).
- **IMP-CLI-024**: for basic mode:
  - username is read from `--username` flag or `ROX_ADMIN_USER` env var (default `admin`).
  - password is read from `ROX_ADMIN_PASSWORD` env var (no flag).
- **IMP-CLI-025**: importer MUST handle overlapping auth config:
  - both `ROX_API_TOKEN` and `ROX_ADMIN_PASSWORD` are set → use token mode,
    emit a warning to stderr (e.g. "both ROX_API_TOKEN and ROX_ADMIN_PASSWORD are set; using token auth"),
  - neither is set → error with help text.
- **IMP-CLI-027**: `--overwrite-existing` (default `false`):
  - when `false`: existing ACS scan configs with matching `scanName` are skipped (create-only).
  - when `true`: existing ACS scan configs with matching `scanName` are updated via
    `PUT /v2/compliance/scan/configurations/{id}`.
- **IMP-CLI-035**: `--help`/`-h` MUST print the usage/flag listing and exit with code `0`.
  It MUST NOT be treated as a config error (no `error:` line, not exit `1`), since asking
  for help is a successful, intended invocation.

### Adopting externally-managed ScanSettingBindings

RHACS >=4.11/5.0 Central rejects creating (or updating) a scan configuration whose profiles
overlap an existing ScanSettingBinding on the target cluster that is NOT labeled
`app.kubernetes.io/name: stackrox` (i.e. an "external" SSB — typically GitOps-managed). Since
the importer's payloads are built from exactly such external SSBs, this blocks every create by
construction. The importer MUST detect this and guide the operator through safely removing the
external SSB, rather than surfacing ACS's raw error. Full runtime behavior is specified in
`08-adopt-external-ssb.feature` (`IMP-ADEXT-*`); this section defines the flag contract.

- **IMP-CLI-030**: `--delete-existing` (boolean, default `false`): when set, the importer
  deletes blocking external ScanSettingBindings itself (after confirmation), instead of the
  default behavior of reporting the conflict and skipping.
- **IMP-CLI-031**: `--yes` / `-y` (boolean, default `false`): skips the interactive
  confirmation prompt that `--delete-existing` would otherwise show. Has no effect when
  `--delete-existing` is not set.
- **IMP-CLI-032**: `--backup-dir <path>` (default: `./co-importer-backup-<RFC3339 timestamp>`):
  directory where backups of resources the importer deletes, and a resumable plan
  (`plan.json`), are written. Before writing, the importer MUST verify the directory is
  either new/empty or contains a `plan.json` from a prior run (see IMP-ADEXT-011/012/015);
  otherwise it MUST fail rather than write into an unrelated directory.
- **IMP-CLI-033**: `--settle-timeout <duration>` (default `5s`): after a blocking external SSB
  is observed gone, the importer re-polls for this duration to detect GitOps self-healing
  before proceeding to create the scan configuration.
- **IMP-CLI-034**: when `--delete-existing` is set, `--yes` is not set, and stdin is not a TTY,
  importer MUST fail at startup with an error instructing the operator to pass `--yes`
  (an interactive confirmation prompt cannot be shown non-interactively).

## Preflight checks

- **IMP-CLI-013**: `--endpoint` MUST use HTTPS:
  - bare hostname/port (no scheme) → `https://` is prepended automatically,
  - `https://...` → accepted as-is,
  - `http://...` → error.
- **IMP-CLI-014**: auth material for inferred mode MUST be non-empty:
  - token mode: `ROX_API_TOKEN` is non-empty,
  - basic mode: `ROX_ADMIN_PASSWORD` is non-empty (username defaults to `admin`).
- **IMP-CLI-015**: importer MUST probe ACS auth with:
  - `GET /v2/compliance/scan/configurations?pagination.limit=1`
  - using selected auth mode,
  - success only on HTTP 200.
- **IMP-CLI-016**: HTTP 401/403 at preflight MUST fail-fast with remediation message.
- **IMP-CLI-016a**: TLS certificate verification failures at preflight MUST hint at
  `--ca-cert-file` and `--insecure-skip-verify` (not generic network connectivity),
  since the most common cause is a self-signed or internal CA certificate.

## Output contract

### Exit codes

- **IMP-CLI-017**: `0` when run completed with no failed bindings.
- **IMP-CLI-018**: `1` for fatal preflight/config errors (no import attempted).
- **IMP-CLI-019**: `2` for partial success (some bindings failed), **or** when any `problems[]`
  entry has category `gitops` (a requested import did not happen because it was blocked by an
  external ScanSettingBinding — whether reported by default, declined at the confirmation
  prompt, or blocked again by GitOps self-heal). A `gitops`-blocked scan is a routine report
  condition (not a fatal error), but it means the requested import was not completed, so it
  MUST NOT be indistinguishable from a fully successful run via exit code alone.

### Console summary

- **IMP-CLI-020**: print totals:
  - bindings discovered
  - creates/skips/failures
  - dry-run indicator

### JSON report shape

- **IMP-CLI-021**: when `--report-json` is set, write valid JSON with:
  - `meta` (timestamp, dryRun, namespaceScope, mode=`create-only` | `create-or-update`,
    `backupDir` — set whenever a blocking external SSB was found, empty otherwise)
  - `counts` (discovered, create, skip, failed)
  - `items[]`:
    - `source` (`namespace`, `bindingName`, `scanSettingName`)
    - `action` (`create|skip|fail|update`)
    - `reason`
    - `attempts`
    - `acsScanConfigId` (if known)
    - `error` (if failed)
  - `problems[]`:
    - `severity` (`error|warning`)
    - `category` (`input|mapping|conflict|auth|api|retry|validation|gitops|orphan`)
      - `gitops`: a blocking external SSB was found, reappeared during the settle window, or
        otherwise prevented a create/update (IMP-ADEXT-*)
      - `orphan`: a ScanSetting previously referenced by a deleted external SSB was left behind
        (IMP-ADEXT-010)
    - `resourceRef` (`namespace/name` or synthetic ref for non-resource errors)
    - `description` (what happened)
    - `fixHint` (how to fix)
    - `skipped` (boolean; true when resource was skipped)

- **IMP-CLI-022**: whenever any problem occurs for a resource, importer MUST:
  - skip that resource,
  - append one `problems[]` entry with `description` and `fixHint`,
  - continue processing other resources.

## Existing ACS config behavior

- **IMP-IDEM-001**: when `--overwrite-existing` is `false` (default) and `scanName` already exists
  in ACS, importer MUST skip that source resource.
- **IMP-IDEM-002**: skipped-existing resources MUST be added to `problems[]` with category `conflict`
  and a fix hint.
- **IMP-IDEM-003**: when `--overwrite-existing` is `false`, importer MUST NOT send `PUT` updates.
- **IMP-IDEM-008**: when `--overwrite-existing` is `true` and `scanName` already exists in ACS,
  importer MUST update it via `PUT /v2/compliance/scan/configurations/{id}`.
- **IMP-IDEM-009**: when `--overwrite-existing` is `true` and `scanName` does not exist,
  importer MUST create it via `POST` (same as IMP-IDEM-001 create path).

Example minimal report skeleton:

```json
{
  "meta": {
    "dryRun": true,
    "namespaceScope": "openshift-compliance",
    "mode": "create-only"
  },
  "counts": {
    "discovered": 2,
    "create": 1,
    "skip": 1,
    "failed": 0
  },
  "items": [],
  "problems": []
}
```
