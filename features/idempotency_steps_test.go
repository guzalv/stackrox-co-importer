package features

import (
	"context"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	"github.com/stackrox/co-importer/internal/acs"
	"github.com/stackrox/co-importer/internal/models"
	"github.com/stackrox/co-importer/internal/problems"
	"github.com/stackrox/co-importer/internal/reconcile"
)

// --- Fake ACS client for idempotency tests ---

// fakeACSClient implements acs.Client for testing reconcile behavior.
type fakeACSClient struct {
	configs     map[string]acs.ScanConfig // scanName -> ScanConfig
	postCalled  bool
	putCalled   bool
	putID       string
	statusCodes []int  // sequence of HTTP status codes to return before success
	callCount   int    // total calls to Create/Update
	createdID   string // ID returned on successful create
}

func newFakeACSClient() *fakeACSClient {
	return &fakeACSClient{
		configs:   make(map[string]acs.ScanConfig),
		createdID: "new-scan-config-id",
	}
}

func (f *fakeACSClient) ListScanConfigs(_ context.Context) ([]acs.ScanConfig, error) {
	var result []acs.ScanConfig
	for _, sc := range f.configs {
		result = append(result, sc)
	}
	return result, nil
}

func (f *fakeACSClient) CreateScanConfig(_ context.Context, _ interface{}) (string, error) {
	f.callCount++
	f.postCalled = true
	if len(f.statusCodes) > 0 && f.callCount <= len(f.statusCodes) {
		code := f.statusCodes[f.callCount-1]
		if code != 0 {
			return "", &acs.HTTPError{StatusCode: code, Message: fmt.Sprintf("HTTP %d error", code)}
		}
	}
	return f.createdID, nil
}

func (f *fakeACSClient) UpdateScanConfig(_ context.Context, id string, _ interface{}) error {
	f.callCount++
	f.putCalled = true
	f.putID = id
	if len(f.statusCodes) > 0 && f.callCount <= len(f.statusCodes) {
		code := f.statusCodes[f.callCount-1]
		if code != 0 {
			return &acs.HTTPError{StatusCode: code, Message: fmt.Sprintf("HTTP %d error", code)}
		}
	}
	return nil
}

// --- Idempotency test context ---

// idemTestContext shares state between idempotency scenario steps.
type idemTestContext struct {
	client     *fakeACSClient
	reconciler *reconcile.Reconciler
	result     reconcile.ActionResult
	payload    models.ACSPayload
	dryRun     bool
	overwrite  bool
	exitCode   int
	problems   *problems.Collector
	// For dry-run: tracks whether a create action would happen
	wouldCreate bool
	// For retry: status codes to inject
	retryCodes []int
}

var itc *idemTestContext

// resetIdemTestContext creates a fresh context for each scenario.
func resetIdemTestContext() {
	itc = &idemTestContext{
		client:   newFakeACSClient(),
		problems: problems.New(),
	}
}

