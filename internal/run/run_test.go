package run

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/guzalv/stackrox-co-importer/internal/acs"
	"github.com/guzalv/stackrox-co-importer/internal/config"
	"github.com/guzalv/stackrox-co-importer/internal/models"
	"github.com/guzalv/stackrox-co-importer/internal/problems"
	"github.com/guzalv/stackrox-co-importer/internal/report"
	"github.com/guzalv/stackrox-co-importer/internal/status"
)

// --- Fakes for full-pipeline (Runner.Run) integration tests. ---
//
// These fakes let TestIMP_ACC_026_* and TestIMP_MAP_020b_* exercise the
// real discovery->merge->reconcile->report pipeline without a real
// kubeconfig or ACS server. See newTestRunner for how they're wired in via
// Runner.buildSources.

// fakeACSClient implements acs.Client with an in-memory scan config store.
type fakeACSClient struct {
	configs map[string]acs.ScanConfig
}

func newFakeACSClient() *fakeACSClient {
	return &fakeACSClient{configs: make(map[string]acs.ScanConfig)}
}

func (f *fakeACSClient) Preflight(_ context.Context) error { return nil }

func (f *fakeACSClient) ListScanConfigs(_ context.Context) ([]acs.ScanConfig, error) {
	out := make([]acs.ScanConfig, 0, len(f.configs))
	for _, c := range f.configs {
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeACSClient) CreateScanConfig(_ context.Context, _ interface{}) (string, error) {
	return "fake-id", nil
}

func (f *fakeACSClient) UpdateScanConfig(_ context.Context, _ string, _ interface{}) error {
	return nil
}

func (f *fakeACSClient) ListClusters(_ context.Context) ([]acs.ACSClusterInfo, error) {
	return nil, nil
}

// fakeCOClient implements cofetch.COClient with canned SSBs/ScanSettings.
type fakeCOClient struct {
	ssbs         []models.ScanSettingBinding
	scanSettings map[string]*models.ScanSetting
}

func (f *fakeCOClient) ListScanSettingBindings(_ context.Context) ([]models.ScanSettingBinding, error) {
	return f.ssbs, nil
}

func (f *fakeCOClient) GetScanSetting(_ context.Context, namespace, name string) (*models.ScanSetting, error) {
	if ss, ok := f.scanSettings[name]; ok {
		return ss, nil
	}
	// Default ScanSetting so tests don't need to wire one up unless they
	// care about its schedule specifically.
	return &models.ScanSetting{Namespace: namespace, Name: name, Schedule: "0 2 * * *"}, nil
}

func (f *fakeCOClient) PatchSSBSettingsRef(_ context.Context, _, _, _ string) error {
	return nil
}

// testSource builds a clusterSource backed by a fakeCOClient, for injection
// via Runner.buildSources. dynClient is intentionally left nil: none of the
// scenarios exercised by run_test.go reach the adopt-external delete path
// (dry-run peeks the action without deleting; conflicts are dropped before
// reconcile), so a nil dynamic.Interface is never dereferenced.
func testSource(contextName, clusterID string, ssbs []models.ScanSettingBinding) clusterSource {
	return clusterSource{
		kubeconfigFile: "/fake/kubeconfig-" + contextName,
		contextName:    contextName,
		clusterID:      clusterID,
		coClient:       &fakeCOClient{ssbs: ssbs, scanSettings: map[string]*models.ScanSetting{}},
	}
}

// newTestRunner builds a Runner wired to fakes: cfg, a fake ACS client, and
// a buildSources func that returns sources directly instead of discovering
// them from a real kubeconfig.
func newTestRunner(cfg *config.Config, acsClient acs.Client, sources []clusterSource) *Runner {
	r := NewRunner(cfg, acsClient)
	r.buildSources = func(_ context.Context, _ *problems.Collector) ([]clusterSource, error) {
		return sources, nil
	}
	return r
}

func readReport(t *testing.T, path string) report.Report {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading report: %v", err)
	}
	var rep report.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("parsing report: %v", err)
	}
	return rep
}

