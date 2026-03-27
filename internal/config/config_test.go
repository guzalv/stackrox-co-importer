package config

import (
	"strings"
	"testing"
	"time"
)

// mockEnv returns an envFunc backed by the given map.
func mockEnv(vars map[string]string) envFunc {
	return func(key string) string {
		return vars[key]
	}
}

// baseEnv returns the minimum env vars needed for a valid parse (token auth + endpoint).
func baseEnv() map[string]string {
	return map[string]string{
		"ROX_API_TOKEN": "tok-123",
		"ROX_ENDPOINT":  "central.example.com",
	}
}

// ─── IMP-CLI-001 + IMP-CLI-013: Endpoint parsing ────────────────────────────

// IMP-CLI-001
func TestIMP_CLI_001_EndpointFromFlag(t *testing.T) {
	env := map[string]string{"ROX_API_TOKEN": "tok"}
	cfg, err := Parse([]string{"--endpoint", "central.example.com"}, mockEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Endpoint != "https://central.example.com" {
		t.Errorf("got endpoint %q, want %q", cfg.Endpoint, "https://central.example.com")
	}
}

// IMP-CLI-001
func TestIMP_CLI_001_EndpointFromEnv(t *testing.T) {
	env := map[string]string{
		"ROX_API_TOKEN": "tok",
		"ROX_ENDPOINT":  "central.example.com",
	}
	cfg, err := Parse(nil, mockEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Endpoint != "https://central.example.com" {
		t.Errorf("got endpoint %q, want %q", cfg.Endpoint, "https://central.example.com")
	}
}

// IMP-CLI-001
func TestIMP_CLI_001_EndpointHTTPSPrepend(t *testing.T) {
	// Bare hostname should get https:// prepended.
	env := map[string]string{"ROX_API_TOKEN": "tok"}
	cfg, err := Parse([]string{"--endpoint", "my-host:8443"}, mockEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://my-host:8443"
	if cfg.Endpoint != want {
		t.Errorf("got %q, want %q", cfg.Endpoint, want)
	}
}

// IMP-CLI-001
func TestIMP_CLI_001_EndpointHTTPSPassthrough(t *testing.T) {
	env := map[string]string{"ROX_API_TOKEN": "tok"}
	cfg, err := Parse([]string{"--endpoint", "https://central.example.com"}, mockEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Endpoint != "https://central.example.com" {
		t.Errorf("got %q, want %q", cfg.Endpoint, "https://central.example.com")
	}
}

// IMP-CLI-013
func TestIMP_CLI_013_EndpointHTTPRejected(t *testing.T) {
	env := map[string]string{"ROX_API_TOKEN": "tok"}
	_, err := Parse([]string{"--endpoint", "http://central.example.com"}, mockEnv(env))
	if err == nil {
		t.Fatal("expected error for http:// endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "HTTPS") && !strings.Contains(err.Error(), "https") {
		t.Errorf("error should mention HTTPS, got: %v", err)
	}
}

// IMP-CLI-001
func TestIMP_CLI_001_EndpointRequired(t *testing.T) {
	env := map[string]string{"ROX_API_TOKEN": "tok"}
	_, err := Parse(nil, mockEnv(env))
	if err == nil {
		t.Fatal("expected error for missing endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "endpoint") && !strings.Contains(err.Error(), "ROX_ENDPOINT") {
		t.Errorf("error should mention endpoint, got: %v", err)
	}
}

// IMP-CLI-001: trailing slash stripped
func TestIMP_CLI_001_EndpointTrailingSlashStripped(t *testing.T) {
	env := map[string]string{"ROX_API_TOKEN": "tok"}
	cfg, err := Parse([]string{"--endpoint", "https://central.example.com/"}, mockEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Endpoint != "https://central.example.com" {
		t.Errorf("got %q, want trailing slash stripped", cfg.Endpoint)
	}
}

// IMP-CLI-001: flag overrides env
func TestIMP_CLI_001_FlagOverridesEnv(t *testing.T) {
	env := map[string]string{
		"ROX_API_TOKEN": "tok",
		"ROX_ENDPOINT":  "from-env.example.com",
	}
	cfg, err := Parse([]string{"--endpoint", "from-flag.example.com"}, mockEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Endpoint != "https://from-flag.example.com" {
		t.Errorf("flag should override env, got %q", cfg.Endpoint)
	}
}

// ─── IMP-CLI-002 + IMP-CLI-024 + IMP-CLI-025 + IMP-CLI-014: Auth ────────────

// IMP-CLI-002
func TestIMP_CLI_002_TokenModeInferred(t *testing.T) {
	env := map[string]string{
		"ROX_API_TOKEN": "my-token",
		"ROX_ENDPOINT":  "central.example.com",
	}
	cfg, err := Parse(nil, mockEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthMode != AuthModeToken {
		t.Errorf("got AuthMode %q, want %q", cfg.AuthMode, AuthModeToken)
	}
	if cfg.Token != "my-token" {
		t.Errorf("got Token %q, want %q", cfg.Token, "my-token")
	}
}

// IMP-CLI-002
func TestIMP_CLI_002_BasicModeInferred(t *testing.T) {
	env := map[string]string{
		"ROX_ADMIN_PASSWORD": "secret",
		"ROX_ENDPOINT":       "central.example.com",
	}
	cfg, err := Parse(nil, mockEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthMode != AuthModeBasic {
		t.Errorf("got AuthMode %q, want %q", cfg.AuthMode, AuthModeBasic)
	}
	if cfg.Password != "secret" {
		t.Errorf("got Password %q, want %q", cfg.Password, "secret")
	}
}

// IMP-CLI-025
func TestIMP_CLI_025_AmbiguousAuthError(t *testing.T) {
	env := map[string]string{
		"ROX_API_TOKEN":      "tok",
		"ROX_ADMIN_PASSWORD": "pass",
		"ROX_ENDPOINT":       "central.example.com",
	}
	_, err := Parse(nil, mockEnv(env))
	if err == nil {
		t.Fatal("expected ambiguous auth error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should mention ambiguous, got: %v", err)
	}
}

// IMP-CLI-025
func TestIMP_CLI_025_NeitherAuthError(t *testing.T) {
	env := map[string]string{
		"ROX_ENDPOINT": "central.example.com",
	}
	_, err := Parse(nil, mockEnv(env))
	if err == nil {
		t.Fatal("expected no-auth error, got nil")
	}
	if !strings.Contains(err.Error(), "ROX_API_TOKEN") || !strings.Contains(err.Error(), "ROX_ADMIN_PASSWORD") {
		t.Errorf("error should mention both auth options, got: %v", err)
	}
}

// IMP-CLI-014
func TestIMP_CLI_014_TokenNonEmpty(t *testing.T) {
	// Token env var set but empty — should be treated as "not set" and fall
	// through to "neither" error since password is also absent.
	env := map[string]string{
		"ROX_API_TOKEN": "",
		"ROX_ENDPOINT":  "central.example.com",
	}
	_, err := Parse(nil, mockEnv(env))
	if err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
}

// IMP-CLI-014
func TestIMP_CLI_014_PasswordNonEmpty(t *testing.T) {
	env := map[string]string{
		"ROX_ADMIN_PASSWORD": "",
		"ROX_ENDPOINT":       "central.example.com",
	}
	_, err := Parse(nil, mockEnv(env))
	if err == nil {
		t.Fatal("expected error for empty password, got nil")
	}
}

// IMP-CLI-024
func TestIMP_CLI_024_BasicModeUsernameDefault(t *testing.T) {
	env := map[string]string{
		"ROX_ADMIN_PASSWORD": "pass",
		"ROX_ENDPOINT":       "central.example.com",
	}
	cfg, err := Parse(nil, mockEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Username != "admin" {
		t.Errorf("got Username %q, want default %q", cfg.Username, "admin")
	}
}

// IMP-CLI-024
func TestIMP_CLI_024_BasicModeUsernameFromEnv(t *testing.T) {
	env := map[string]string{
		"ROX_ADMIN_PASSWORD": "pass",
		"ROX_ADMIN_USER":     "custom-user",
		"ROX_ENDPOINT":       "central.example.com",
	}
	cfg, err := Parse(nil, mockEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Username != "custom-user" {
		t.Errorf("got Username %q, want %q", cfg.Username, "custom-user")
	}
}

// IMP-CLI-024
func TestIMP_CLI_024_BasicModeUsernameFromFlag(t *testing.T) {
	env := map[string]string{
		"ROX_ADMIN_PASSWORD": "pass",
		"ROX_ADMIN_USER":     "env-user",
		"ROX_ENDPOINT":       "central.example.com",
	}
	cfg, err := Parse([]string{"--username", "flag-user"}, mockEnv(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Username != "flag-user" {
		t.Errorf("flag should override env, got %q", cfg.Username)
	}
}

// ─── IMP-CLI-004: Namespace scope ────────────────────────────────────────────

// IMP-CLI-004
func TestIMP_CLI_004_NamespaceDefault(t *testing.T) {
	cfg, err := Parse(nil, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Namespace != "openshift-compliance" {
		t.Errorf("got Namespace %q, want default %q", cfg.Namespace, "openshift-compliance")
	}
}

// IMP-CLI-004
func TestIMP_CLI_004_NamespaceCustom(t *testing.T) {
	cfg, err := Parse([]string{"--co-namespace", "my-ns"}, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Namespace != "my-ns" {
		t.Errorf("got Namespace %q, want %q", cfg.Namespace, "my-ns")
	}
}

// IMP-CLI-004
func TestIMP_CLI_004_AllNamespacesClearsNamespace(t *testing.T) {
	cfg, err := Parse([]string{"--co-all-namespaces"}, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.AllNamespaces {
		t.Error("AllNamespaces should be true")
	}
	if cfg.Namespace != "" {
		t.Errorf("Namespace should be empty when AllNamespaces is true, got %q", cfg.Namespace)
	}
}

// IMP-CLI-004: --co-all-namespaces overrides --co-namespace
func TestIMP_CLI_004_AllNamespacesOverridesNamespace(t *testing.T) {
	cfg, err := Parse(
		[]string{"--co-namespace", "my-ns", "--co-all-namespaces"},
		mockEnv(baseEnv()),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Namespace != "" {
		t.Errorf("Namespace should be cleared when --co-all-namespaces is set, got %q", cfg.Namespace)
	}
}

// ─── IMP-CLI-006, IMP-CLI-007, IMP-CLI-027: Mode flags ──────────────────────

// IMP-CLI-006
func TestIMP_CLI_006_OverwriteExistingDefault(t *testing.T) {
	cfg, err := Parse(nil, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OverwriteExisting {
		t.Error("OverwriteExisting should default to false")
	}
}

// IMP-CLI-027
func TestIMP_CLI_027_OverwriteExistingEnabled(t *testing.T) {
	cfg, err := Parse([]string{"--overwrite-existing"}, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.OverwriteExisting {
		t.Error("OverwriteExisting should be true when flag is set")
	}
}

// IMP-CLI-007
func TestIMP_CLI_007_DryRunDefault(t *testing.T) {
	cfg, err := Parse(nil, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DryRun {
		t.Error("DryRun should default to false")
	}
}

// IMP-CLI-007
func TestIMP_CLI_007_DryRunEnabled(t *testing.T) {
	cfg, err := Parse([]string{"--dry-run"}, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.DryRun {
		t.Error("DryRun should be true when flag is set")
	}
}

// ─── IMP-CLI-009, IMP-CLI-010, IMP-CLI-011, IMP-CLI-012: Optional inputs ────

// IMP-CLI-009
func TestIMP_CLI_009_RequestTimeoutDefault(t *testing.T) {
	cfg, err := Parse(nil, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RequestTimeout != 30*time.Second {
		t.Errorf("got RequestTimeout %v, want 30s", cfg.RequestTimeout)
	}
}

// IMP-CLI-009
func TestIMP_CLI_009_RequestTimeoutCustom(t *testing.T) {
	cfg, err := Parse([]string{"--request-timeout", "1m"}, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RequestTimeout != time.Minute {
		t.Errorf("got RequestTimeout %v, want 1m", cfg.RequestTimeout)
	}
}

// IMP-CLI-010
func TestIMP_CLI_010_MaxRetriesDefault(t *testing.T) {
	cfg, err := Parse(nil, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxRetries != 5 {
		t.Errorf("got MaxRetries %d, want 5", cfg.MaxRetries)
	}
}

// IMP-CLI-010
func TestIMP_CLI_010_MaxRetriesCustom(t *testing.T) {
	cfg, err := Parse([]string{"--max-retries", "10"}, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxRetries != 10 {
		t.Errorf("got MaxRetries %d, want 10", cfg.MaxRetries)
	}
}

// IMP-CLI-010
func TestIMP_CLI_010_MaxRetriesZeroAllowed(t *testing.T) {
	cfg, err := Parse([]string{"--max-retries", "0"}, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxRetries != 0 {
		t.Errorf("got MaxRetries %d, want 0", cfg.MaxRetries)
	}
}

// IMP-CLI-010
func TestIMP_CLI_010_MaxRetriesNegativeRejected(t *testing.T) {
	_, err := Parse([]string{"--max-retries", "-1"}, mockEnv(baseEnv()))
	if err == nil {
		t.Fatal("expected error for negative max-retries, got nil")
	}
}

// IMP-CLI-011
func TestIMP_CLI_011_CACertFileDefault(t *testing.T) {
	cfg, err := Parse(nil, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CACertFile != "" {
		t.Errorf("CACertFile should default to empty, got %q", cfg.CACertFile)
	}
}

// IMP-CLI-011
func TestIMP_CLI_011_CACertFileCustom(t *testing.T) {
	cfg, err := Parse([]string{"--ca-cert-file", "/path/to/ca.pem"}, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CACertFile != "/path/to/ca.pem" {
		t.Errorf("got CACertFile %q, want %q", cfg.CACertFile, "/path/to/ca.pem")
	}
}

// IMP-CLI-012
func TestIMP_CLI_012_InsecureSkipVerifyDefault(t *testing.T) {
	cfg, err := Parse(nil, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should default to false")
	}
}

// IMP-CLI-012
func TestIMP_CLI_012_InsecureSkipVerifyEnabled(t *testing.T) {
	cfg, err := Parse([]string{"--insecure-skip-verify"}, mockEnv(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true when flag is set")
	}
}

// ─── IMP-CLI-017, IMP-CLI-018, IMP-CLI-019: Exit codes ──────────────────────

// IMP-CLI-017
func TestIMP_CLI_017_ExitOK(t *testing.T) {
	if ExitOK != 0 {
		t.Errorf("ExitOK should be 0, got %d", ExitOK)
	}
}

// IMP-CLI-018
func TestIMP_CLI_018_ExitConfigError(t *testing.T) {
	if ExitConfigError != 1 {
		t.Errorf("ExitConfigError should be 1, got %d", ExitConfigError)
	}
}

// IMP-CLI-019
func TestIMP_CLI_019_ExitPartialSuccess(t *testing.T) {
	if ExitPartialSuccess != 2 {
		t.Errorf("ExitPartialSuccess should be 2, got %d", ExitPartialSuccess)
	}
}
