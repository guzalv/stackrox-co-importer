package adopt

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeK8sClient implements K8sClient for tests.
type fakeK8sClient struct {
	exists    map[string]bool  // "ctx/ns/name" -> exists
	existsErr map[string]error // "ctx/ns/name" -> error
	patchErr  map[string]error // "ctx/ns/ssb" -> error
	patchLog  []string         // records "ctx/ns/ssb->target" calls
}

func newFakeK8s() *fakeK8sClient {
	return &fakeK8sClient{
		exists:    make(map[string]bool),
		existsErr: make(map[string]error),
		patchErr:  make(map[string]error),
	}
}

func (f *fakeK8sClient) ScanSettingExists(ctx, ns, name string) (bool, error) {
	k := ctx + "/" + ns + "/" + name
	if err, ok := f.existsErr[k]; ok {
		return false, err
	}
	return f.exists[k], nil
}

func (f *fakeK8sClient) PatchSSBSettingsRef(ctx, ns, ssbName, newRef string) error {
	k := ctx + "/" + ns + "/" + ssbName
	f.patchLog = append(f.patchLog, k+"->"+newRef)
	if err, ok := f.patchErr[k]; ok {
		return err
	}
	return nil
}

// IMP-ADOPT-003
func TestRunAdoption_AlreadyAdopted(t *testing.T) {
	k8s := newFakeK8s()
	req := AdoptionRequest{
		Context: "ctx-a", Namespace: "ns", SSBName: "my-ssb",
		CurrentSetting: "acs-managed-ss", TargetSetting: "acs-managed-ss",
	}

	result := RunAdoption(k8s, []AdoptionRequest{req}, time.Second)

	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
	if len(k8s.patchLog) != 0 {
		t.Errorf("expected no patches, got: %v", k8s.patchLog)
	}
	if len(result.InfoLogs) != 1 {
		t.Errorf("expected 1 info log for skip, got %d", len(result.InfoLogs))
	}
}

// IMP-ADOPT-001, IMP-ADOPT-002
func TestRunAdoption_HappyPath(t *testing.T) {
	k8s := newFakeK8s()
	k8s.exists["ctx-a/ns/acs-managed-ss"] = true

	req := AdoptionRequest{
		Context: "ctx-a", Namespace: "ns", SSBName: "my-ssb",
		CurrentSetting: "old-ss", TargetSetting: "acs-managed-ss",
	}

	result := RunAdoption(k8s, []AdoptionRequest{req}, time.Second)

	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", result.Warnings)
	}
	if len(k8s.patchLog) != 1 || k8s.patchLog[0] != "ctx-a/ns/my-ssb->acs-managed-ss" {
		t.Errorf("unexpected patch log: %v", k8s.patchLog)
	}
	if len(result.InfoLogs) != 1 || !strings.Contains(result.InfoLogs[0], "adopted") {
		t.Errorf("expected adoption info log, got: %v", result.InfoLogs)
	}
}

// IMP-ADOPT-004..006
func TestRunAdoption_ScanSettingNotYetExist(t *testing.T) {
	k8s := newFakeK8s()
	// exists returns false (default for missing key)

	req := AdoptionRequest{
		Context: "ctx-a", Namespace: "ns", SSBName: "my-ssb",
		CurrentSetting: "old-ss", TargetSetting: "acs-managed-ss",
	}

	result := RunAdoption(k8s, []AdoptionRequest{req}, 50*time.Millisecond)

	if result.Err != nil {
		t.Errorf("unexpected fatal error: %v", result.Err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "timeout") {
		t.Errorf("expected timeout warning, got: %v", result.Warnings)
	}
	if len(k8s.patchLog) != 0 {
		t.Error("should not patch when ScanSetting doesn't exist")
	}
}

// K8s check error → warn and continue
func TestRunAdoption_K8sCheckError(t *testing.T) {
	k8s := newFakeK8s()
	k8s.existsErr["ctx-a/ns/acs-managed-ss"] = errors.New("k8s API error")

	req := AdoptionRequest{
		Context: "ctx-a", Namespace: "ns", SSBName: "my-ssb",
		CurrentSetting: "old-ss", TargetSetting: "acs-managed-ss",
	}

	result := RunAdoption(k8s, []AdoptionRequest{req}, time.Second)

	if result.Err != nil {
		t.Errorf("unexpected fatal error: %v", result.Err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "k8s API error") {
		t.Errorf("expected k8s error warning, got: %v", result.Warnings)
	}
}

// Patch error → warn and continue, no fatal error
func TestRunAdoption_PatchError(t *testing.T) {
	k8s := newFakeK8s()
	k8s.exists["ctx-a/ns/acs-managed-ss"] = true
	k8s.patchErr["ctx-a/ns/my-ssb"] = errors.New("patch forbidden")

	req := AdoptionRequest{
		Context: "ctx-a", Namespace: "ns", SSBName: "my-ssb",
		CurrentSetting: "old-ss", TargetSetting: "acs-managed-ss",
	}

	result := RunAdoption(k8s, []AdoptionRequest{req}, time.Second)

	if result.Err != nil {
		t.Errorf("unexpected fatal error: %v", result.Err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "patch forbidden") {
		t.Errorf("expected patch error warning, got: %v", result.Warnings)
	}
}

// IMP-ADOPT-007, IMP-ADOPT-008: multiple requests processed independently
func TestRunAdoption_MultipleRequests(t *testing.T) {
	k8s := newFakeK8s()
	k8s.exists["ctx-a/ns/acs-ss"] = true
	// ctx-b: ScanSetting does not exist yet

	reqs := []AdoptionRequest{
		{Context: "ctx-a", Namespace: "ns", SSBName: "ssb-1", CurrentSetting: "old", TargetSetting: "acs-ss"},
		{Context: "ctx-b", Namespace: "ns", SSBName: "ssb-1", CurrentSetting: "old", TargetSetting: "acs-ss"},
	}

	result := RunAdoption(k8s, reqs, 50*time.Millisecond)

	if result.Err != nil {
		t.Errorf("unexpected fatal error: %v", result.Err)
	}
	// One success, one timeout warning — both processed
	if len(result.InfoLogs) != 1 {
		t.Errorf("expected 1 info log, got %d: %v", len(result.InfoLogs), result.InfoLogs)
	}
	if len(result.Warnings) != 1 {
		t.Errorf("expected 1 warning, got %d: %v", len(result.Warnings), result.Warnings)
	}
}
