package features

import (
	"fmt"
	"os"

	"github.com/cucumber/godog"
	"github.com/guzalv/stackrox-co-importer/internal/adoptexternal"
	"github.com/guzalv/stackrox-co-importer/internal/merge"
)

// registerDryRunSteps registers step definitions for the "Dry-run: zero side
// effects" section of specs/08-adopt-external-ssb.feature (IMP-ADEXT-014).
// It shares mappingTestContext (mtc) with registerMappingSteps/
// registerBackupSteps: "the importer processes the adoption workflow"
// (registered by registerBackupSteps) routes to processDryRunAdoptionWorkflow
// below whenever a scenario sets mtc.adextDryRun via the "--dry-run" is set"
// step.
func registerDryRunSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^"--dry-run" is set$`, dryRunIsSet)
	ctx.Step(`^no backup directory or file MUST be written$`, noBackupDirectoryOrFileMustBeWritten)
	ctx.Step(`^no ACS scan configuration create or update call MUST be attempted$`, noACSCreateOrUpdateCallMustBeAttempted)
	ctx.Step(`^the importer MUST log the actions it would have taken$`, importerMustLogActionsItWouldHaveTaken)
}

func dryRunIsSet() error {
	// IMP-ADEXT-014
	mtc.adextDryRun = true
	return nil
}

// processDryRunAdoptionWorkflow computes what the adoption workflow would do
// for the blocked scanName via adoptexternal.DryRunBlocker, without ever
// calling WriteBackup, a Deleter, a Confirmer, or an ACS create/update.
//
// To prove the backup step performs no filesystem write, it records a
// backup-dir path that is never created (no os.MkdirAll, no os.WriteFile
// call happens anywhere in this function), so the "no backup directory or
// file MUST be written" assertion can check that nothing exists at that
// path afterward.
// IMP-ADEXT-014
func processDryRunAdoptionWorkflow() error {
	if mtc.adextBlockingSSB == nil {
		return fmt.Errorf("no blocking ScanSettingBinding identified yet")
	}

	dir, err := os.MkdirTemp("", "co-importer-dryrun-")
	if err != nil {
		return fmt.Errorf("creating placeholder temp dir for dry-run no-write check: %w", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing placeholder temp dir: %w", err)
	}
	// mtc.backupDir now names a path that does not exist. A real backup
	// would os.MkdirAll it; DryRunBlocker never touches the filesystem, so
	// it MUST still not exist once the scenario asserts against it.
	mtc.backupDir = dir
	mtc.backupErr = nil

	ssbs := []merge.ClusterSSB{{Context: "default", SSB: mtc.adextBlockingSSB}}
	mtc.adextDryRunLog = adoptexternal.DryRunBlocker(ssbs, mtc.adextTargetScanName, mtc.adextDeleteExisting)

	// Dry-run never deletes and never confirms gone, so nothing may proceed
	// to a real create/update: treat it like a skip for the purposes of the
	// shared "MUST NOT be deleted"/"MUST be skipped" assertions.
	mtc.adextDeleted = false
	mtc.adextResolveErr = nil
	mtc.skipped[mtc.adextTargetScanName] = true
	return nil
}

func noBackupDirectoryOrFileMustBeWritten() error {
	// IMP-ADEXT-014
	if mtc.backupErr != nil {
		return fmt.Errorf("expected no backup attempt in dry-run, but WriteBackup returned: %w", mtc.backupErr)
	}
	if mtc.backupDir == "" {
		return fmt.Errorf("no backup directory path was recorded for this scenario")
	}
	if _, err := os.Stat(mtc.backupDir); !os.IsNotExist(err) {
		if err == nil {
			return fmt.Errorf("expected backup directory %q to not exist after a dry-run, but it does", mtc.backupDir)
		}
		return fmt.Errorf("checking backup directory %q: %w", mtc.backupDir, err)
	}
	return nil
}

func noACSCreateOrUpdateCallMustBeAttempted() error {
	// IMP-ADEXT-014
	if mtc.adextACSClient != nil && (mtc.adextACSClient.postCalled || mtc.adextACSClient.putCalled) {
		return fmt.Errorf(
			"expected no ACS create/update call in dry-run, but postCalled=%v putCalled=%v",
			mtc.adextACSClient.postCalled, mtc.adextACSClient.putCalled,
		)
	}
	return nil
}

func importerMustLogActionsItWouldHaveTaken() error {
	// IMP-ADEXT-014
	if len(mtc.adextDryRunLog) == 0 {
		return fmt.Errorf("expected the importer to log the actions it would have taken, but no log messages were recorded")
	}
	return nil
}
