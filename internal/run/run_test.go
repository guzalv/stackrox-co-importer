package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// missingScanSettings: names for which GetScanSetting returns an error,
	// simulating a binding that references a ScanSetting that doesn't exist.
	missingScanSettings map[string]bool
}

func (f *fakeCOClient) ListScanSettingBindings(_ context.Context) ([]models.ScanSettingBinding, error) {
	return f.ssbs, nil
}

func (f *fakeCOClient) GetScanSetting(_ context.Context, namespace, name string) (*models.ScanSetting, error) {
	if f.missingScanSettings[name] {
		return nil, fmt.Errorf("scansettings %q not found", name)
	}
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
//
// IMP-ACC-023 (exit-0 half): asserts ListSSBs returns ExitSuccess.
// IMP-ACC-024: the Runner here is built with acsClient == nil (no ACS
// credentials configured at all), confirming --list-ssbs succeeds without
// any ACS client/credentials.
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
//
// IMP-ACC-016: this is the full-pipeline (discover->merge->report) check that
// same-name SSBs across clusters with mismatched profiles produce an error
// for that SSB name, surfaced via items[]/counts/problems[].
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

// TestIMP_ACC_008_BrokenBindingFailsOnlyItself verifies that a binding whose
// referenced ScanSetting cannot be fetched fails only itself: it appears in
// items[] with action "fail", counts toward counts.failed (keeping
// discovered == created+skipped+failed), records a problem with a fixHint, and
// does not stop the other, valid binding from being processed. IMP-ACC-008,
// IMP-MAP-008..011. This is the full-pipeline counterpart to the merge-level
// Godog scenario; before the fix the broken binding was counted as discovered
// but silently dropped from items[]/counts.failed, so a run with only a broken
// binding exited 0 instead of 2.
func TestIMP_ACC_008_BrokenBindingFailsOnlyItself(t *testing.T) {
	good := ssbRef("good-binding", "ocp4-cis")
	good.ScanSettingName = "default"
	broken := ssbRef("broken-binding", "ocp4-cis")
	broken.ScanSettingName = "does-not-exist"

	cfg := &config.Config{
		DryRun:     true,
		ReportJSON: filepath.Join(t.TempDir(), "report.json"),
	}
	src := clusterSource{
		kubeconfigFile: "/fake/kubeconfig-ctx-a",
		contextName:    "ctx-a",
		clusterID:      "cluster-a",
		coClient: &fakeCOClient{
			ssbs:                []models.ScanSettingBinding{good, broken},
			scanSettings:        map[string]*models.ScanSetting{},
			missingScanSettings: map[string]bool{"does-not-exist": true},
		},
	}
	runner := newTestRunner(cfg, newFakeACSClient(), []clusterSource{src})

	exitCode := runner.Run(context.Background())
	if exitCode != ExitPartialError {
		t.Fatalf("expected exit code %d (partial: a binding failed), got %d", ExitPartialError, exitCode)
	}

	rep := readReport(t, cfg.ReportJSON)
	if rep.Counts.Discovered != 2 {
		t.Fatalf("expected discovered=2, got %d", rep.Counts.Discovered)
	}
	if rep.Counts.Failed != 1 {
		t.Fatalf("expected failed=1 (only the broken binding), got %d", rep.Counts.Failed)
	}
	sum := rep.Counts.Create + rep.Counts.Skip + rep.Counts.Failed
	if sum != rep.Counts.Discovered {
		t.Fatalf("counts do not reconcile: discovered=%d != create(%d)+skip(%d)+failed(%d)=%d",
			rep.Counts.Discovered, rep.Counts.Create, rep.Counts.Skip, rep.Counts.Failed, sum)
	}

	var brokenItem *report.Item
	for i := range rep.Items {
		if rep.Items[i].Source.BindingName == "broken-binding" {
			brokenItem = &rep.Items[i]
		}
	}
	if brokenItem == nil {
		t.Fatal("expected a items[] entry for broken-binding, found none")
	}
	if brokenItem.Action != "fail" {
		t.Errorf("expected action=fail for broken-binding, got %q", brokenItem.Action)
	}

	foundProblem := false
	for _, p := range rep.Problems {
		if strings.Contains(p.ResourceRef, "broken-binding") {
			foundProblem = true
			if p.FixHint == "" {
				t.Error("broken-binding problem has empty fixHint (IMP-MAP-011)")
			}
		}
	}
	if !foundProblem {
		t.Error("expected a problems[] entry for broken-binding")
	}

	// The valid binding must still have been processed (not blocked by the
	// broken one): it should appear as a non-fail item.
	foundGood := false
	for _, item := range rep.Items {
		if item.Source.BindingName == "good-binding" && item.Action != "fail" {
			foundGood = true
		}
	}
	if !foundGood {
		t.Error("expected good-binding to still be processed despite the broken binding")
	}
}

// TestIMP_ADEXT_016_BackupWriteFailureSurfacesHint reproduces the container
// failure where --backup-dir is not writable (image runs as USER 65534 with no
// writable volume mounted): CheckBackupDir passes because the directory does not
// exist yet, but WriteBackup's MkdirAll then fails with a permission error. The
// scan MUST fail with a remediation hint, not a raw OS error. IMP-ADEXT-016.
func TestIMP_ADEXT_016_BackupWriteFailureSurfacesHint(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("relies on filesystem permissions; root bypasses them")
	}

	// A read-only parent dir: the (non-existent) backup dir under it passes
	// CheckBackupDir (does not exist yet) but MkdirAll fails (parent not writable),
	// matching the container's EACCES-at-MkdirAll case.
	roParent := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(roParent, 0o555); err != nil {
		t.Fatalf("mkdir read-only parent: %v", err)
	}
	backupDir := filepath.Join(roParent, "co-importer-backup")

	// An unlabeled SSB is "external", and a scan takes the SSB's own name, so the
	// SSB blocks its own scan. Non-dry-run, so the importer backs it up first.
	cfg := &config.Config{
		Namespace:  "openshift-compliance",
		BackupDir:  backupDir,
		ReportJSON: filepath.Join(t.TempDir(), "report.json"),
	}
	src := testSource("ctx-a", "cluster-a", []models.ScanSettingBinding{ssbRef("cis-weekly", "ocp4-cis")})
	runner := newTestRunner(cfg, newFakeACSClient(), []clusterSource{src})

	exitCode := runner.Run(context.Background())
	if exitCode == ExitSuccess {
		t.Fatalf("expected a non-success exit code when the backup cannot be written, got %d", exitCode)
	}

	rep := readReport(t, cfg.ReportJSON)
	if rep.Counts.Failed != 1 {
		t.Fatalf("expected failed=1, got %d", rep.Counts.Failed)
	}

	var item *report.Item
	for i := range rep.Items {
		if rep.Items[i].Source.BindingName == "cis-weekly" {
			item = &rep.Items[i]
		}
	}
	if item == nil {
		t.Fatal("expected an items[] entry for cis-weekly")
	}
	if item.Action != "fail" {
		t.Errorf("expected action=fail, got %q", item.Action)
	}

	foundHint := false
	for _, p := range rep.Problems {
		if strings.Contains(p.ResourceRef, "cis-weekly") && p.FixHint != "" {
			foundHint = true
			if !strings.Contains(p.FixHint, "writable") {
				t.Errorf("backup-failure fix hint should mention a writable directory/volume, got %q", p.FixHint)
			}
			if p.Category != "gitops" {
				t.Errorf("expected problem category gitops, got %q", p.Category)
			}
		}
	}
	if !foundHint {
		t.Error("expected a problems[] entry with a fix hint for the backup write failure (IMP-ADEXT-016)")
	}
}

