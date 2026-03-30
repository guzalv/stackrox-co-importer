//go:build e2e

package e2e

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Report JSON shape (IMP-CLI-021)
// ---------------------------------------------------------------------------

type reportMeta struct {
	DryRun         bool   `json:"dryRun"`
	NamespaceScope string `json:"namespaceScope"`
	Mode           string `json:"mode"`
}

type reportCounts struct {
	Discovered int `json:"discovered"`
	Create     int `json:"create"`
	Skip       int `json:"skip"`
	Failed     int `json:"failed"`
}

type reportItem struct {
	Source struct {
		Namespace       string `json:"namespace"`
		BindingName     string `json:"bindingName"`
		ScanSettingName string `json:"scanSettingName"`
	} `json:"source"`
	Action          string `json:"action"`
	Reason          string `json:"reason"`
	Attempts        int    `json:"attempts"`
	ACSScanConfigID string `json:"acsScanConfigId"`
	Error           string `json:"error"`
}

type reportProblem struct {
	Severity    string `json:"severity"`
	Category    string `json:"category"`
	ResourceRef string `json:"resourceRef"`
	Description string `json:"description"`
	FixHint     string `json:"fixHint"`
	Skipped     bool   `json:"skipped"`
}

type report struct {
	Meta     reportMeta      `json:"meta"`
	Counts   reportCounts    `json:"counts"`
	Items    []reportItem    `json:"items"`
	Problems []reportProblem `json:"problems"`
}

// ---------------------------------------------------------------------------
// ACS scan configuration list response (subset)
// ---------------------------------------------------------------------------

type acsScanConfig struct {
	ID       string `json:"id"`
	ScanName string `json:"scanName"`
}

type acsScanConfigListResponse struct {
	Configurations []acsScanConfig `json:"configurations"`
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// runID generates a 6-char random hex suffix for unique resource names.
// IMP-ACC-019
func runID() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate run ID: %v", err))
	}
	return hex.EncodeToString(b)
}

func requireEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("required env var %s not set — skipping", key)
	}
	return v
}

func coNamespace(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("CO_NAMESPACE"); v != "" {
		return v
	}
	return "openshift-compliance"
}

func importerBin(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("IMPORTER_BIN"); v != "" {
		return v
	}
	// Default path relative to repo root (this file lives in e2e/).
	return filepath.Join("..", "bin", "compliance-operator-importer")
}

// runImporter executes the importer binary with the given arguments and returns
// stdout, stderr, and the exit code.
func runImporter(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	bin := importerBin(t)

	cmd := exec.Command(bin, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run importer binary %q: %v", bin, err)
		}
	}
	return stdout, stderr, exitCode
}

func httpClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // test cluster uses self-signed certs
			},
		},
	}
}

// acsAPIGet makes an authenticated GET request to the ACS API and returns the
// response body. It uses ROX_API_TOKEN if set, otherwise falls back to basic
// auth with ROX_ADMIN_USER/ROX_ADMIN_PASSWORD.
func acsAPIGet(t *testing.T, path string) []byte {
	t.Helper()
	endpoint := requireEnv(t, "ROX_ENDPOINT")

	url := strings.TrimRight(endpoint, "/") + path
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}

	setAuth(t, req)

	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatalf("ACS API GET %s: %v", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ACS API GET %s returned %d: %s", path, resp.StatusCode, string(body))
	}
	return body
}

// setAuth sets the Authorization header on the request based on available env vars.
func setAuth(t *testing.T, req *http.Request) {
	t.Helper()
	if token := os.Getenv("ROX_API_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		return
	}
	password := os.Getenv("ROX_ADMIN_PASSWORD")
	if password == "" {
		t.Skip("neither ROX_API_TOKEN nor ROX_ADMIN_PASSWORD is set — skipping")
	}
	user := os.Getenv("ROX_ADMIN_USER")
	if user == "" {
		user = "admin"
	}
	req.SetBasicAuth(user, password)
}

// listACSConfigs returns all scan configurations from ACS.
func listACSConfigs(t *testing.T) []acsScanConfig {
	t.Helper()
	body := acsAPIGet(t, "/v2/compliance/scan/configurations?pagination.limit=200")
	var resp acsScanConfigListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("parsing ACS scan config list: %v", err)
	}
	return resp.Configurations
}