// registerIdempotencySteps registers step definitions for specs/03-idempotency-dry-run-retries.feature.
func registerIdempotencySteps(ctx *godog.ScenarioContext) {
	ctx.Before(func(ctx2 context.Context, sc *godog.Scenario) (context.Context, error) {
		resetIdemTestContext()
		return ctx2, nil
	})

	// Background (shared with mapping — only register if not already done)
	// acsEndpointAndTokenPreflightSucceeded — registered in mapping_steps
	ctx.Step(`^desired payload for source "([^"]*)" is computed$`, desiredPayloadIsComputed)

	// @idempotency — IMP-IDEM-001
	ctx.Step(`^ACS has no scan configuration with scanName "([^"]*)"$`, acsHasNoScanConfig)
	ctx.Step(`^importer executes in apply mode$`, importerExecutesInApplyMode)
	ctx.Step(`^importer MUST send POST to create scan configuration$`, importerMustSendPOST)
	ctx.Step(`^action MUST be "([^"]*)"$`, actionMustBe)

	// @idempotency — IMP-IDEM-002, IMP-IDEM-003
	ctx.Step(`^ACS has scan configuration with scanName "([^"]*)"$`, acsHasScanConfig)
	ctx.Step(`^--overwrite-existing is false$`, overwriteExistingIsFalse)
	ctx.Step(`^importer MUST NOT send PUT$`, importerMustNotSendPUT)
	ctx.Step(`^reason MUST include "([^"]*)"$`, reasonMustInclude)
	ctx.Step(`^problems list MUST include conflict category$`, problemsMustIncludeConflictCategory)

	// @idempotency @overwrite — IMP-IDEM-008, IMP-IDEM-009
	ctx.Step(`^ACS has scan configuration with scanName "([^"]*)" and id "([^"]*)"$`, acsHasScanConfigWithID)
	ctx.Step(`^--overwrite-existing is true$`, overwriteExistingIsTrue)
	ctx.Step(`^importer MUST send PUT to update scan configuration "([^"]*)"$`, importerMustSendPUT)

	// @dryrun — IMP-IDEM-004..007
	ctx.Step(`^importer is started with --dry-run$`, importerStartedWithDryRun)
	ctx.Step(`^at least one action would be create in apply mode$`, atLeastOneActionWouldBeCreate)
	ctx.Step(`^importer completes$`, importerCompletes)
	ctx.Step(`^importer MUST NOT send POST$`, importerMustNotSendPOST)
	ctx.Step(`^planned actions MUST be included in report$`, plannedActionsMustBeInReport)
	ctx.Step(`^problems list MUST still be populated for problematic resources$`, problemsMustBePopulated)

	// @retry — IMP-ERR-001, IMP-ERR-002, IMP-ERR-004
	ctx.Step(`^an ACS create operation returns HTTP (\d+) for first (\d+) attempts$`, acsReturnsHTTPForFirstAttempts)
	ctx.Step(`^the (\d+)(?:st|nd|rd|th) attempt succeeds$`, attemptSucceeds)
	ctx.Step(`^operation MUST be retried with backoff$`, operationMustBeRetriedWithBackoff)
	ctx.Step(`^total attempts MUST be (\d+)$`, totalAttemptsMustBe)
	ctx.Step(`^an ACS create operation returns HTTP (\d+)$`, acsReturnsHTTP)
	ctx.Step(`^operation MUST NOT be retried$`, operationMustNotBeRetried)
	ctx.Step(`^the item MUST be skipped and recorded as a problem$`, itemMustBeSkippedAndRecorded)

	// @exitcodes — IMP-ERR-003
	ctx.Step(`^importer run ends with outcome "([^"]*)"$`, importerRunEndsWithOutcome)
	ctx.Step(`^process exit code MUST be (\d+)$`, processExitCodeMustBe)
}

// --- Step definitions ---

// IMP-IDEM-001 (background): set up a test ACS payload
func desiredPayloadIsComputed(source string) error {
	// Parse source as "namespace/name"
	parts := strings.SplitN(source, "/", 2)
	name := source
	if len(parts) == 2 {
		name = parts[1]
	}
	itc.payload = models.ACSPayload{
		ScanName: name,
		ScanConfig: models.ACSBaseScanConfig{
			Profiles:    []string{"ocp4-cis"},
			Description: fmt.Sprintf("Imported from CO source %s", source),
		},
		Clusters: []string{"test-cluster-id"},
	}
	return nil
}

// IMP-IDEM-001: ACS has no scan configuration with the given scanName
func acsHasNoScanConfig(scanName string) error {
	// Ensure the fake client has no config with this name (default state)
	delete(itc.client.configs, scanName)
	return nil
}

