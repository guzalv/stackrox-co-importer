Feature: Adopt externally-managed ScanSettingBindings before creating ACS scan configs
  As an operator migrating GitOps-managed Compliance Operator scans to ACS
  I want the importer to detect and safely remove conflicting external SSBs
  So that ACS can create its own scan configuration instead of failing on a raw conflict

  # RHACS >=4.11/5.0 Central rejects creating or updating a scan configuration whose profiles
  # overlap an existing ScanSettingBinding that is not labeled "app.kubernetes.io/name: stackrox"
  # (an "external" SSB). Since the importer's payloads are built from exactly such external SSBs,
  # every create is blocked by construction unless the external SSB is removed first. A
  # self-healing GitOps tool can recreate a deleted SSB in ~1s, so removal must be guarded and the
  # operator must pause GitOps reconciliation for the resource (documented prerequisite, not
  # enforced by any GitOps-tool-specific code here).

  Background:
    Given ACS endpoint and token preflight succeeded
    And the importer can read compliance.openshift.io resources

  # ─── Detection ────────────────────────────────────────────────────────────────

  # IMP-ADEXT-001
  @adopt-external @detect
  Scenario: Detect a blocking external ScanSettingBinding
    Given ScanSettingBinding "cis-weekly" in namespace "openshift-compliance" references profiles ["ocp4-cis"]
    And the SSB has no "app.kubernetes.io/name: stackrox" label
    And no ACS scan configuration named "cis-weekly" exists
    When the importer processes all discovered bindings
    Then "cis-weekly" MUST be identified as blocked by an external ScanSettingBinding
    And the blocking ScanSettingBinding MUST be "cis-weekly" in namespace "openshift-compliance"

  # IMP-ADEXT-013
  @adopt-external @detect
  Scenario: Stackrox-managed SSBs are not treated as blockers or re-imported
    Given ScanSettingBinding "cis-weekly" in namespace "openshift-compliance" references profiles ["ocp4-cis"]
    And the SSB has the "app.kubernetes.io/name: stackrox" label
    When the importer processes all discovered bindings
    Then "cis-weekly" MUST NOT be identified as blocked by an external ScanSettingBinding
    And "cis-weekly" MUST NOT be treated as a source binding to import

  # ─── Backup (always, before any removal) ──────────────────────────────────────

  # IMP-ADEXT-002
  @adopt-external @backup
  Scenario: Backup is written before any removal action
    Given ScanSettingBinding "cis-weekly" in namespace "openshift-compliance" references profiles ["ocp4-cis"]
    And the SSB has no "app.kubernetes.io/name: stackrox" label
    And the SSB references ScanSetting "gitops-weekly"
    And "cis-weekly" is identified as blocked by an external ScanSettingBinding
    When the importer processes the adoption workflow
    Then the backup directory MUST contain a YAML copy of ScanSettingBinding "cis-weekly"
    And the backup directory MUST contain a YAML copy of ScanSetting "gitops-weekly"
    And the backup directory MUST contain a "plan.json" describing scan configuration "cis-weekly"
    And the backup MUST be written before any ScanSettingBinding is deleted

  # IMP-ADEXT-015
  @adopt-external @backup
  Scenario: Backup dir conflict is rejected
    Given "--backup-dir" points to a non-empty directory with no "plan.json"
    When the importer starts
    Then the importer MUST fail with an error mentioning the backup directory is not empty

  # ─── Default behavior: report and skip, never block or delete ────────────────

  # IMP-ADEXT-003
  @adopt-external @default
  Scenario: Default behavior reports the conflict and skips without deleting anything
    Given "cis-weekly" is identified as blocked by an external ScanSettingBinding
    And "--delete-existing" is not set
    When the importer processes the adoption workflow
    Then ScanSettingBinding "cis-weekly" MUST NOT be deleted
    And "cis-weekly" MUST be skipped
    And problems list MUST include category "gitops" for "cis-weekly"
    And the problem fix hint MUST mention "--delete-existing" and pausing GitOps reconciliation
    And the importer MUST NOT prompt for confirmation
    And the importer MUST NOT block waiting for the ScanSettingBinding to be removed

  # ─── --delete-existing: confirm, then delete ──────────────────────────────────

  # IMP-ADEXT-004
  @adopt-external @delete-existing
  Scenario: --delete-existing with confirmation deletes the blocking SSB
    Given "cis-weekly" is identified as blocked by an external ScanSettingBinding
    And "--delete-existing" is set
    And the operator confirms the deletion prompt with "y"
    When the importer processes the adoption workflow
    Then ScanSettingBinding "cis-weekly" MUST be deleted

  # IMP-ADEXT-004
  @adopt-external @delete-existing
  Scenario: --yes skips the confirmation prompt
    Given "cis-weekly" is identified as blocked by an external ScanSettingBinding
    And "--delete-existing" is set
    And "--yes" is set
    When the importer processes the adoption workflow
    Then the importer MUST NOT prompt for confirmation
    And ScanSettingBinding "cis-weekly" MUST be deleted

  # IMP-ADEXT-005
  @adopt-external @delete-existing
  Scenario: Declining the confirmation prompt skips without deleting
    Given "cis-weekly" is identified as blocked by an external ScanSettingBinding
    And "--delete-existing" is set
    And the operator declines the deletion prompt with "n"
    When the importer processes the adoption workflow
    Then ScanSettingBinding "cis-weekly" MUST NOT be deleted
    And "cis-weekly" MUST be skipped
    And problems list MUST include category "gitops" for "cis-weekly"
    And the importer MUST NOT exit with a fatal error

  # ─── Settle-timeout guard against GitOps self-heal ────────────────────────────

  # IMP-ADEXT-006, IMP-ADEXT-007
  @adopt-external @settle
  Scenario: Reappearance during the settle window is treated as GitOps self-heal
    Given "cis-weekly" is identified as blocked by an external ScanSettingBinding
    And "--delete-existing" is set and confirmed
    And ScanSettingBinding "cis-weekly" reappears within the settle timeout after deletion
    When the importer processes the adoption workflow
    Then "cis-weekly" MUST be skipped
    And problems list MUST include category "gitops" for "cis-weekly"
    And the problem description MUST mention the ScanSettingBinding reappeared
    And no ACS scan configuration create or update call MUST be attempted for "cis-weekly"

  # IMP-ADEXT-006
  @adopt-external @settle
  Scenario: No reappearance within the settle window allows the create to proceed
    Given "cis-weekly" is identified as blocked by an external ScanSettingBinding
    And "--delete-existing" is set and confirmed
    And ScanSettingBinding "cis-weekly" does NOT reappear within the settle timeout
    When the importer processes the adoption workflow
    Then the importer MUST proceed to create ACS scan configuration "cis-weekly"

  # ─── Multi-cluster: all clusters must be clear ────────────────────────────────

  # IMP-ADEXT-008
  @adopt-external @multicluster
  Scenario: Create proceeds only once every cluster's blocking SSB is confirmed gone
    Given kubecontext "ctx-a" has a blocking ScanSettingBinding "cis-weekly"
    And kubecontext "ctx-b" has a blocking ScanSettingBinding "cis-weekly"
    And "--delete-existing" is set and confirmed
    And ScanSettingBinding "cis-weekly" on ctx-a is confirmed gone
    And ScanSettingBinding "cis-weekly" on ctx-b reappears within the settle timeout
    When the importer processes the adoption workflow
    Then the merged scan configuration "cis-weekly" MUST NOT be created
    And problems list MUST include category "gitops" for "cis-weekly"
    And the problem description MUST mention cluster "ctx-b"

  # ─── Create (idempotent) ───────────────────────────────────────────────────────

  # IMP-ADEXT-009
  @adopt-external @create
  Scenario: Create routes through the normal reconcile path and stays idempotent
    Given "cis-weekly" was already cleared of blocking external ScanSettingBindings
    And ACS already has a scan configuration named "cis-weekly"
    When the importer processes the adoption workflow
    Then the importer MUST NOT send a duplicate create for "cis-weekly"
    And "cis-weekly" MUST be skipped with reason mentioning "already exists"

  # ─── Orphaned ScanSetting ───────────────────────────────────────────────────────

  # IMP-ADEXT-010
  @adopt-external @orphan
  Scenario: Leftover ScanSetting is left in place with a warning
    Given ScanSettingBinding "cis-weekly" referenced ScanSetting "gitops-weekly" before deletion
    And "cis-weekly" was successfully created in ACS after removing the blocking ScanSettingBinding
    When the importer processes the adoption workflow
    Then ScanSetting "gitops-weekly" MUST NOT be deleted
    And problems list MUST include category "orphan" for "gitops-weekly"
    And the problem severity MUST be "warning"

  # ─── Resume after interruption or manual deletion ─────────────────────────────

  # IMP-ADEXT-011
  @adopt-external @resume
  Scenario: Resume from a persisted plan when the SSB is already gone
    Given a prior run persisted "plan.json" for scan configuration "cis-weekly" in "--backup-dir"
    And the plan's ACS endpoint and cluster IDs match the current run
    And ScanSettingBinding "cis-weekly" no longer exists on any cluster
    When the importer starts with the same "--backup-dir"
    Then the importer MUST use the persisted plan instead of failing due to missing discovery
    And the importer MUST proceed to create ACS scan configuration "cis-weekly"

  # IMP-ADEXT-012
  @adopt-external @resume
  Scenario: Resume refuses a plan from a different environment
    Given a prior run persisted "plan.json" for ACS endpoint "https://other-central.example.com"
    And the current run's ACS endpoint is "https://central.example.com"
    When the importer starts with the same "--backup-dir"
    Then config parsing or startup MUST fail with an error mentioning environment mismatch
    And the importer MUST NOT use the mismatched plan

  # ─── Dry-run: zero side effects ────────────────────────────────────────────────

  # IMP-ADEXT-014
  @adopt-external @dry-run
  Scenario: Dry-run performs no backup, deletion, or creation
    Given "cis-weekly" is identified as blocked by an external ScanSettingBinding
    And "--dry-run" is set
    When the importer processes the adoption workflow
    Then no backup directory or file MUST be written
    And ScanSettingBinding "cis-weekly" MUST NOT be deleted
    And no ACS scan configuration create or update call MUST be attempted
    And the importer MUST log the actions it would have taken