// TestIMP_CLI_020a_ProblemsPrintedToConsole verifies that collected problems and
// their fix hints are printed to the console (not only to the JSON report), so a
// user who did not pass --report-json still sees remediation. Before the fix the
// console only said "see problems for remediation" with no actual problems
// printed. IMP-CLI-020a.
func TestIMP_CLI_020a_ProblemsPrintedToConsole(t *testing.T) {
	good := ssbRef("good-binding", "ocp4-cis")
	good.ScanSettingName = "default"
	broken := ssbRef("broken-binding", "ocp4-cis")
	broken.ScanSettingName = "does-not-exist"

	cfg := &config.Config{
		DryRun:     true,
		ReportJSON: filepath.Join(t.TempDir(), "report.json"),
	}
	src := clusterSource{
		kubeconfigFile: "/fake/kubeconfig-ctx-a",
		contextName:    "ctx-a",
		clusterID:      "cluster-a",
		coClient: &fakeCOClient{
			ssbs:                []models.ScanSettingBinding{good, broken},
			scanSettings:        map[string]*models.ScanSetting{},
			missingScanSettings: map[string]bool{"does-not-exist": true},
		},
	}

	var console bytes.Buffer
	runner := newTestRunner(cfg, newFakeACSClient(), []clusterSource{src})
	runner.printer = status.NewWithWriter(&console)

	runner.Run(context.Background())

	rep := readReport(t, cfg.ReportJSON)
	if len(rep.Problems) == 0 {
		t.Fatal("precondition: expected at least one problem in the report")
	}

	out := console.String()
	if !strings.Contains(out, "Problems") {
		t.Errorf("expected a Problems section in console output; got:\n%s", out)
	}
	// Every problem's resourceRef and fix hint must reach the console.
	for _, p := range rep.Problems {
		if p.ResourceRef != "" && !strings.Contains(out, p.ResourceRef) {
			t.Errorf("console must mention problem resourceRef %q; got:\n%s", p.ResourceRef, out)
		}
		if p.FixHint != "" && !strings.Contains(out, p.FixHint) {
			t.Errorf("console must include fix hint %q; got:\n%s", p.FixHint, out)
		}
	}
}