func ssbRef(name string, profiles ...string) models.ScanSettingBinding {
	refs := make([]models.ProfileRef, len(profiles))
	for i, p := range profiles {
		refs[i] = models.ProfileRef{Name: p, Kind: "Profile"}
	}
	return models.ScanSettingBinding{
		Namespace:       "openshift-compliance",
		Name:            name,
		ScanSettingName: "default",
		Profiles:        refs,
	}
}

// TestIMP_CLI_029_ListSSBsWithoutACSDoesNotPanic reproduces the real-cluster
// regression found validating the --list-ssbs-without-endpoint fix: ListSSBs
// builds a Runner with a nil ACS client, and buildClusterSources then tried to
// resolve a cluster ID via ACS.ListClusters() on that nil client, panicking.
// IMP-CLI-029 says --list-ssbs never contacts ACS, so cluster-ID discovery must
// be skipped. This test drives the real buildClusterSources via a temp
// kubeconfig (clients are lazy, so no real connection happens) and asserts
// ListSSBs returns without panicking.
func TestIMP_CLI_029_ListSSBsWithoutACSDoesNotPanic(t *testing.T) {
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	// Minimal, structurally valid kubeconfig with one context. The server is
	// never contacted (ListSSBs only lazily lists, failing gracefully).
	const kc = `apiVersion: v1
kind: Config
clusters:
- name: c1
  cluster:
    server: https://127.0.0.1:1
contexts:
- name: ctx-a
  context:
    cluster: c1
    user: u1
current-context: ctx-a
users:
- name: u1
  user:
    token: fake
`
	if err := os.WriteFile(kubeconfig, []byte(kc), 0o600); err != nil {
		t.Fatalf("writing kubeconfig: %v", err)
	}
	t.Setenv("KUBECONFIG", kubeconfig)

	cfg := &config.Config{ListSSBs: true, Namespace: "openshift-compliance"}
	var out bytes.Buffer

	// Before the fix this panicked; now it must return an exit code cleanly.
	exitCode := ListSSBs(context.Background(), cfg, &out)
	if exitCode != ExitSuccess {
		t.Fatalf("expected ExitSuccess from --list-ssbs, got %d", exitCode)
	}

	// The key invariant: with a nil ACS client (list mode), buildClusterSources
	// must build the source WITHOUT attempting cluster-ID discovery (which calls
	// ACS). It must not construct a Discoverer backed by the nil ACS client. So
	// a source is returned with an empty clusterID, and ACS is never touched.
	r := &Runner{cfg: cfg, acsClient: nil, printer: status.New()}
	sources, err := r.buildClusterSources(context.Background(), problems.New())
	if err != nil {
		t.Fatalf("buildClusterSources returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source built in list mode, got %d", len(sources))
	}
	if sources[0].clusterID != "" {
		t.Errorf("expected empty clusterID in list mode (no ACS discovery), got %q", sources[0].clusterID)
	}
}

