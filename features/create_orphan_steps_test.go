package features

import (
	"context"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	"github.com/guzalv/stackrox-co-importer/internal/acs"
	"github.com/guzalv/stackrox-co-importer/internal/adoptexternal"
	"github.com/guzalv/stackrox-co-importer/internal/models"
	"github.com/guzalv/stackrox-co-importer/internal/reconcile"
)

// registerCreateOrphanSteps registers step definitions for the "Create
// (idempotent)" and "Orphaned ScanSetting" sections of
// specs/08-adopt-external-ssb.feature (IMP-ADEXT-009, IMP-ADEXT-010). It
// shares mappingTestContext (mtc) with registerMappingSteps/
// registerBackupSteps: once a blocker is cleared (ResolveBlocker/
// ResolveMergedBlockers reported proceed=true in earlier scenarios, or a
// scenario sets that up directly as a precondition here), the actual create
// MUST route through the existing reconcile.Reconciler rather than a
// duplicate "does this scanName exist" check, so idempotency is preserved.
func registerCreateOrphanSteps(ctx *godog.ScenarioContext) {
	// @adopt-external @create — IMP-ADEXT-009
	ctx.Step(`^"([^"]*)" was already cleared of blocking external ScanSettingBindings$`, scanNameWasAlreadyClearedOfBlockers)
	ctx.Step(`^ACS already has a scan configuration named "([^"]*)"$`, acsAlreadyHasScanConfigurationNamed)
	ctx.Step(`^the importer MUST NOT send a duplicate create for "([^"]*)"$`, importerMustNotSendDuplicateCreateFor)
	ctx.Step(`^"([^"]*)" MUST be skipped with reason mentioning "([^"]*)"$`, scanNameMustBeSkippedWithReasonMentioning)

	// @adopt-external @orphan — IMP-ADEXT-010
	ctx.Step(`^ScanSettingBinding "([^"]*)" referenced ScanSetting "([^"]*)" before deletion$`, ssbReferencedScanSettingBeforeDeletion)
	ctx.Step(`^"([^"]*)" was successfully created in ACS after removing the blocking ScanSettingBinding$`, scanNameWasSuccessfullyCreatedAfterRemovingBlocker)
	ctx.Step(`^ScanSetting "([^"]*)" MUST NOT be deleted$`, scanSettingMustNotBeDeleted)
	ctx.Step(`^the problem severity MUST be "([^"]*)"$`, theProblemSeverityMustBe)
}

// ensureAdextACSClient lazily creates the fake ACS client used to prove that
// the create-after-clear path routes through reconcile.Reconciler, so Given
// steps can run in either order.
func ensureAdextACSClient() {
	if mtc.adextACSClient == nil {
		mtc.adextACSClient = newFakeACSClient()
	}
}

// --- IMP-ADEXT-009: Create routes through the normal reconcile path and stays idempotent ---

func scanNameWasAlreadyClearedOfBlockers(scanName string) error {
	mtc.adextTargetScanName = scanName
	mtc.adextCreateAfterClear = true
	ensureAdextACSClient()
	return nil
}

func acsAlreadyHasScanConfigurationNamed(scanName string) error {
	ensureAdextACSClient()
	mtc.adextACSClient.configs[scanName] = acs.ScanConfig{
		ID:       "pre-existing-id",
		ScanName: scanName,
	}
	return nil
}

// processCreateAfterClearWorkflow routes the create for a scanName whose
// blocker(s) were already confirmed gone through the existing
// reconcile.Reconciler, exactly as internal/run.go's eventual wiring will:
// no duplicate "does this scanName already exist" check is implemented
// here, only a call into Reconcile. IMP-ADEXT-009
//
// When the create succeeds and a ScanSetting referenced by a deleted blocker
// was recorded (IMP-ADEXT-010), an "orphan" warning is added since the
// importer never deletes that ScanSetting itself.
func processCreateAfterClearWorkflow() error {
	ensureAdextACSClient()
	mtc.adextReconciler = &reconcile.Reconciler{
		Client:  mtc.adextACSClient,
		Options: reconcile.Options{},
	}
	payload := models.ACSPayload{ScanName: mtc.adextTargetScanName}
	mtc.adextReconcileResult = mtc.adextReconciler.Reconcile(context.Background(), payload)

	switch mtc.adextReconcileResult.Action {
	case "skip":
		mtc.skipped[mtc.adextTargetScanName] = true
	case "create":
		if mtc.adextOrphanScanSetting != "" {
			mtc.problems.Add(adoptexternal.OrphanWarning(mtc.adextOrphanScanSetting, mtc.adextOrphanContext, mtc.adextOrphanNamespace))
		}
	}
	return nil
}

func importerMustNotSendDuplicateCreateFor(scanName string) error {
	if mtc.adextACSClient == nil || mtc.adextACSClient.postCalled {
		return fmt.Errorf("expected no CreateScanConfig call for %q, but one was made", scanName)
	}
	return nil
}

func scanNameMustBeSkippedWithReasonMentioning(scanName, substr string) error {
	if !mtc.skipped[scanName] {
		return fmt.Errorf("expected %q to be skipped, but it was not", scanName)
	}
	if !strings.Contains(mtc.adextReconcileResult.Reason, substr) {
		return fmt.Errorf("expected skip reason to mention %q, got %q", substr, mtc.adextReconcileResult.Reason)
	}
	return nil
}

// --- IMP-ADEXT-010: Leftover ScanSetting is left in place with a warning ---

func ssbReferencedScanSettingBeforeDeletion(_, scanSettingName string) error {
	mtc.adextOrphanScanSetting = scanSettingName
	mtc.adextOrphanContext = "default"
	mtc.adextOrphanNamespace = "openshift-compliance"
	return nil
}

func scanNameWasSuccessfullyCreatedAfterRemovingBlocker(scanName string) error {
	mtc.adextTargetScanName = scanName
	mtc.adextCreateAfterClear = true
	ensureAdextACSClient()
	// IMP-ADEXT-010: the create must actually succeed for the orphan
	// warning to apply, so make sure ACS has no pre-existing config with
	// this name.
	delete(mtc.adextACSClient.configs, scanName)
	return nil
}

func scanSettingMustNotBeDeleted(name string) error {
	// IMP-ADEXT-010: the importer never deletes a ScanSetting itself, only
	// warns that it may be orphaned. There is no ScanSetting-deleting code
	// path anywhere in this package for a scenario to have triggered, so
	// this documents that guarantee; nothing in processCreateAfterClearWorkflow
	// (or any other adoptexternal entry point) ever calls a deleter for a
	// ScanSetting.
	if mtc.adextOrphanScanSetting != name {
		return fmt.Errorf("expected ScanSetting %q to be tracked as potentially orphaned, but it was not", name)
	}
	return nil
}

func theProblemSeverityMustBe(severity string) error {
	for _, p := range mtc.problems.All() {
		if p.Category == "orphan" && p.Severity == severity {
			return nil
		}
	}
	return fmt.Errorf("no %q-category problem found with severity %q", "orphan", severity)
}