// parseReport reads and parses a JSON report file.
func parseReport(t *testing.T, path string) report {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading report file %s: %v", path, err)
	}
	var r report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("parsing report JSON from %s: %v\nRaw: %s", path, err, string(data))
	}
	return r
}

// deleteACSConfig deletes an ACS scan configuration by ID. Used for cleanup.
func deleteACSConfig(t *testing.T, id string) {
	t.Helper()
	endpoint := requireEnv(t, "ROX_ENDPOINT")
	url := strings.TrimRight(endpoint, "/") + "/v2/compliance/scan/configurations/" + id

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Logf("warning: failed to build delete request for ACS config %s: %v", id, err)
		return
	}
	setAuth(t, req)

	resp, err := httpClient().Do(req)
	if err != nil {
		t.Logf("warning: failed to delete ACS config %s: %v", id, err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Logf("warning: delete ACS config %s returned %d", id, resp.StatusCode)
	}
}

// configNames returns a set of scan config names.
func configNames(configs []acsScanConfig) map[string]bool {
	m := make(map[string]bool, len(configs))
	for _, c := range configs {
		m[c.ScanName] = true
	}
	return m
}

// commonArgs returns the common flags used in most importer invocations.
// IMP-ACC-022: includes --co-namespace to scope to test resources.
func commonArgs(t *testing.T) []string {
	t.Helper()
	endpoint := requireEnv(t, "ROX_ENDPOINT")
	ns := coNamespace(t)
	return []string{
		"--endpoint", endpoint,
		"--insecure-skip-verify",
		"--co-namespace", ns,
	}
}

// ---------------------------------------------------------------------------
// CO resource helpers (IMP-ACC-018)
// ---------------------------------------------------------------------------

// createScanSetting creates a ScanSetting in the given namespace via kubectl.
func createScanSetting(t *testing.T, namespace, name, schedule string) {
	t.Helper()
	yaml := fmt.Sprintf(`apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSetting
metadata:
  name: %s
  namespace: %s
schedule: "%s"
roles:
  - master
  - worker
`, name, namespace, schedule)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to create ScanSetting %s/%s: %v\n%s", namespace, name, err, string(out))
	}
	t.Logf("created ScanSetting %s/%s", namespace, name)
}

// createScanSettingBinding creates a ScanSettingBinding in the given namespace via kubectl.
func createScanSettingBinding(t *testing.T, namespace, name, scanSettingName string, profiles []string) {
	t.Helper()

	var profileEntries strings.Builder
	for _, p := range profiles {
		fmt.Fprintf(&profileEntries, `  - name: %s
    kind: Profile
    apiGroup: compliance.openshift.io/v1alpha1
`, p)
	}

	yaml := fmt.Sprintf(`apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSettingBinding
metadata:
  name: %s
  namespace: %s
profiles:
%ssettingsRef:
  name: %s
  kind: ScanSetting
  apiGroup: compliance.openshift.io/v1alpha1
`, name, namespace, profileEntries.String(), scanSettingName)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to create ScanSettingBinding %s/%s: %v\n%s", namespace, name, err, string(out))
	}
	t.Logf("created ScanSettingBinding %s/%s", namespace, name)
}

// deleteResource deletes a CO resource via kubectl. Errors are logged but not fatal
// (used in t.Cleanup). The gvr should be the plural resource name, e.g. "scansettings"
// or "scansettingbindings".
func deleteResource(t *testing.T, namespace, gvr, name string) {
	t.Helper()
	fullGVR := gvr + ".compliance.openshift.io"
	cmd := exec.Command("kubectl", "delete", fullGVR, name,
		"-n", namespace, "--ignore-not-found")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("warning: failed to delete %s %s/%s: %v\n%s", gvr, namespace, name, err, string(out))
		return
	}
	t.Logf("deleted %s %s/%s", gvr, namespace, name)
}

// cleanupACSConfigByName finds and deletes any ACS scan config with the given name.
// Used in t.Cleanup to remove configs created during tests.
func cleanupACSConfigByName(t *testing.T, name string) {
	t.Helper()
	configs := listACSConfigs(t)
	for _, c := range configs {
		if c.ScanName == name {
			deleteACSConfig(t, c.ID)
			t.Logf("deleted ACS scan config %q (id=%s)", c.ScanName, c.ID)
		}
	}
}

