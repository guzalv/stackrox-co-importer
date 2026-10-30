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
// IMP-ACC-010: this entire e2e suite runs against a stock, unmodified
// StackRox + Compliance Operator install (see hack/ci-setup-kind-stackrox.sh,
// which installs both products from their published manifests/images with no
// importer-specific patches). The importer only talks to the public CO/K8s
// API and the public ACS API as an external client — it never requires
// Sensor or Central source changes. That this whole suite passes against an
// unmodified install is the evidence for that non-goal.
// ---------------------------------------------------------------------------

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
//
// Auth env vars are normalized: both ROX_API_TOKEN and ROX_ADMIN_PASSWORD are
// stripped from the inherited environment, then exactly one is re-added (token
// takes precedence). This prevents the IMP-CLI-025 "ambiguous auth" error when
// both are set in the shell, without requiring callers to manage env state.
func runImporter(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	bin := importerBin(t)

	cmd := exec.Command(bin, args...)

	// Build a clean environment: pass everything except auth vars, then add one.
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "ROX_API_TOKEN=") &&
			!strings.HasPrefix(e, "ROX_ADMIN_PASSWORD=") &&
			!strings.HasPrefix(e, "ROX_ADMIN_USER=") {
			env = append(env, e)
		}
	}
	if token := os.Getenv("ROX_API_TOKEN"); token != "" {
		env = append(env, "ROX_API_TOKEN="+token)
	} else if password := os.Getenv("ROX_ADMIN_PASSWORD"); password != "" {
		env = append(env, "ROX_ADMIN_PASSWORD="+password)
		if user := os.Getenv("ROX_ADMIN_USER"); user != "" {
			env = append(env, "ROX_ADMIN_USER="+user)
		}
	}
	cmd.Env = env

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

// acsCluster is a subset of the ACS cluster list response.
type acsCluster struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// decoyClusterName returns a registered ACS cluster name that is NOT the
// cluster under test (CO_CLUSTER_NAME/"ga-ocp-cron-2" by default). Creating
// a scan config targeting this cluster satisfies "ACS already has this
// scanName" for the reconcile pre-list, WITHOUT Sensor on the cluster under
// test ever being asked to materialize anything for it (see
// TestIMP_ACC_006_SkipExistingWithoutOverwrite's comment for why that
// matters: Sensor immediately replaces any ScanSettingBinding of that exact
// name with its own labeled version on any cluster a scan config targets).
func decoyClusterName(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("CO_DECOY_CLUSTER_NAME"); v != "" {
		return v
	}
	return "ga-ocp-cron"
}

