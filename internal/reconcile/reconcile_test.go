package reconcile

import (
	"context"
	"errors"
	"testing"

	"github.com/stackrox/co-importer/internal/acs"
	"github.com/stackrox/co-importer/internal/models"
)

// fakeACSClient implements acs.Client for reconcile tests.
type fakeACSClient struct {
	existing    []acs.ScanConfig
	listErr     error
	createID    string
	createErr   error
	updateErr   error
	createCalls int
	updateCalls int
}

func (f *fakeACSClient) Preflight(_ context.Context) error { return nil }

func (f *fakeACSClient) ListScanConfigs(_ context.Context) ([]acs.ScanConfig, error) {
	return f.existing, f.listErr
}

func (f *fakeACSClient) CreateScanConfig(_ context.Context, _ interface{}) (string, error) {
	f.createCalls++
	return f.createID, f.createErr
}

func (f *fakeACSClient) UpdateScanConfig(_ context.Context, _ string, _ interface{}) error {
	f.updateCalls++
	return f.updateErr
}

func (f *fakeACSClient) ListClusters(_ context.Context) ([]acs.ACSClusterInfo, error) {
	return nil, nil
}

var testPayload = models.ACSPayload{ScanName: "test-config"}

// ── isTransient ───────────────────────────────────────────────────────────────

func TestIsTransient(t *testing.T) {
	tests := []struct {
		code      int
		transient bool
	}{
		{200, false},
		{400, false},
		{401, false},
		{403, false},
		{404, false},
		{429, true},
		{500, false},
		{502, true},
		{503, true},
		{504, true},
	}
	for _, tc := range tests {
		if got := isTransient(tc.code); got != tc.transient {
			t.Errorf("isTransient(%d) = %v, want %v", tc.code, got, tc.transient)
		}
	}
}

// ── Reconcile: skip path ──────────────────────────────────────────────────────

func TestReconcile_SkipExisting(t *testing.T) {
	client := &fakeACSClient{
		existing: []acs.ScanConfig{{ID: "existing-id", ScanName: "test-config"}},
	}
	r := &Reconciler{Client: client, Options: Options{OverwriteExisting: false}}

	result := r.Reconcile(context.Background(), testPayload)

	if result.Action != "skip" {
		t.Errorf("action: got %q, want %q", result.Action, "skip")
	}
	if result.AcsScanConfigID != "existing-id" {
		t.Errorf("ID: got %q, want %q", result.AcsScanConfigID, "existing-id")
	}
	if client.createCalls != 0 {
		t.Errorf("expected no creates, got %d", client.createCalls)
	}
}

// ── Reconcile: create dry-run ─────────────────────────────────────────────────

func TestReconcile_CreateDryRun(t *testing.T) {
	client := &fakeACSClient{}
	r := &Reconciler{Client: client, Options: Options{DryRun: true}}

	result := r.Reconcile(context.Background(), testPayload)

	if result.Action != "create" {
		t.Errorf("action: got %q, want %q", result.Action, "create")
	}
	if client.createCalls != 0 {
		t.Errorf("expected no actual creates in dry-run, got %d", client.createCalls)
	}
}

// ── Reconcile: create success ─────────────────────────────────────────────────

func TestReconcile_CreateSuccess(t *testing.T) {
	client := &fakeACSClient{createID: "new-id"}
	r := &Reconciler{Client: client, Options: Options{MaxRetries: 0}}

	result := r.Reconcile(context.Background(), testPayload)

	if result.Action != "create" {
		t.Errorf("action: got %q, want %q", result.Action, "create")
	}
	if result.AcsScanConfigID != "new-id" {
		t.Errorf("ID: got %q, want %q", result.AcsScanConfigID, "new-id")
	}
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
}

// ── Reconcile: update dry-run ─────────────────────────────────────────────────

func TestReconcile_UpdateDryRun(t *testing.T) {
	client := &fakeACSClient{
		existing: []acs.ScanConfig{{ID: "existing-id", ScanName: "test-config"}},
	}
	r := &Reconciler{Client: client, Options: Options{OverwriteExisting: true, DryRun: true}}

	result := r.Reconcile(context.Background(), testPayload)

	if result.Action != "update" {
		t.Errorf("action: got %q, want %q", result.Action, "update")
	}
	if client.updateCalls != 0 {
		t.Errorf("expected no actual updates in dry-run, got %d", client.updateCalls)
	}
}