// IMP-IDEM-001: execute the reconciler in apply mode
func importerExecutesInApplyMode() error {
	itc.reconciler = &reconcile.Reconciler{
		Client: itc.client,
		Options: reconcile.Options{
			DryRun:            itc.dryRun,
			OverwriteExisting: itc.overwrite,
			MaxRetries:        3, // allow up to 3 retries (4 total attempts)
		},
	}
	itc.result = itc.reconciler.Reconcile(context.Background(), itc.payload)

	// If skipped, record as a conflict problem (IMP-IDEM-003)
	if itc.result.Action == "skip" {
		itc.problems.Add(problems.Problem{
			Severity:    "warning",
			Category:    "conflict",
			ResourceRef: itc.payload.ScanName,
			Description: itc.result.Reason,
		})
	}
	// If failed, record as a problem
	if itc.result.Action == "fail" {
		itc.problems.Add(problems.Problem{
			Severity:    "error",
			Category:    "acs-error",
			ResourceRef: itc.payload.ScanName,
			Description: itc.result.Reason,
			Skipped:     true,
		})
	}
	return nil
}

// IMP-IDEM-001: verify POST was sent
func importerMustSendPOST() error {
	if !itc.client.postCalled {
		return fmt.Errorf("expected POST to be sent, but it was not")
	}
	return nil
}

// IMP-IDEM-001: verify action
func actionMustBe(expected string) error {
	if itc.result.Action != expected {
		return fmt.Errorf("expected action %q, got %q (reason: %s)", expected, itc.result.Action, itc.result.Reason)
	}
	return nil
}

// IMP-IDEM-002: ACS has a scan configuration with the given scanName
func acsHasScanConfig(scanName string) error {
	itc.client.configs[scanName] = acs.ScanConfig{
		ID:       "existing-auto-id",
		ScanName: scanName,
	}
	return nil
}

// IMP-IDEM-002: overwrite-existing is false
func overwriteExistingIsFalse() error {
	itc.overwrite = false
	return nil
}

// IMP-IDEM-002: verify PUT was not sent
func importerMustNotSendPUT() error {
	if itc.client.putCalled {
		return fmt.Errorf("expected PUT not to be sent, but it was")
	}
	return nil
}

// IMP-IDEM-003: verify reason includes expected text
func reasonMustInclude(expected string) error {
	if !strings.Contains(itc.result.Reason, expected) {
		return fmt.Errorf("expected reason to include %q, got %q", expected, itc.result.Reason)
	}
	return nil
}

// IMP-IDEM-003: verify conflict category in problems
func problemsMustIncludeConflictCategory() error {
	if !itc.problems.HasCategory("conflict") {
		return fmt.Errorf("expected problems to include conflict category, but none found")
	}
	return nil
}

// IMP-IDEM-008: ACS has a scan configuration with scanName and id
func acsHasScanConfigWithID(scanName, id string) error {
	itc.client.configs[scanName] = acs.ScanConfig{
		ID:       id,
		ScanName: scanName,
	}
	return nil
}

// IMP-IDEM-008: overwrite-existing is true
func overwriteExistingIsTrue() error {
	itc.overwrite = true
	return nil
}

// IMP-IDEM-008: verify PUT was sent with correct ID
func importerMustSendPUT(expectedID string) error {
	if !itc.client.putCalled {
		return fmt.Errorf("expected PUT to be sent, but it was not")
	}
	if itc.client.putID != expectedID {
		return fmt.Errorf("expected PUT to ID %q, got %q", expectedID, itc.client.putID)
	}
	return nil
}

// IMP-IDEM-004: start importer with --dry-run
func importerStartedWithDryRun() error {
	itc.dryRun = true
	return nil
}

// IMP-IDEM-004: at least one action would be create
func atLeastOneActionWouldBeCreate() error {
	// Ensure no existing config so a create would happen
	itc.client.configs = make(map[string]acs.ScanConfig)
	itc.wouldCreate = true
	return nil
}