// TestIMP_ACC_026_ExcludeCountsTowardDiscoveredNotProcessed is a regression
// guard for IMP-ACC-026: "--exclude MUST skip SSBs whose names match the
// pattern, counting them as discovered but not processing them (not in
// created, skipped, or failed counts)."
//
// This test PASSED against unchanged production code (run.go:164 sums
// totalDiscovered before filter.ExcludeSSBs runs at run.go:167), confirming
// "bug 4" is not a bug: --exclude already does not subtract from
// counts.discovered. Kept as a regression guard.
func TestIMP_ACC_026_ExcludeCountsTowardDiscoveredNotProcessed(t *testing.T) {
	ssbs := []models.ScanSettingBinding{
		ssbRef("keep-one", "ocp4-cis"),
		ssbRef("keep-two", "ocp4-cis"),
		ssbRef("exclude-me", "ocp4-cis"),
	}
	cfg := &config.Config{
		DryRun:          true,
		ExcludePatterns: []string{"^exclude-"},
		ReportJSON:      filepath.Join(t.TempDir(), "report.json"),
	}
	sources := []clusterSource{testSource("ctx-a", "cluster-a", ssbs)}
	runner := newTestRunner(cfg, newFakeACSClient(), sources)

	exitCode := runner.Run(context.Background())
	if exitCode != ExitSuccess {
		t.Fatalf("expected ExitSuccess, got %d", exitCode)
	}

	rep := readReport(t, cfg.ReportJSON)
	if rep.Counts.Discovered != 3 {
		t.Fatalf("expected discovered=3 (pre-filter total), got %d", rep.Counts.Discovered)
	}
	processed := rep.Counts.Create + rep.Counts.Skip + rep.Counts.Failed
	if processed != 2 {
		t.Fatalf("expected 2 processed (excluded SSB must not count toward create/skip/failed), got %d (create=%d skip=%d failed=%d)",
			processed, rep.Counts.Create, rep.Counts.Skip, rep.Counts.Failed)
	}
	if len(rep.Items) != 2 {
		t.Fatalf("expected 2 items (excluded SSB has no item), got %d", len(rep.Items))
	}
}

// TestIMP_MAP_020b_CrossClusterConflictAppearsInItemsAndCounts verifies that
// a cross-cluster conflict (same scanName, mismatched profiles on two
// clusters) is a per-binding failure visible in the final report: each
// per-cluster binding involved gets an items[] entry with action "fail",
// counts.failed is incremented accordingly, and
// counts.discovered == counts.created + counts.skipped + counts.failed
// still holds (IMP-MAP-020b). The existing merge-level "conflict" problem
// (IMP-MAP-020) must still be recorded too.
func TestIMP_MAP_020b_CrossClusterConflictAppearsInItemsAndCounts(t *testing.T) {
	ssbA := ssbRef("cis-weekly", "ocp4-cis")
	ssbB := ssbRef("cis-weekly", "ocp4-cis", "ocp4-moderate") // mismatched profiles

	cfg := &config.Config{
		DryRun:     true,
		ReportJSON: filepath.Join(t.TempDir(), "report.json"),
	}
	sources := []clusterSource{
		testSource("ctx-a", "cluster-a", []models.ScanSettingBinding{ssbA}),
		testSource("ctx-b", "cluster-b", []models.ScanSettingBinding{ssbB}),
	}
	runner := newTestRunner(cfg, newFakeACSClient(), sources)

	runner.Run(context.Background())

	rep := readReport(t, cfg.ReportJSON)
	if rep.Counts.Discovered != 2 {
		t.Fatalf("expected discovered=2 (one binding per cluster), got %d", rep.Counts.Discovered)
	}
	if rep.Counts.Failed != 2 {
		t.Fatalf("expected failed=2 (one per conflicting per-cluster binding), got %d", rep.Counts.Failed)
	}
	sum := rep.Counts.Create + rep.Counts.Skip + rep.Counts.Failed
	if sum != rep.Counts.Discovered {
		t.Fatalf("IMP-MAP-020b invariant violated: discovered=%d != create(%d)+skip(%d)+failed(%d)=%d",
			rep.Counts.Discovered, rep.Counts.Create, rep.Counts.Skip, rep.Counts.Failed, sum)
	}

	var failItems int
	for _, item := range rep.Items {
		if item.Source.BindingName != "cis-weekly" {
			continue
		}
		failItems++
		if item.Action != "fail" {
			t.Errorf("expected action %q for conflicting binding, got %q", "fail", item.Action)
		}
	}
	if failItems != 2 {
		t.Errorf("expected 2 items[] entries for the conflicting binding (one per cluster), got %d", failItems)
	}

	foundConflictProblem := false
	for _, p := range rep.Problems {
		if p.Category == "conflict" && p.ResourceRef == "cis-weekly" {
			foundConflictProblem = true
		}
	}
	if !foundConflictProblem {
		t.Error("expected the existing IMP-MAP-020 conflict problem to still be recorded")
	}
}