// lookupClusterID returns the ACS cluster ID for clusterName, or "" if no such
// cluster is registered.
func lookupClusterID(t *testing.T, clusterName string) string {
	t.Helper()
	body := acsAPIGet(t, "/v1/clusters")
	var resp struct {
		Clusters []acsCluster `json:"clusters"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("parsing ACS cluster list: %v", err)
	}
	for _, c := range resp.Clusters {
		if c.Name == clusterName {
			return c.ID
		}
	}
	return ""
}

// resolveDecoyClusterID resolves the decoy cluster ID, but skips the test
// (rather than failing it) when no decoy cluster is registered. The decoy
// pattern needs a second registered ACS cluster with its own Sensor-synced
// profiles — available on the real multi-cluster OCP test env, but not on the
// single-cluster kind CI setup. Making kind provide a faithful decoy requires
// a genuinely separate second kind cluster + Sensor (cross-cluster networking),
// tracked as a follow-up spike. Until then these tests only run where a decoy
// exists.
func resolveDecoyClusterID(t *testing.T) string {
	t.Helper()
	clusterName := decoyClusterName(t)
	if id := lookupClusterID(t, clusterName); id != "" {
		return id
	}
	t.Skipf("decoy cluster %q not registered in ACS; this test needs a second "+
		"cluster (set CO_DECOY_CLUSTER_NAME, or run against a multi-cluster env)", clusterName)
	return ""
}

// createACSConfigDirect creates an ACS scan configuration directly via the
// API (bypassing the importer binary and its adopt-external workflow), so
// tests can set up an "already exists in ACS" precondition independent of
// the CO-side ScanSettingBinding's label/adoption state (IMP-ACC-006,
// IMP-ACC-014). Returns the created config's ID.
func createACSConfigDirect(t *testing.T, scanName string, profiles []string, clusterID string) string {
	t.Helper()
	endpoint := requireEnv(t, "ROX_ENDPOINT")

	payload := map[string]interface{}{
		"scanName": scanName,
		"scanConfig": map[string]interface{}{
			"oneTimeScan": false,
			"profiles":    profiles,
			"scanSchedule": map[string]interface{}{
				"intervalType": "DAILY",
				"hour":         2,
				"minute":       0,
			},
			"description": "created directly via API for e2e test",
		},
		"clusters": []string{clusterID},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshalling ACS create payload: %v", err)
	}

	url := strings.TrimRight(endpoint, "/") + "/v2/compliance/scan/configurations"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	setAuth(t, req)

	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatalf("ACS API POST scan configurations: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ACS API POST scan configurations returned %d: %s", resp.StatusCode, string(respBody))
	}

	var created acsScanConfig
	if err := json.Unmarshal(respBody, &created); err != nil {
		t.Fatalf("parsing create response: %v", err)
	}
	return created.ID
}

// getSSB returns the raw kubectl get output for a ScanSettingBinding, or ""
// if it doesn't exist. Used to assert a binding was NOT modified.
func getSSB(t *testing.T, namespace, name string) string {
	t.Helper()
	cmd := exec.Command("kubectl", "get", "scansettingbindings.compliance.openshift.io", name,
		"-n", namespace, "-o", "yaml")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return string(out)
}

// waitForSSBContains polls the live ScanSettingBinding until its YAML contains
// substr, or the timeout elapses. Sensor materializes its own labeled SSB
// asynchronously after a scan config is created, so callers that assert on the
// Sensor-created object (e.g. the stackrox ownership label) must wait for it
// rather than reading once immediately. Returns the last-seen YAML.
func waitForSSBContains(t *testing.T, namespace, name, substr string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for {
		last = getSSB(t, namespace, name)
		if strings.Contains(last, substr) {
			return last
		}
		if time.Now().After(deadline) {
			return last
		}
		time.Sleep(2 * time.Second)
	}
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

	// Dry-run never records a "gitops" problem (IMP-ADEXT-014), so exit code
	// must be 0.
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
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

	// IMP-ACC-018: create own CO resources. Uses a profile not already
	// claimed by another scan config on this shared cluster (IMP-ACC-021).
	createScanSetting(t, ns, ssName, "0 2 * * *")
	createScanSettingBinding(t, ns, ssbName, ssName, []string{"ocp4-pci-dss"})

	// IMP-ACC-020: clean up ALL resources via t.Cleanup, even on failure.
	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", ssbName)
		deleteResource(t, ns, "scansettings", ssName)
		cleanupACSConfigByName(t, ssbName)
	})

	reportPath := filepath.Join(t.TempDir(), "apply-report.json")
	// IMP-ACC-028: a freshly-authored SSB has no stackrox label, so RHACS
	// >=4.11/5.0 blocks its own create by construction; --delete-existing
	// --yes resolves the (uncontested, no-GitOps-involved) blocker so this
	// test can exercise the actual create path (IMP-ACC-029).
	// --backup-dir is pinned to a temp dir so the default (CWD-relative,
	// timestamped) path never litters the working directory.
	args := append(commonArgs(t),
		"--delete-existing", "--yes",
		"--backup-dir", filepath.Join(t.TempDir(), "backup"),
		"--report-json", reportPath,
	)

	// No GitOps tool is involved here, so the blocking SSB can't reappear:
	// expect a clean success.
	stdout, stderr, exitCode := runImporter(t, args...)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	r := parseReport(t, reportPath)

	// Expect at least one discovered binding (the one we created).
	if r.Counts.Discovered == 0 {
		t.Fatalf("report shows 0 discovered bindings — expected at least one\nstdout: %s\nstderr: %s", stdout, stderr)
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
		reportJSON, _ := json.MarshalIndent(r, "", "  ")
		t.Errorf("report does not show action=create for %q\nreport: %s\nstderr: %s", ssbName, reportJSON, stderr)
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

	// IMP-ACC-018: create own CO resources. Uses a profile not already
	// claimed by another scan config on this shared cluster (IMP-ACC-021).
	createScanSetting(t, ns, ssName, "0 2 * * *")
	createScanSettingBinding(t, ns, ssbName, ssName, []string{"ocp4-pci-dss"})

	// IMP-ACC-020: clean up ALL resources via t.Cleanup, even on failure.
	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", ssbName)
		deleteResource(t, ns, "scansettings", ssName)
		cleanupACSConfigByName(t, ssbName)
	})

	// First run: apply. IMP-ACC-028/029: needs --delete-existing --yes to
	// actually create (see TestIMP_ACC_004_ApplyCreatesConfigs).
	// --backup-dir is pinned to a temp dir (see TestIMP_ACC_004).
	reportPath1 := filepath.Join(t.TempDir(), "first-run.json")
	firstArgs := append(commonArgs(t),
		"--delete-existing", "--yes",
		"--backup-dir", filepath.Join(t.TempDir(), "backup"),
		"--report-json", reportPath1,
	)

	// No GitOps tool is involved here, so the blocking SSB can't reappear:
	// expect a clean success.
	_, _, exitCode := runImporter(t, firstArgs...)
	if exitCode != 0 {
		t.Fatalf("first run: expected exit code 0, got %d", exitCode)
	}

	// Snapshot configs after first run.
	configsAfterFirst := listACSConfigs(t)

	// Second run: apply again, no extra flags. IMP-ADEXT-013: the source SSB
	// is now stackrox-labeled (ACS/Sensor materialized its own after the
	// first run), so it's excluded from discovery entirely — it may not
	// appear in items[] at all, which is the correct steady state once
	// adopted, not a bug. The idempotency guarantee this test actually
	// verifies is "zero new creates, zero net change in ACS".
	reportPath2 := filepath.Join(t.TempDir(), "second-run.json")
	secondArgs := append(commonArgs(t), "--report-json", reportPath2)

	_, _, exitCode = runImporter(t, secondArgs...)
	if exitCode != 0 {
		t.Fatalf("second run: expected exit code 0, got %d", exitCode)
	}

	r := parseReport(t, reportPath2)

	// Second run should have no new creates — everything should be skipped
	// (or no longer discovered at all, per the note above).
	if r.Counts.Create != 0 {
		t.Errorf("second run created %d configs — expected 0 (idempotent)", r.Counts.Create)
	}

	for _, item := range r.Items {
		if item.Source.BindingName == ssbName && item.Action == "create" {
			t.Errorf("second run has action=create for %q — expected skip or no entry", ssbName)
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
//
// Tests the "ACS already has this scanName" skip path in isolation from the
// adopt-external workflow: the ACS scan config is created directly via the
// API (not through the importer), while the matching CO ScanSettingBinding
// stays external (unadopted, no stackrox label). The importer's internal
// dry-run "peek" at the reconcile outcome finds the scanName already exists
// and returns skip immediately — this run must never touch the external
// ScanSettingBinding, since no write to ACS is needed for it at all.
func TestIMP_ACC_006_SkipExistingWithoutOverwrite(t *testing.T) {
	id := runID()
	ns := coNamespace(t)
	ssbName := "e2e-exist-" + id

	// Target the ACS config at a DIFFERENT registered cluster (not the one
	// under test): Sensor on the cluster under test would otherwise
	// immediately materialize its own labeled ScanSettingBinding the moment
	// a scan config exists targeting it, overwriting the plain external one
	// this test creates below before the importer ever runs. Pointing the
	// config elsewhere still satisfies "ACS already has this scanName" for
	// the importer's pre-list check (which doesn't look at target
	// clusters), without Sensor on the cluster under test ever being
	// involved. Uses a profile not already claimed by other scan configs on
	// this shared cluster (IMP-ACC-021 test isolation).
	clusterID := resolveDecoyClusterID(t)
	acsID := createACSConfigDirect(t, ssbName, []string{"ocp4-moderate"}, clusterID)
	t.Cleanup(func() {
		deleteACSConfig(t, acsID)
	})

	createScanSettingBinding(t, ns, ssbName, "default", []string{"ocp4-moderate"})
	ssbBefore := getSSB(t, ns, ssbName)
	// IMP-ACC-020: register cleanup for the SSB immediately, before any call
	// that might fail/t.Fatalf, so it's never leaked on a failure.
	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", ssbName)
	})

	reportPath := filepath.Join(t.TempDir(), "skip-existing.json")
	args := append(commonArgs(t), "--report-json", reportPath)

	// A plain "already exists" skip is a "conflict" problem, not "gitops",
	// so IMP-CLI-019's gitops-forces-exit-2 rule does not apply here.
	_, _, exitCode := runImporter(t, args...)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	r := parseReport(t, reportPath)

	if r.Meta.Mode != "create-only" {
		t.Errorf("expected mode=create-only, got %q", r.Meta.Mode)
	}

	foundSkip := false
	for _, item := range r.Items {
		if item.Source.BindingName == ssbName {
			if item.Action != "skip" {
				t.Errorf("expected action=skip for %q, got %q", ssbName, item.Action)
			}
			foundSkip = true
		}
	}
	if !foundSkip {
		reportJSON, _ := json.MarshalIndent(r, "", "  ")
		t.Fatalf("expected an item for %q with action=skip, found none\nreport: %s", ssbName, reportJSON)
	}

	hasConflict := false
	for _, p := range r.Problems {
		if p.ResourceRef == ssbName && p.Category == "conflict" {
			hasConflict = true
			if p.Description == "" {
				t.Error("conflict problem has empty description")
			}
			if p.FixHint == "" {
				t.Error("conflict problem has empty fixHint")
			}
		}
	}
	if !hasConflict {
		t.Error("expected a conflict-category problem for the skipped config")
	}

	// The whole point: since no write was needed, the external
	// ScanSettingBinding must never have been touched.
	ssbAfter := getSSB(t, ns, ssbName)
	if ssbAfter == "" {
		t.Error("ScanSettingBinding was deleted — it should never be touched when ACS already has the scanName and --overwrite-existing is not set")
	} else if ssbAfter != ssbBefore {
		t.Error("ScanSettingBinding was modified — it should never be touched when ACS already has the scanName and --overwrite-existing is not set")
	}
}

// IMP-ACC-014
//
// With --overwrite-existing, a write (PUT) IS needed, so the external
// ScanSettingBinding becomes a genuine blocker again (RHACS's conflict check
// applies to updates too, not just creates) — --delete-existing --yes is
// required for the update to succeed.
func TestIMP_ACC_014_OverwriteExistingUpdates(t *testing.T) {
	id := runID()
	ns := coNamespace(t)
	ssbName := "e2e-exist-" + id

	// Target the decoy cluster for the same reason as TestIMP_ACC_006: avoid
	// Sensor on the cluster under test materializing its own
	// ScanSettingBinding before this test creates a plain external one. Once
	// the importer's own --overwrite-existing update runs below, it
	// legitimately re-targets the config at the cluster under test, and
	// Sensor materializing its own ScanSettingBinding there at that point is
	// the correct, expected outcome.
	clusterID := resolveDecoyClusterID(t)
	createACSConfigDirect(t, ssbName, []string{"ocp4-moderate"}, clusterID)
	// IMP-ACC-020: register cleanup immediately, before any fallible call.
	t.Cleanup(func() {
		cleanupACSConfigByName(t, ssbName)
	})

	createScanSettingBinding(t, ns, ssbName, "default", []string{"ocp4-moderate"})
	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", ssbName)
	})

	reportPath := filepath.Join(t.TempDir(), "overwrite.json")
	args := append(commonArgs(t),
		"--overwrite-existing", "--delete-existing", "--yes",
		"--backup-dir", filepath.Join(t.TempDir(), "backup"),
		"--report-json", reportPath,
	)

	// No GitOps tool is involved here, so the blocking SSB can't reappear:
	// expect a clean success.
	_, _, exitCode := runImporter(t, args...)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	r := parseReport(t, reportPath)

	if r.Meta.Mode != "create-or-update" {
		t.Errorf("expected mode=create-or-update, got %q", r.Meta.Mode)
	}

	foundUpdate := false
	for _, item := range r.Items {
		if item.Source.BindingName == ssbName {
			if item.Action == "update" {
				foundUpdate = true
			} else if item.Action == "skip" {
				t.Errorf("expected action=update for %q with --overwrite-existing --delete-existing --yes, got skip", ssbName)
			}
		}
	}
	if !foundUpdate {
		reportJSON, _ := json.MarshalIndent(r, "", "  ")
		t.Errorf("did not find action=update for %q in report items\nreport: %s", ssbName, reportJSON)
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
// A12 — Adopt-external workflow (IMP-ACC-028, IMP-ACC-029, IMP-ACC-030)
//
// These exercise the real backup -> delete -> create (and resume) mechanism
// against a real cluster, per specs/08-adopt-external-ssb.feature
// (IMP-ADEXT-*) and AGENTS.md's real-cluster validation checkpoint. No
// GitOps tool is involved here (that race is validated manually, see
// ~/ai/current-work/stackrox-co-importer-customer-share.md) — these confirm
// the mechanism itself end-to-end: a freshly-authored, unadopted
// ScanSettingBinding is its own blocker on RHACS >=4.11/5.0 by construction.
// ---------------------------------------------------------------------------

// IMP-ACC-028
func TestIMP_ACC_028_DefaultReportsAndSkipsWithoutDeleting(t *testing.T) {
	id := runID()
	ns := coNamespace(t)
	ssName := "e2e-ss-" + id
	ssbName := "e2e-adext-" + id

	createScanSetting(t, ns, ssName, "0 2 * * *")
	createScanSettingBinding(t, ns, ssbName, ssName, []string{"ocp4-e8"})
	ssbBefore := getSSB(t, ns, ssbName)

	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", ssbName)
		deleteResource(t, ns, "scansettings", ssName)
		cleanupACSConfigByName(t, ssbName)
	})

	backupDir := filepath.Join(t.TempDir(), "backup")
	reportPath := filepath.Join(t.TempDir(), "report.json")
	args := append(commonArgs(t), "--backup-dir", backupDir, "--report-json", reportPath)

	// IMP-CLI-019: a gitops-blocked skip forces exit code 2, even though
	// nothing failed outright — the requested import did not happen.
	_, _, exitCode := runImporter(t, args...)
	if exitCode != 2 {
		t.Fatalf("expected exit code 2 (gitops-blocked), got %d", exitCode)
	}

	r := parseReport(t, reportPath)

	foundGitops := false
	for _, p := range r.Problems {
		if p.ResourceRef == ssbName && p.Category == "gitops" {
			foundGitops = true
			// IMP-ACC-012: the gitops problem MUST carry a remediation hint.
			if !strings.Contains(p.FixHint, "--delete-existing") {
				t.Errorf("gitops problem fixHint does not mention --delete-existing: %q", p.FixHint)
			}
		}
	}
	if !foundGitops {
		reportJSON, _ := json.MarshalIndent(r, "", "  ")
		t.Fatalf("expected a gitops-category problem for %q, found none\nreport: %s", ssbName, reportJSON)
	}

	// IMP-ACC-011: without --delete-existing, importer MUST NOT mutate CO resources —
	// the blocker is reported and skipped, never touched.
	ssbAfter := getSSB(t, ns, ssbName)
	if ssbAfter == "" {
		t.Error("ScanSettingBinding was deleted — default behavior (no --delete-existing) must never delete")
	} else if ssbAfter != ssbBefore {
		t.Error("ScanSettingBinding was modified — default behavior (no --delete-existing) must never mutate")
	}

	// No ACS config created.
	configs := listACSConfigs(t)
	for _, c := range configs {
		if c.ScanName == ssbName {
			t.Errorf("ACS scan config %q was created — default behavior must not create while blocked", ssbName)
		}
	}
}

// IMP-ACC-029
func TestIMP_ACC_029_DeleteExistingBacksUpDeletesAndCreates(t *testing.T) {
	id := runID()
	ns := coNamespace(t)
	ssName := "e2e-ss-" + id
	ssbName := "e2e-adext-" + id

	createScanSetting(t, ns, ssName, "0 2 * * *")
	createScanSettingBinding(t, ns, ssbName, ssName, []string{"ocp4-e8"})

	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", ssbName)
		deleteResource(t, ns, "scansettings", ssName)
		cleanupACSConfigByName(t, ssbName)
	})

	backupDir := filepath.Join(t.TempDir(), "backup")
	reportPath := filepath.Join(t.TempDir(), "report.json")
	args := append(commonArgs(t),
		"--delete-existing", "--yes",
		"--backup-dir", backupDir,
		"--report-json", reportPath,
	)

	// No GitOps tool is involved here, so the blocking SSB can't reappear:
	// expect a clean success (IMP-CLI-019 only forces exit 2 for a gitops
	// problem, and none should be recorded when the create succeeds).
	stdout, stderr, exitCode := runImporter(t, args...)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	// Backup MUST exist and contain a plan.json plus the backed-up resources.
	planPath := filepath.Join(backupDir, "plan.json")
	if _, err := os.Stat(planPath); err != nil {
		t.Errorf("expected plan.json at %s: %v", planPath, err)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("reading backup dir: %v", err)
	}
	foundSSBBackup := false
	for _, e := range entries {
		if strings.Contains(e.Name(), "__ssb__"+ssbName) {
			foundSSBBackup = true
		}
	}
	if !foundSSBBackup {
		t.Errorf("expected a backed-up ScanSettingBinding YAML for %q in %s", ssbName, backupDir)
	}

	r := parseReport(t, reportPath)
	foundCreate := false
	for _, item := range r.Items {
		if item.Source.BindingName == ssbName && item.Action == "create" {
			foundCreate = true
		}
	}
	if !foundCreate {
		reportJSON, _ := json.MarshalIndent(r, "", "  ")
		t.Fatalf("expected action=create for %q\nreport: %s", ssbName, reportJSON)
	}

	// The live ScanSettingBinding must now be ACS/Sensor's own (new UID,
	// stackrox label) — confirming it was genuinely deleted and recreated,
	// not left alone. Sensor materializes it asynchronously after the scan
	// config is created, so poll rather than read once.
	ssbYAML := waitForSSBContains(t, ns, ssbName, "app.kubernetes.io/name: stackrox", 60*time.Second)
	if !strings.Contains(ssbYAML, "app.kubernetes.io/name: stackrox") {
		t.Errorf("expected the live ScanSettingBinding %q to carry the stackrox label after adoption, got:\n%s", ssbName, ssbYAML)
	}

	configs := listACSConfigs(t)
	found := false
	for _, c := range configs {
		if c.ScanName == ssbName {
			found = true
		}
	}
	if !found {
		t.Errorf("ACS scan config %q not found after --delete-existing --yes", ssbName)
	}
}

// IMP-ACC-030
//
// Simulates an operator following IMP-ACC-028's guidance: a first run
// without --delete-existing backs up the plan but makes no changes; the
// operator then deletes the ScanSettingBinding themselves; a second run
// against the SAME --backup-dir must create the scan configuration from the
// persisted plan, since there is nothing left to discover live.
func TestIMP_ACC_030_ResumeFromPersistedPlan(t *testing.T) {
	id := runID()
	ns := coNamespace(t)
	ssName := "e2e-ss-" + id
	ssbName := "e2e-adext-" + id

	createScanSetting(t, ns, ssName, "0 2 * * *")
	createScanSettingBinding(t, ns, ssbName, ssName, []string{"ocp4-e8"})

	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", ssbName)
		deleteResource(t, ns, "scansettings", ssName)
		cleanupACSConfigByName(t, ssbName)
	})

	backupDir := filepath.Join(t.TempDir(), "backup")

	// First run: default behavior, writes the backup/plan but changes nothing.
	firstReport := filepath.Join(t.TempDir(), "first.json")
	firstArgs := append(commonArgs(t), "--backup-dir", backupDir, "--report-json", firstReport)
	runImporter(t, firstArgs...)

	if _, err := os.Stat(filepath.Join(backupDir, "plan.json")); err != nil {
		t.Fatalf("expected plan.json to exist after the first run: %v", err)
	}

	// Operator follows the guidance: delete the ScanSettingBinding themselves.
	deleteResource(t, ns, "scansettingbindings", ssbName)

	// Second run: same --backup-dir, no --delete-existing needed (nothing
	// left to delete). Must resume from the plan and create.
	secondReport := filepath.Join(t.TempDir(), "second.json")
	secondArgs := append(commonArgs(t), "--backup-dir", backupDir, "--report-json", secondReport)
	stdout, stderr, exitCode := runImporter(t, secondArgs...)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	r := parseReport(t, secondReport)
	foundCreate := false
	for _, item := range r.Items {
		if item.Source.BindingName == ssbName && item.Action == "create" {
			foundCreate = true
		}
	}
	if !foundCreate {
		reportJSON, _ := json.MarshalIndent(r, "", "  ")
		t.Fatalf("expected the resumed run to create %q\nstdout: %s\nreport: %s", ssbName, stdout, reportJSON)
	}

	configs := listACSConfigs(t)
	found := false
	for _, c := range configs {
		if c.ScanName == ssbName {
			found = true
		}
	}
	if !found {
		t.Errorf("ACS scan config %q not found after resume", ssbName)
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

	// Dry-run never records a "gitops" problem (IMP-ADEXT-014), so exit code
	// must be 0.
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout: %s\nstderr: %s",
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

// ---------------------------------------------------------------------------
// A1 — CO resource discovery (IMP-ACC-001)
// ---------------------------------------------------------------------------

// IMP-ACC-001
//
// Per specs/04-validation-and-acceptance.md A1: an importer run MUST begin
// only if required CO resource types are listable. This runs the same
// kubectl commands the spec names directly and asserts each exits 0 against
// the test namespace, independent of whether any resources of that type
// currently exist there.
func TestIMP_ACC_001_RequiredCOResourceTypesListable(t *testing.T) {
	ns := coNamespace(t)

	requiredTypes := []string{
		"scansettingbindings",
		"scansettings",
		"profiles",
	}

	for _, resourceType := range requiredTypes {
		fullGVR := resourceType + ".compliance.openshift.io"
		cmd := exec.Command("kubectl", "get", fullGVR, "-n", ns)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("kubectl get %s -n %s failed (exit != 0): %v\n%s", fullGVR, ns, err, string(out))
		}
	}
}

// ---------------------------------------------------------------------------
// A7 — Failure paths (IMP-ACC-008)
// IMP-ACC-018, IMP-ACC-019, IMP-ACC-020, IMP-ACC-021, IMP-ACC-022
// ---------------------------------------------------------------------------

// IMP-ACC-008
//
// A ScanSettingBinding referencing a ScanSetting that does not exist MUST
// fail only that binding: the run still processes other, valid bindings,
// but the overall exit code MUST be 2 (partial success/failure), per
// specs/04-validation-and-acceptance.md A7.
func TestIMP_ACC_008_BrokenBindingFailsOnlyItself(t *testing.T) {
	id := runID()
	ns := coNamespace(t)

	validSSName := "e2e-ss-" + id
	validSSBName := "e2e-valid-" + id
	brokenSSBName := "e2e-broken-" + id
	missingSettingName := "does-not-exist-" + id

	// IMP-ACC-018: create own CO resources.
	createScanSetting(t, ns, validSSName, "0 2 * * *")
	createScanSettingBinding(t, ns, validSSBName, validSSName, []string{"ocp4-cis"})
	// Broken binding: references a ScanSetting that is deliberately never
	// created.
	createScanSettingBinding(t, ns, brokenSSBName, missingSettingName, []string{"ocp4-cis"})

	// IMP-ACC-020: clean up ALL resources via t.Cleanup, even on failure.
	t.Cleanup(func() {
		deleteResource(t, ns, "scansettingbindings", brokenSSBName)
		deleteResource(t, ns, "scansettingbindings", validSSBName)
		deleteResource(t, ns, "scansettings", validSSName)
		cleanupACSConfigByName(t, validSSBName)
	})

	reportPath := filepath.Join(t.TempDir(), "broken-binding.json")
	// IMP-ACC-028: the valid binding is freshly authored (no stackrox
	// label), so it is its own gitops blocker by construction on
	// RHACS >=4.11/5.0; --delete-existing --yes resolves that uncontested
	// blocker so the valid binding can actually be created, isolating the
	// assertion to the broken binding's effect on the exit code.
	args := append(commonArgs(t),
		"--delete-existing", "--yes",
		"--backup-dir", filepath.Join(t.TempDir(), "backup"),
		"--report-json", reportPath,
	)

	stdout, stderr, exitCode := runImporter(t, args...)

	// The broken binding's failure MUST be what drives the exit code to 2 —
	// a partial run (some succeeded, one failed), not a clean success and
	// not a fatal preflight failure.
	if exitCode != 2 {
		t.Fatalf("expected exit code 2 (broken binding fails only itself), got %d\nstdout: %s\nstderr: %s",
			exitCode, stdout, stderr)
	}

	r := parseReport(t, reportPath)

	// The broken binding MUST be recorded as failed: either as an items[]
	// entry with action=fail, or as a problem referencing it (both forms
	// count as "marked failed" per IMP-MAP-008..011's sibling unit-level
	// contract). Either way it MUST NOT be silently dropped from the report.
	brokenFound := false
	for _, item := range r.Items {
		if item.Source.BindingName == brokenSSBName {
			brokenFound = true
			if item.Action != "fail" {
				t.Errorf("expected action=fail for broken binding %q, got %q", brokenSSBName, item.Action)
			}
		}
	}
	for _, p := range r.Problems {
		if strings.Contains(p.ResourceRef, brokenSSBName) {
			brokenFound = true
			if p.Description == "" {
				t.Error("broken-binding problem has empty description")
			}
			if p.FixHint == "" {
				t.Error("broken-binding problem has empty fixHint")
			}
		}
	}
	if !brokenFound {
		reportJSON, _ := json.MarshalIndent(r, "", "  ")
		t.Fatalf("expected %q to be recorded as failed (items[] or problems[]), found neither\nreport: %s",
			brokenSSBName, reportJSON)
	}

	// The valid binding MUST still be processed (created, per
	// --delete-existing --yes resolving its own gitops blocker).
	validProcessed := false
	for _, item := range r.Items {
		if item.Source.BindingName == validSSBName && (item.Action == "create" || item.Action == "skip") {
			validProcessed = true
		}
	}
	if !validProcessed {
		reportJSON, _ := json.MarshalIndent(r, "", "  ")
		t.Fatalf("expected valid binding %q to be processed (create or skip), found no such item\nreport: %s",
			validSSBName, reportJSON)
	}
}