// fakeAdoptK8s is an in-memory adoptK8sClient: it records deletions and reports
// every ScanSettingBinding as gone (so the settle guard never trips).
type fakeAdoptK8s struct {
	deleted []string
}

func (f *fakeAdoptK8s) DeleteScanSettingBinding(ctxName, namespace, name string) error {
	f.deleted = append(f.deleted, ctxName+"/"+namespace+"/"+name)
	return nil
}

func (f *fakeAdoptK8s) ScanSettingBindingExists(_, _, _ string) (bool, error) {
	return false, nil
}

// TestIMP_ADEXT_018_BatchedDeleteConfirmation verifies that when several scan
// configurations are blocked by external ScanSettingBindings and
// --delete-existing is set without --yes, the operator is prompted EXACTLY ONCE
// for the whole set — not once per scan — and that after confirming, every
// blocking SSB is deleted and every scan is created. IMP-ADEXT-018.
func TestIMP_ADEXT_018_BatchedDeleteConfirmation(t *testing.T) {
	// Two external (unlabeled) SSBs, each blocking its own scan configuration.
	ssbA := ssbRef("scan-a", "ocp4-cis")
	ssbB := ssbRef("scan-b", "ocp4-moderate")

	cfg := &config.Config{
		DeleteExisting: true,
		Namespace:      "openshift-compliance",
		BackupDir:      filepath.Join(t.TempDir(), "backups"),
		ReportJSON:     filepath.Join(t.TempDir(), "report.json"),
		SettleTimeout:  0,
	}

	src := testSource("ctx-a", "cluster-a", []models.ScanSettingBinding{ssbA, ssbB})
	runner := newTestRunner(cfg, newFakeACSClient(), []clusterSource{src})

	fakeK8s := &fakeAdoptK8s{}
	runner.newAdoptK8s = func(_ []clusterSource) adoptK8sClient { return fakeK8s }

	promptCount := 0
	runner.confirmer = func(_ string) (bool, error) {
		promptCount++
		return true, nil
	}

	runner.Run(context.Background())

	if promptCount != 1 {
		t.Errorf("expected exactly ONE batched confirmation prompt for 2 blocked scans, got %d", promptCount)
	}

	rep := readReport(t, cfg.ReportJSON)
	if rep.Counts.Create != 2 {
		t.Errorf("expected both blocked scans created after confirmation, got create=%d", rep.Counts.Create)
	}
	if len(fakeK8s.deleted) != 2 {
		t.Errorf("expected both blocking SSBs deleted, got %d: %v", len(fakeK8s.deleted), fakeK8s.deleted)
	}
}

// TestIMP_ADEXT_019_DeleteRoutedByClusterIDNotContext verifies that when two
// clusters share the same kubecontext name ("admin" — common in practice) but
// have distinct ACS cluster IDs, a merged scan blocked on both clusters routes
// each delete to the CORRECT cluster. Routing by context name would send both
// deletes to the same (first) cluster. The fake records the routing key passed
// to the Deleter; we assert both distinct cluster IDs appear. IMP-ADEXT-019.
func TestIMP_ADEXT_019_DeleteRoutedByClusterIDNotContext(t *testing.T) {
	// Same SSB name + profile on two clusters => one merged scan spanning both.
	// Both sources share the context name "admin" but have distinct cluster IDs.
	srcA := testSource("admin", "cluster-a", []models.ScanSettingBinding{ssbRef("shared", "ocp4-cis")})
	srcB := testSource("admin", "cluster-b", []models.ScanSettingBinding{ssbRef("shared", "ocp4-cis")})

	cfg := &config.Config{
		DeleteExisting: true,
		ConfirmYes:     true,
		Namespace:      "openshift-compliance",
		BackupDir:      filepath.Join(t.TempDir(), "backups"),
		ReportJSON:     filepath.Join(t.TempDir(), "report.json"),
		SettleTimeout:  0,
	}

	runner := newTestRunner(cfg, newFakeACSClient(), []clusterSource{srcA, srcB})
	fakeK8s := &fakeAdoptK8s{}
	runner.newAdoptK8s = func(_ []clusterSource) adoptK8sClient { return fakeK8s }

	runner.Run(context.Background())

	// Each cluster's "shared" SSB must be deleted against its OWN cluster: the
	// routing keys recorded must be the distinct cluster IDs, not "admin" twice.
	routed := map[string]bool{}
	for _, d := range fakeK8s.deleted {
		routed[strings.SplitN(d, "/", 2)[0]] = true
	}
	for _, want := range []string{"cluster-a", "cluster-b"} {
		if !routed[want] {
			t.Errorf("expected a delete routed to cluster %q; routing keys recorded: %v", want, fakeK8s.deleted)
		}
	}
	if routed["admin"] {
		t.Errorf("delete was routed by context name %q instead of cluster ID: %v", "admin", fakeK8s.deleted)
	}

	rep := readReport(t, cfg.ReportJSON)
	if rep.Counts.Create != 1 {
		t.Errorf("expected the merged scan created once, got create=%d", rep.Counts.Create)
	}
}