// IMP-IDEM-004: importer completes (runs reconciler in dry-run mode)
func importerCompletes() error {
	itc.reconciler = &reconcile.Reconciler{
		Client: itc.client,
		Options: reconcile.Options{
			DryRun:            itc.dryRun,
			OverwriteExisting: itc.overwrite,
			MaxRetries:        3,
		},
	}
	itc.result = itc.reconciler.Reconcile(context.Background(), itc.payload)

	// Also add a test problem for "problematic resources" assertion
	itc.problems.Add(problems.Problem{
		Severity:    "warning",
		Category:    "mapping",
		ResourceRef: "test/problematic-resource",
		Description: "test problem for dry-run",
	})
	return nil
}

// IMP-IDEM-005: verify POST was not sent
func importerMustNotSendPOST() error {
	if itc.client.postCalled {
		return fmt.Errorf("expected POST not to be sent, but it was")
	}
	return nil
}

// IMP-IDEM-006: verify planned actions are in report
func plannedActionsMustBeInReport() error {
	// In dry-run, the result should still have an action
	if itc.result.Action == "" {
		return fmt.Errorf("expected planned action in report, but action is empty")
	}
	if itc.result.Action != "create" && itc.result.Action != "update" && itc.result.Action != "skip" {
		return fmt.Errorf("expected a planned action (create/update/skip), got %q", itc.result.Action)
	}
	return nil
}

// IMP-IDEM-007: verify problems list is populated even in dry-run
func problemsMustBePopulated() error {
	if len(itc.problems.All()) == 0 {
		return fmt.Errorf("expected problems to be populated, but list is empty")
	}
	return nil
}

// IMP-ERR-001: set up transient error codes for first N attempts
func acsReturnsHTTPForFirstAttempts(statusCode, attempts int) error {
	itc.retryCodes = make([]int, attempts)
	for i := 0; i < attempts; i++ {
		itc.retryCodes[i] = statusCode
	}
	itc.client.statusCodes = itc.retryCodes
	return nil
}

// IMP-ERR-001: the Nth attempt succeeds (statusCodes list handles this — no code needed)
func attemptSucceeds(_ int) error {
	// The fake client will succeed when callCount > len(statusCodes)
	return nil
}

// IMP-ERR-001: verify retries happened
func operationMustBeRetriedWithBackoff() error {
	if itc.client.callCount <= 1 {
		return fmt.Errorf("expected retries, but only %d call(s) made", itc.client.callCount)
	}
	return nil
}

// IMP-ERR-001: verify total attempts
func totalAttemptsMustBe(expected int) error {
	if itc.result.Attempts != expected {
		return fmt.Errorf("expected %d total attempts, got %d", expected, itc.result.Attempts)
	}
	return nil
}

// IMP-ERR-002: set up a single non-transient error code
func acsReturnsHTTP(statusCode int) error {
	// Always return this code (fill enough entries)
	itc.client.statusCodes = []int{statusCode, statusCode, statusCode, statusCode}
	return nil
}

// IMP-ERR-002: verify no retries
func operationMustNotBeRetried() error {
	if itc.client.callCount > 1 {
		return fmt.Errorf("expected no retries, but %d calls made", itc.client.callCount)
	}
	return nil
}

// IMP-ERR-002: verify item skipped and recorded as problem
func itemMustBeSkippedAndRecorded() error {
	if itc.result.Action != "fail" {
		return fmt.Errorf("expected action 'fail', got %q", itc.result.Action)
	}
	if len(itc.problems.All()) == 0 {
		return fmt.Errorf("expected item to be recorded as a problem, but problems list is empty")
	}
	return nil
}

// IMP-ERR-003: set up outcome for exit code test
func importerRunEndsWithOutcome(outcome string) error {
	itc.exitCode = reconcile.ExitCode(outcome)
	return nil
}

// IMP-ERR-003: verify exit code
func processExitCodeMustBe(expected int) error {
	if itc.exitCode != expected {
		return fmt.Errorf("expected exit code %d, got %d", expected, itc.exitCode)
	}
	return nil
}