// ---------------------------------------------------------------------------
// A2 — ACS auth preflight (IMP-ACC-002, IMP-ACC-013)
// ---------------------------------------------------------------------------

// IMP-ACC-002
func TestIMP_ACC_002_TokenAuthPreflight(t *testing.T) {
	requireEnv(t, "ROX_ENDPOINT")
	token := os.Getenv("ROX_API_TOKEN")
	if token == "" {
		t.Skip("ROX_API_TOKEN not set — skipping token auth preflight test")
	}

	body := acsAPIGet(t, "/v2/compliance/scan/configurations?pagination.limit=1")

	// Must be valid JSON.
	if !json.Valid(body) {
		t.Fatalf("response is not valid JSON: %s", string(body))
	}
}

// IMP-ACC-013
func TestIMP_ACC_013_BasicAuthPreflight(t *testing.T) {
	requireEnv(t, "ROX_ENDPOINT")
	password := os.Getenv("ROX_ADMIN_PASSWORD")
	if password == "" {
		t.Skip("ROX_ADMIN_PASSWORD not set — skipping basic auth preflight test")
	}
	// Clear token so the helper uses basic auth.
	originalToken := os.Getenv("ROX_API_TOKEN")
	os.Unsetenv("ROX_API_TOKEN")
	t.Cleanup(func() {
		if originalToken != "" {
			os.Setenv("ROX_API_TOKEN", originalToken)
		}
	})

	body := acsAPIGet(t, "/v2/compliance/scan/configurations?pagination.limit=1")

	if !json.Valid(body) {
		t.Fatalf("response is not valid JSON: %s", string(body))
	}
}

// ---------------------------------------------------------------------------
// A3 — Dry-run side-effect safety (IMP-ACC-003)
// IMP-ACC-018, IMP-ACC-019, IMP-ACC-020, IMP-ACC-021, IMP-ACC-022
// ---------------------------------------------------------------------------

// IMP-ACC-003
func TestIMP_ACC_003_DryRunNoWrites(t *testing.T) {
	id := runID()
	ns := coNamespace(t)
	ssName := "e2e-ss-" + id
	ssbName := "e2e-dryrun-" + id

	// IMP-ACC-018: create own CO resources.
	createScanSetting(t, ns, ssName, "0 2 * * *")
	createScanSettingBinding(t, ns, ssbName, ssName, []string{"ocp4-cis"})

	// IMP-ACC-020: clean up ALL resources via t.Cleanup, even on failure.
	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", ssbName)
		deleteResource(t, ns, "scansettings", ssName)
		cleanupACSConfigByName(t, ssbName)
	})

	// Snapshot ACS configs before the run.
	configsBefore := listACSConfigs(t)

	reportPath := filepath.Join(t.TempDir(), "dryrun-report.json")
	args := append(commonArgs(t),
		"--dry-run",
		"--report-json", reportPath,
	)

	_, _, exitCode := runImporter(t, args...)

	// Exit code must be 0 (success) or 2 (partial).
	if exitCode != 0 && exitCode != 2 {
		t.Fatalf("expected exit code 0 or 2, got %d", exitCode)
	}

	// Report file must exist and be valid JSON.
	r := parseReport(t, reportPath)

	// Dry-run flag must be set in report.
	if !r.Meta.DryRun {
		t.Error("report meta.dryRun should be true for --dry-run invocation")
	}

	// No items should have action "create" with an actual ACS config ID,
	// because dry-run should not apply any changes.
	for _, item := range r.Items {
		if item.Action == "create" && item.ACSScanConfigID != "" {
			t.Errorf("dry-run produced a create action with an actual ACS config ID: %+v", item)
		}
	}

	// problems[] should be present and contain description + fixHint for each problematic resource.
	for _, p := range r.Problems {
		if p.Description == "" {
			t.Errorf("problem missing description: %+v", p)
		}
		if p.FixHint == "" {
			t.Errorf("problem missing fixHint: %+v", p)
		}
	}

	// Verify no configs were actually created.
	configsAfter := listACSConfigs(t)
	namesBefore := configNames(configsBefore)
	for _, c := range configsAfter {
		if !namesBefore[c.ScanName] {
			t.Errorf("dry-run created ACS config %q (id=%s) — expected no writes", c.ScanName, c.ID)
		}
	}
}

