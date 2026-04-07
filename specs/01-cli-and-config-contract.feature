Feature: CLI and configuration contract
  As an operator running the importer
  I want predictable CLI and environment-variable behaviour
  So that the tool behaves consistently and fails fast with clear messages

  # ─── IMP-CLI-001, IMP-CLI-013: Endpoint ──────────────────────────────────────

  # IMP-CLI-001
  Scenario: Endpoint read from --endpoint flag
    Given env var "ROX_API_TOKEN" is "tok"
    When I parse config with flags "--endpoint central.example.com"
    Then config parsing succeeds
    And the endpoint is "https://central.example.com"

  # IMP-CLI-001
  Scenario: Endpoint read from ROX_ENDPOINT env var
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And the endpoint is "https://central.example.com"

  # IMP-CLI-001
  Scenario: --endpoint flag overrides ROX_ENDPOINT env var
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "from-env.example.com"
    When I parse config with flags "--endpoint from-flag.example.com"
    Then config parsing succeeds
    And the endpoint is "https://from-flag.example.com"

  # IMP-CLI-001
  Scenario: https:// endpoint accepted as-is
    Given env var "ROX_API_TOKEN" is "tok"
    When I parse config with flags "--endpoint https://central.example.com"
    Then config parsing succeeds
    And the endpoint is "https://central.example.com"

  # IMP-CLI-001
  Scenario: Trailing slash stripped from endpoint
    Given env var "ROX_API_TOKEN" is "tok"
    When I parse config with flags "--endpoint https://central.example.com/"
    Then config parsing succeeds
    And the endpoint is "https://central.example.com"

  # IMP-CLI-001
  Scenario: Endpoint is required when neither flag nor env var is set
    Given env var "ROX_API_TOKEN" is "tok"
    When I parse config with no flags
    Then config parsing fails with error containing "endpoint"

  # IMP-CLI-013
  Scenario: http:// endpoint is rejected
    Given env var "ROX_API_TOKEN" is "tok"
    When I parse config with flags "--endpoint http://central.example.com"
    Then config parsing fails with error containing "https"

  # ─── IMP-CLI-002, IMP-CLI-025: Auth mode ──────────────────────────────────────

  # IMP-CLI-002
  Scenario: Token mode inferred from ROX_API_TOKEN
    Given env var "ROX_API_TOKEN" is "my-token"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And the auth mode is "token"
    And the token is "my-token"

  # IMP-CLI-002
  Scenario: Basic mode inferred from ROX_ADMIN_PASSWORD
    Given env var "ROX_ADMIN_PASSWORD" is "secret"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And the auth mode is "basic"
    And the password is "secret"

  # IMP-CLI-025
  Scenario: Both auth vars set — token mode is used with a warning
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ADMIN_PASSWORD" is "pass"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And the auth mode is "token"
    And a warning is emitted containing "ROX_ADMIN_PASSWORD"

  # IMP-CLI-025
  Scenario: Neither auth var set — error with help text listing both options
    Given env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing fails with error containing "ROX_API_TOKEN"
    And the error also contains "ROX_ADMIN_PASSWORD"

  # ─── IMP-CLI-014: Auth material must be non-empty ─────────────────────────────

  # IMP-CLI-014
  Scenario: Empty ROX_API_TOKEN is treated as not set
    Given env var "ROX_API_TOKEN" is ""
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing fails

  # IMP-CLI-014
  Scenario: Empty ROX_ADMIN_PASSWORD is treated as not set
    Given env var "ROX_ADMIN_PASSWORD" is ""
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing fails

  # ─── IMP-CLI-024: Username ────────────────────────────────────────────────────

  # IMP-CLI-024
  Scenario: Username defaults to admin in basic mode
    Given env var "ROX_ADMIN_PASSWORD" is "pass"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And the username is "admin"

  # IMP-CLI-024
  Scenario: Username read from ROX_ADMIN_USER env var
    Given env var "ROX_ADMIN_PASSWORD" is "pass"
    And env var "ROX_ADMIN_USER" is "custom-user"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And the username is "custom-user"

  # IMP-CLI-024
  Scenario: --username flag overrides ROX_ADMIN_USER
    Given env var "ROX_ADMIN_PASSWORD" is "pass"
    And env var "ROX_ADMIN_USER" is "env-user"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--username flag-user"
    Then config parsing succeeds
    And the username is "flag-user"

  # ─── IMP-CLI-004: Namespace scope ─────────────────────────────────────────────

  # IMP-CLI-004
  Scenario: Namespace defaults to openshift-compliance
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And the namespace is "openshift-compliance"

  # IMP-CLI-004
  Scenario: Custom namespace via --co-namespace
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--co-namespace my-ns"
    Then config parsing succeeds
    And the namespace is "my-ns"

  # IMP-CLI-004
  Scenario: --co-all-namespaces clears namespace
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--co-all-namespaces"
    Then config parsing succeeds
    And all-namespaces is enabled
    And the namespace is ""

  # IMP-CLI-004
  Scenario: --co-all-namespaces overrides --co-namespace
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--co-namespace my-ns --co-all-namespaces"
    Then config parsing succeeds
    And all-namespaces is enabled
    And the namespace is ""

  # ─── IMP-CLI-006, IMP-CLI-027: Overwrite existing ────────────────────────────

  # IMP-CLI-006
  Scenario: Overwrite-existing defaults to false
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And overwrite-existing is disabled

  # IMP-CLI-027
  Scenario: --overwrite-existing enables update mode
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--overwrite-existing"
    Then config parsing succeeds
    And overwrite-existing is enabled

  # ─── IMP-CLI-007: Dry-run ─────────────────────────────────────────────────────

  # IMP-CLI-007
  Scenario: Dry-run defaults to false
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And dry-run is disabled

  # IMP-CLI-007
  Scenario: --dry-run enables dry-run mode
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--dry-run"
    Then config parsing succeeds
    And dry-run is enabled

  # ─── IMP-CLI-009: Request timeout ─────────────────────────────────────────────

  # IMP-CLI-009
  Scenario: Request timeout defaults to 30s
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And the request timeout is "30s"

  # IMP-CLI-009
  Scenario: Custom request timeout via --request-timeout
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--request-timeout 1m"
    Then config parsing succeeds
    And the request timeout is "1m0s"

  # ─── IMP-CLI-010: Max retries ─────────────────────────────────────────────────

  # IMP-CLI-010
  Scenario: Max retries defaults to 5
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And max retries is 5

  # IMP-CLI-010
  Scenario: Custom max retries via --max-retries
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--max-retries 10"
    Then config parsing succeeds
    And max retries is 10

  # IMP-CLI-010
  Scenario: Max retries of 0 is allowed
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--max-retries 0"
    Then config parsing succeeds
    And max retries is 0

  # IMP-CLI-010
  Scenario: Negative max retries are rejected
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--max-retries -1"
    Then config parsing fails

  # ─── IMP-CLI-011: CA cert file ────────────────────────────────────────────────

  # IMP-CLI-011
  Scenario: CA cert file defaults to empty
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And the CA cert file is ""

  # IMP-CLI-011
  Scenario: Custom CA cert file via --ca-cert-file
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--ca-cert-file /path/to/ca.pem"
    Then config parsing succeeds
    And the CA cert file is "/path/to/ca.pem"

  # ─── IMP-CLI-012: Insecure skip verify ───────────────────────────────────────

  # IMP-CLI-012
  Scenario: Insecure-skip-verify defaults to false
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And insecure-skip-verify is disabled

  # IMP-CLI-012
  Scenario: --insecure-skip-verify enables TLS bypass
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--insecure-skip-verify"
    Then config parsing succeeds
    And insecure-skip-verify is enabled

  # ─── IMP-CLI-017, IMP-CLI-018, IMP-CLI-019: Exit codes ──────────────────────

  # IMP-CLI-017
  Scenario: Exit code for completed run with no failures is 0
    Then the "success" exit code is 0

  # IMP-CLI-018
  Scenario: Exit code for fatal config or preflight error is 1
    Then the "config-error" exit code is 1

  # IMP-CLI-019
  Scenario: Exit code for partial success is 2
    Then the "partial-success" exit code is 2

  # ─── IMP-CLI-028: --exclude ──────────────────────────────────────────────────

  # IMP-CLI-028
  Scenario: --exclude defaults to empty (no exclusions)
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And there are no exclude patterns

  # IMP-CLI-028
  Scenario: Single --exclude pattern is stored
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--exclude ocp4-cis"
    Then config parsing succeeds
    And the exclude patterns are "ocp4-cis"

  # IMP-CLI-028
  Scenario: Multiple --exclude flags are accumulated
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--exclude ocp4-cis --exclude ocp4-pci.*"
    Then config parsing succeeds
    And the exclude patterns are "ocp4-cis,ocp4-pci.*"

  # IMP-CLI-028
  Scenario: Invalid --exclude regex causes a config parse error
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--exclude [invalid"
    Then config parsing fails with error containing "exclude"

  # ─── IMP-CLI-029: --list-ssbs ────────────────────────────────────────────────

  # IMP-CLI-029
  Scenario: --list-ssbs defaults to false
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with no flags
    Then config parsing succeeds
    And list-ssbs is disabled

  # IMP-CLI-029
  Scenario: --list-ssbs flag enables list mode
    Given env var "ROX_API_TOKEN" is "tok"
    And env var "ROX_ENDPOINT" is "central.example.com"
    When I parse config with flags "--list-ssbs"
    Then config parsing succeeds
    And list-ssbs is enabled