// ── Reconcile: update success ─────────────────────────────────────────────────

func TestReconcile_UpdateSuccess(t *testing.T) {
	client := &fakeACSClient{
		existing: []acs.ScanConfig{{ID: "existing-id", ScanName: "test-config"}},
	}
	r := &Reconciler{Client: client, Options: Options{OverwriteExisting: true, MaxRetries: 0}}

	result := r.Reconcile(context.Background(), testPayload)

	if result.Action != "update" {
		t.Errorf("action: got %q, want %q", result.Action, "update")
	}
	if result.AcsScanConfigID != "existing-id" {
		t.Errorf("ID: got %q, want %q", result.AcsScanConfigID, "existing-id")
	}
	if client.updateCalls != 1 {
		t.Errorf("expected 1 update call, got %d", client.updateCalls)
	}
}

// ── Reconcile: ListScanConfigs failure ────────────────────────────────────────

func TestReconcile_ListError(t *testing.T) {
	client := &fakeACSClient{listErr: errors.New("API down")}
	r := &Reconciler{Client: client, Options: Options{}}

	result := r.Reconcile(context.Background(), testPayload)

	if result.Action != "fail" {
		t.Errorf("action: got %q, want %q", result.Action, "fail")
	}
	if result.Error == nil {
		t.Error("expected non-nil error")
	}
}

// ── executeWithRetry: non-transient fails immediately ─────────────────────────

func TestExecuteWithRetry_NonTransientNoRetry(t *testing.T) {
	calls := 0
	client := &fakeACSClient{}
	r := &Reconciler{Client: client, Options: Options{MaxRetries: 3}}

	result := r.executeWithRetry(context.Background(), func() (string, error) {
		calls++
		return "", &acs.HTTPError{StatusCode: 400, Message: "bad request"}
	})

	if result.Action != "fail" {
		t.Errorf("action: got %q, want %q", result.Action, "fail")
	}
	if calls != 1 {
		t.Errorf("expected 1 call (no retry on non-transient), got %d", calls)
	}
}

// ── executeWithRetry: transient retries up to max ─────────────────────────────

func TestExecuteWithRetry_TransientSucceedsOnRetry(t *testing.T) {
	calls := 0
	client := &fakeACSClient{}
	r := &Reconciler{Client: client, Options: Options{MaxRetries: 2}}

	result := r.executeWithRetry(context.Background(), func() (string, error) {
		calls++
		if calls < 2 {
			return "", &acs.HTTPError{StatusCode: 503, Message: "unavailable"}
		}
		return "new-id", nil
	})

	if result.Error != nil {
		t.Errorf("expected success, got error: %v", result.Error)
	}
	if result.AcsScanConfigID != "new-id" {
		t.Errorf("ID: got %q, want %q", result.AcsScanConfigID, "new-id")
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

// ── executeWithRetry: context cancelled during backoff ────────────────────────

func TestExecuteWithRetry_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before even starting

	client := &fakeACSClient{}
	r := &Reconciler{Client: client, Options: Options{MaxRetries: 3}}

	result := r.executeWithRetry(ctx, func() (string, error) {
		return "", &acs.HTTPError{StatusCode: 503, Message: "unavailable"}
	})

	if result.Action != "fail" {
		t.Errorf("action: got %q, want %q", result.Action, "fail")
	}
	if result.Error == nil {
		t.Error("expected non-nil error for cancelled context")
	}
}

// ── ExitCode ──────────────────────────────────────────────────────────────────

func TestExitCode(t *testing.T) {
	tests := []struct {
		outcome string
		want    int
	}{
		{"all successful", 0},
		{"fatal preflight failure", 1},
		{"partial binding failures", 2},
		{"unknown", 1}, // default
	}
	for _, tc := range tests {
		if got := ExitCode(tc.outcome); got != tc.want {
			t.Errorf("ExitCode(%q) = %d, want %d", tc.outcome, got, tc.want)
		}
	}
}