// ---------------------------------------------------------------------------
// A4 — Apply creates expected configs (IMP-ACC-004)
// IMP-ACC-018, IMP-ACC-019, IMP-ACC-020, IMP-ACC-021, IMP-ACC-022
// ---------------------------------------------------------------------------

// IMP-ACC-004
func TestIMP_ACC_004_ApplyCreatesConfigs(t *testing.T) {
	id := runID()
	ns := coNamespace(t)
	ssName := "e2e-ss-" + id
	ssbName := "e2e-apply-" + id

	// IMP-ACC-018: create own CO resources.
	createScanSetting(t, ns, ssName, "0 2 * * *")
	createScanSettingBinding(t, ns, ssbName, ssName, []string{"ocp4-cis"})

	// IMP-ACC-020: clean up ALL resources via t.Cleanup, even on failure.
	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", ssbName)
		deleteResource(t, ns, "scansettings", ssName)
		cleanupACSConfigByName(t, ssbName)
	})

	reportPath := filepath.Join(t.TempDir(), "apply-report.json")
	args := append(commonArgs(t),
		"--report-json", reportPath,
	)

	_, _, exitCode := runImporter(t, args...)

	if exitCode != 0 && exitCode != 2 {
		t.Fatalf("expected exit code 0 or 2, got %d", exitCode)
	}

	r := parseReport(t, reportPath)

	// Expect at least one discovered binding (the one we created).
	if r.Counts.Discovered == 0 {
		t.Fatal("report shows 0 discovered bindings — expected at least one")
	}

	// Report should show action=create for our SSB.
	foundCreate := false
	for _, item := range r.Items {
		if item.Source.BindingName == ssbName && item.Action == "create" {
			foundCreate = true
			break
		}
	}
	if !foundCreate {
		t.Errorf("report does not show action=create for %q", ssbName)
	}

	// Verify ACS config was actually created.
	configs := listACSConfigs(t)
	found := false
	for _, c := range configs {
		if c.ScanName == ssbName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ACS scan config %q not found after apply", ssbName)
	}
}

// ---------------------------------------------------------------------------
// A5 — Idempotency on second run (IMP-ACC-005)
// IMP-ACC-018, IMP-ACC-019, IMP-ACC-020, IMP-ACC-021, IMP-ACC-022
// ---------------------------------------------------------------------------

// IMP-ACC-005
func TestIMP_ACC_005_SecondRunIdempotent(t *testing.T) {
	id := runID()
	ns := coNamespace(t)
	ssName := "e2e-ss-" + id
	ssbName := "e2e-idem-" + id

	// IMP-ACC-018: create own CO resources.
	createScanSetting(t, ns, ssName, "0 2 * * *")
	createScanSettingBinding(t, ns, ssbName, ssName, []string{"ocp4-cis"})

	// IMP-ACC-020: clean up ALL resources via t.Cleanup, even on failure.
	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", ssbName)
		deleteResource(t, ns, "scansettings", ssName)
		cleanupACSConfigByName(t, ssbName)
	})

	// First run: apply.
	reportPath1 := filepath.Join(t.TempDir(), "first-run.json")
	firstArgs := append(commonArgs(t), "--report-json", reportPath1)

	_, _, exitCode := runImporter(t, firstArgs...)
	if exitCode != 0 && exitCode != 2 {
		t.Fatalf("first run: expected exit code 0 or 2, got %d", exitCode)
	}

	// Snapshot configs after first run.
	configsAfterFirst := listACSConfigs(t)

	// Second run: apply again.
	reportPath2 := filepath.Join(t.TempDir(), "second-run.json")
	secondArgs := append(commonArgs(t), "--report-json", reportPath2)

	_, _, exitCode = runImporter(t, secondArgs...)
	if exitCode != 0 && exitCode != 2 {
		t.Fatalf("second run: expected exit code 0 or 2, got %d", exitCode)
	}

	r := parseReport(t, reportPath2)

	// Second run should have no new creates — everything should be skipped.
	if r.Counts.Create != 0 {
		t.Errorf("second run created %d configs — expected 0 (idempotent)", r.Counts.Create)
	}

	// Our SSB should show action=skip on the second run.
	for _, item := range r.Items {
		if item.Source.BindingName == ssbName && item.Action == "create" {
			t.Errorf("second run has action=create for %q — expected skip", ssbName)
		}
	}

	// No net changes in ACS config list.
	configsAfterSecond := listACSConfigs(t)
	if len(configsAfterFirst) != len(configsAfterSecond) {
		t.Errorf("config count changed between runs: %d → %d",
			len(configsAfterFirst), len(configsAfterSecond))
	}
}

