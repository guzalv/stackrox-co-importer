package features

import (
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// registerSettleSteps registers step definitions for the "Settle-timeout
// guard against GitOps self-heal" section of specs/08-adopt-external-ssb.feature
// (IMP-ADEXT-006, IMP-ADEXT-007). It shares mappingTestContext (mtc) with
// registerMappingSteps/registerBackupSteps/registerDeleteConfirmSteps. Only
// the single-cluster scenarios are covered here; the multi-cluster all-clear
// gate (IMP-ADEXT-008) is separate follow-up work.
func registerSettleSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^"--delete-existing" is set and confirmed$`, deleteExistingIsSetAndConfirmed)
	ctx.Step(`^ScanSettingBinding "([^"]*)" reappears within the settle timeout after deletion$`, ssbReappearsWithinTheSettleTimeout)
	ctx.Step(`^ScanSettingBinding "([^"]*)" does NOT reappear within the settle timeout$`, ssbDoesNotReappearWithinTheSettleTimeout)
	ctx.Step(`^the problem description MUST mention the ScanSettingBinding reappeared$`, problemDescriptionMustMentionReappeared)
	ctx.Step(`^no ACS scan configuration create or update call MUST be attempted for "([^"]*)"$`, noACSCreateOrUpdateCallMustBeAttemptedFor)
	ctx.Step(`^the importer MUST proceed to create ACS scan configuration "([^"]*)"$`, importerMustProceedToCreateACSScanConfig)
}

// fakeExister is a test double for adoptexternal.Exister. It reports a
// fixed, pre-configured answer on every call so scenarios can simulate a
// ScanSettingBinding reappearing (GitOps self-heal) or staying gone, without
// talking to a real cluster.
type fakeExister struct {
	exists bool
	calls  int
	err    error
}

func newFakeExister(exists bool) *fakeExister {
	return &fakeExister{exists: exists}
}

func (f *fakeExister) ScanSettingBindingExists(_, _, _ string) (bool, error) {
	f.calls++
	return f.exists, f.err
}

// instantPoll is injected in place of time.Sleep so SettleGuard's polling
// loop runs deterministically and fast in tests, without waiting out the
// settle timeout for real.
func instantPoll(time.Duration) {}

// --- IMP-ADEXT-004/006/007: "--delete-existing" is set and confirmed ---

func deleteExistingIsSetAndConfirmed() error {
	// IMP-ADEXT-006, IMP-ADEXT-007: combines --delete-existing with an
	// operator confirming the prompt, so the settle-timeout scenarios can
	// focus on what happens after the blocker is deleted.
	mtc.adextDeleteExisting = true
	mtc.adextConfirmer.answer = true
	return nil
}

// --- IMP-ADEXT-006, IMP-ADEXT-007: Reappearance during the settle window ---

func ssbReappearsWithinTheSettleTimeout(_ string) error {
	mtc.adextExister = newFakeExister(true)
	mtc.adextSettleTimeout = 2 * time.Second
	mtc.adextPoll = instantPoll
	return nil
}

func problemDescriptionMustMentionReappeared() error {
	for _, p := range mtc.problems.All() {
		if strings.Contains(p.Description, "reappeared") {
			return nil
		}
	}
	return fmt.Errorf("no problem description mentions the ScanSettingBinding reappeared")
}

func noACSCreateOrUpdateCallMustBeAttemptedFor(name string) error {
	// IMP-ADEXT-006, IMP-ADEXT-007: there is no create/update wiring yet
	// (separate follow-up work), so this asserts the orchestration outcome
	// that would prevent one from ever being attempted: ResolveBlocker
	// reported the blockers are not confirmed gone, and the scan is skipped.
	if mtc.adextDeleted {
		return fmt.Errorf("expected %q's blockers to not be confirmed gone, but ResolveBlocker reported they were", name)
	}
	if !mtc.skipped[name] {
		return fmt.Errorf("expected %q to be skipped (no create attempted), but it was not", name)
	}
	return nil
}

// --- IMP-ADEXT-006: No reappearance within the settle window ---

func ssbDoesNotReappearWithinTheSettleTimeout(_ string) error {
	mtc.adextExister = newFakeExister(false)
	mtc.adextSettleTimeout = 2 * time.Second
	mtc.adextPoll = instantPoll
	return nil
}

func importerMustProceedToCreateACSScanConfig(name string) error {
	// IMP-ADEXT-006: there is no create wiring yet (separate follow-up
	// work), so this asserts the orchestration signal a caller would use to
	// proceed: ResolveBlocker reported the blockers are confirmed gone, and
	// the scan was not skipped.
	if mtc.adextResolveErr != nil {
		return fmt.Errorf("expected no error resolving %q's blockers, got: %w", name, mtc.adextResolveErr)
	}
	if !mtc.adextDeleted {
		return fmt.Errorf("expected %q's blockers to be confirmed gone so the create may proceed, but ResolveBlocker reported they were not", name)
	}
	if mtc.skipped[name] {
		return fmt.Errorf("expected %q to not be skipped, since no reappearance was detected", name)
	}
	return nil
}