// ---------------------------------------------------------------------------
// A6 — Existing config behavior (IMP-ACC-006, IMP-ACC-014)
// IMP-ACC-018, IMP-ACC-019, IMP-ACC-020, IMP-ACC-021, IMP-ACC-022
// ---------------------------------------------------------------------------

// IMP-ACC-006
func TestIMP_ACC_006_SkipExistingWithoutOverwrite(t *testing.T) {
	id := runID()
	ns := coNamespace(t)
	ssName := "e2e-ss-" + id
	ssbName := "e2e-exist-" + id

	// IMP-ACC-018: create own CO resources.
	createScanSetting(t, ns, ssName, "0 2 * * *")
	createScanSettingBinding(t, ns, ssbName, ssName, []string{"ocp4-cis"})

	// IMP-ACC-020: clean up ALL resources via t.Cleanup, even on failure.
	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", ssbName)
		deleteResource(t, ns, "scansettings", ssName)
		cleanupACSConfigByName(t, ssbName)
	})

	// First run: create the ACS config.
	setupReport := filepath.Join(t.TempDir(), "setup.json")
	setupArgs := append(commonArgs(t), "--report-json", setupReport)
	runImporter(t, setupArgs...)

	// Second run: without --overwrite-existing (default create-only mode).
	reportPath := filepath.Join(t.TempDir(), "skip-existing.json")
	secondArgs := append(commonArgs(t), "--report-json", reportPath)

	_, _, exitCode := runImporter(t, secondArgs...)
	if exitCode != 0 && exitCode != 2 {
		t.Fatalf("expected exit code 0 or 2, got %d", exitCode)
	}

	r := parseReport(t, reportPath)

	// Mode should be create-only.
	if r.Meta.Mode != "create-only" {
		t.Errorf("expected mode=create-only, got %q", r.Meta.Mode)
	}

	// Existing configs should be skipped.
	if r.Counts.Skip == 0 && r.Counts.Discovered > 0 {
		t.Error("expected at least one skip for existing configs, got 0")
	}

	// problems[] should contain conflict entries for skipped configs.
	hasConflict := false
	for _, p := range r.Problems {
		if p.Category == "conflict" {
			hasConflict = true
			if p.Description == "" {
				t.Error("conflict problem has empty description")
			}
			if p.FixHint == "" {
				t.Error("conflict problem has empty fixHint")
			}
		}
	}
	if r.Counts.Skip > 0 && !hasConflict {
		t.Error("skipped existing configs but no conflict entries found in problems[]")
	}
}

// IMP-ACC-014
func TestIMP_ACC_014_OverwriteExistingUpdates(t *testing.T) {
	id := runID()
	ns := coNamespace(t)
	ssName := "e2e-ss-" + id
	ssbName := "e2e-exist-" + id

	// IMP-ACC-018: create own CO resources.
	createScanSetting(t, ns, ssName, "0 2 * * *")
	createScanSettingBinding(t, ns, ssbName, ssName, []string{"ocp4-cis"})

	// IMP-ACC-020: clean up ALL resources via t.Cleanup, even on failure.
	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", ssbName)
		deleteResource(t, ns, "scansettings", ssName)
		cleanupACSConfigByName(t, ssbName)
	})

	// First run: create the ACS config.
	setupReport := filepath.Join(t.TempDir(), "setup.json")
	setupArgs := append(commonArgs(t), "--report-json", setupReport)
	runImporter(t, setupArgs...)

	// Second run: with --overwrite-existing.
	reportPath := filepath.Join(t.TempDir(), "overwrite.json")
	overwriteArgs := append(commonArgs(t),
		"--overwrite-existing",
		"--report-json", reportPath,
	)

	_, _, exitCode := runImporter(t, overwriteArgs...)
	if exitCode != 0 && exitCode != 2 {
		t.Fatalf("expected exit code 0 or 2, got %d", exitCode)
	}

	r := parseReport(t, reportPath)

	// Mode should be create-or-update.
	if r.Meta.Mode != "create-or-update" {
		t.Errorf("expected mode=create-or-update, got %q", r.Meta.Mode)
	}

	// With --overwrite-existing, our SSB should show action=update, not skip.
	foundUpdate := false
	for _, item := range r.Items {
		if item.Source.BindingName == ssbName {
			if item.Action == "update" {
				foundUpdate = true
			} else if item.Action == "skip" {
				t.Errorf("expected action=update for %q with --overwrite-existing, got skip", ssbName)
			}
		}
	}
	if !foundUpdate {
		t.Logf("warning: did not find action=update for %q in report items", ssbName)
	}
}

// ---------------------------------------------------------------------------
// A7 — Failure paths (IMP-ACC-007)
// ---------------------------------------------------------------------------

// IMP-ACC-007
func TestIMP_ACC_007_InvalidTokenFailsFast(t *testing.T) {
	endpoint := requireEnv(t, "ROX_ENDPOINT")

	// Run with an invalid token by overriding the environment.
	bin := importerBin(t)
	cmd := exec.Command(bin,
		"--endpoint", endpoint,
		"--insecure-skip-verify",
		"--dry-run",
	)

	// Set up environment with a bogus token and clear basic auth to avoid
	// ambiguous auth errors.
	cmd.Env = append(os.Environ(),
		"ROX_API_TOKEN=invalid-token-that-should-fail",
	)
	// Remove ROX_ADMIN_PASSWORD to avoid ambiguous auth.
	filtered := make([]string, 0, len(cmd.Env))
	for _, e := range cmd.Env {
		if !strings.HasPrefix(e, "ROX_ADMIN_PASSWORD=") {
			filtered = append(filtered, e)
		}
	}
	cmd.Env = filtered

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected importer to fail with invalid token, but got exit code 0")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected error type: %v", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("expected exit code 1 for invalid auth, got %d\nstdout: %s\nstderr: %s",
			exitErr.ExitCode(), outBuf.String(), errBuf.String())
	}
}

// ---------------------------------------------------------------------------
// A9 — Auto-discovery (IMP-ACC-017)
// IMP-ACC-018, IMP-ACC-019, IMP-ACC-020, IMP-ACC-021, IMP-ACC-022
// ---------------------------------------------------------------------------

// IMP-ACC-017
func TestIMP_ACC_017_AutoDiscovery(t *testing.T) {
	id := runID()
	ns := coNamespace(t)
	ssName := "e2e-ss-" + id
	ssbName := "e2e-disco-" + id

	// IMP-ACC-018: create own CO resources.
	createScanSetting(t, ns, ssName, "0 2 * * *")
	createScanSettingBinding(t, ns, ssbName, ssName, []string{"ocp4-cis"})

	// IMP-ACC-020: clean up ALL resources via t.Cleanup, even on failure.
	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", ssbName)
		deleteResource(t, ns, "scansettings", ssName)
		cleanupACSConfigByName(t, ssbName)
	})

	// Run in dry-run mode (no writes) without providing an explicit cluster ID.
	// The importer should auto-discover the cluster ID from the
	// admission-control ConfigMap's cluster-id key.
	reportPath := filepath.Join(t.TempDir(), "autodiscovery.json")
	args := append(commonArgs(t),
		"--dry-run",
		"--report-json", reportPath,
	)

	stdout, stderr, exitCode := runImporter(t, args...)

	if exitCode != 0 && exitCode != 2 {
		t.Fatalf("expected exit code 0 or 2, got %d\nstdout: %s\nstderr: %s",
			exitCode, stdout, stderr)
	}

	r := parseReport(t, reportPath)

	// With auto-discovery working, we expect at least some discovered bindings.
	if r.Counts.Discovered == 0 {
		t.Errorf("auto-discovery: 0 bindings discovered — cluster ID may not have been resolved\nstdout: %s\nstderr: %s",
			stdout, stderr)
	}

	fmt.Fprintf(os.Stderr, "auto-discovery report: discovered=%d, create=%d, skip=%d, failed=%d\n",
		r.Counts.Discovered, r.Counts.Create, r.Counts.Skip, r.Counts.Failed)
}
