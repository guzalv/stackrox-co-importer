package features

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
	"github.com/stackrox/co-importer/internal/adoptexternal"
	"github.com/stackrox/co-importer/internal/merge"
	"github.com/stackrox/co-importer/internal/models"
	"sigs.k8s.io/yaml"
)

// registerBackupSteps registers step definitions for the "Backup (always,
// before any removal)" section of specs/08-adopt-external-ssb.feature
// (IMP-ADEXT-002, IMP-ADEXT-015). It shares mappingTestContext (mtc) with
// registerMappingSteps since both operate on the same ScanSettingBinding
// fixtures set up by the shared "Given" steps.
func registerBackupSteps(ctx *godog.ScenarioContext) {
	ctx.After(func(goCtx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		if mtc != nil && mtc.backupDir != "" {
			_ = os.RemoveAll(mtc.backupDir)
		}
		return goCtx, err
	})

	// @adopt-external @backup — IMP-ADEXT-002
	ctx.Step(`^the SSB references ScanSetting "([^"]*)"$`, theSSBReferencesScanSetting)
	ctx.Step(`^"([^"]*)" is identified as blocked by an external ScanSettingBinding$`, scanNameIsIdentifiedAsBlocked)
	ctx.Step(`^the importer processes the adoption workflow$`, theImporterProcessesTheAdoptionWorkflow)
	ctx.Step(`^the backup directory MUST contain a YAML copy of ScanSettingBinding "([^"]*)"$`, backupDirMustContainSSBYAML)
	ctx.Step(`^the backup directory MUST contain a YAML copy of ScanSetting "([^"]*)"$`, backupDirMustContainScanSettingYAML)
	ctx.Step(`^the backup directory MUST contain a "plan\.json" describing scan configuration "([^"]*)"$`, backupDirMustContainPlanJSONFor)
	ctx.Step(`^the backup MUST be written before any ScanSettingBinding is deleted$`, backupMustBeWrittenBeforeAnyDeletion)

	// @adopt-external @backup — IMP-ADEXT-015
	ctx.Step(`^"--backup-dir" points to a non-empty directory with no "plan\.json"$`, backupDirPointsToNonEmptyDirWithNoPlanJSON)
	ctx.Step(`^the importer starts$`, theImporterStarts)
	ctx.Step(`^the importer MUST fail with an error mentioning the backup directory is not empty$`, importerMustFailWithBackupDirNotEmptyError)
}

// --- IMP-ADEXT-002: Backup is written before any removal action ---

func theSSBReferencesScanSetting(settingName string) error {
	// IMP-ADEXT-002
	if mtc.ssb == nil {
		return fmt.Errorf("no ScanSettingBinding set up yet")
	}
	mtc.ssb.ScanSettingName = settingName
	mtc.scanSetting = &models.ScanSetting{
		Name:      settingName,
		Namespace: mtc.ssb.Namespace,
		Schedule:  "0 0 * * *",
	}
	return nil
}

func scanNameIsIdentifiedAsBlocked(scanName string) error {
	// IMP-ADEXT-002 (shared precondition with the not-yet-implemented
	// IMP-ADEXT-003..008 scenarios)
	if mtc.ssb == nil {
		mtc.ssb = &models.ScanSettingBinding{Name: scanName, Namespace: "openshift-compliance"}
	}
	mtc.adextMode = true
	mtc.adextTargetScanName = scanName
	mtc.adextBlocked = true
	mtc.adextBlockingSSB = mtc.ssb
	return nil
}

// theImporterProcessesTheAdoptionWorkflow writes the backup for the blocking
// ScanSettingBinding (and its referenced ScanSetting) set up by prior steps,
// into a fresh temp directory used as --backup-dir for this scenario, then
// resolves what happens to the blocked scanName (report-and-skip by default,
// or delete-with-confirmation when --delete-existing is set).
// IMP-ADEXT-002, IMP-ADEXT-003, IMP-ADEXT-004, IMP-ADEXT-005
func theImporterProcessesTheAdoptionWorkflow() error {
	if mtc.adextDryRun {
		// IMP-ADEXT-014: --dry-run MUST perform zero side effects anywhere
		// in this flow, so route to a dedicated dry-run-only path instead
		// of the real backup/resolve workflow below.
		return processDryRunAdoptionWorkflow()
	}

	if mtc.adextMultiCluster {
		// IMP-ADEXT-008: a single scanName blocked on multiple clusters at once.
		return processMultiClusterAdoptionWorkflow()
	}

	if mtc.adextCreateAfterClear {
		// IMP-ADEXT-009, IMP-ADEXT-010: the blocker was already confirmed
		// gone, so route the create through the normal reconcile path.
		return processCreateAfterClearWorkflow()
	}

	if mtc.adextBlockingSSB == nil {
		return fmt.Errorf("no blocking ScanSettingBinding identified yet")
	}

	dir, err := os.MkdirTemp("", "co-importer-backup-")
	if err != nil {
		return fmt.Errorf("creating temp backup dir: %w", err)
	}
	mtc.backupDir = dir

	ssbs := []merge.ClusterSSB{{Context: "default", SSB: mtc.adextBlockingSSB}}

	var scanSettings []adoptexternal.ScanSettingSource
	var scanSettingName string
	if mtc.scanSetting != nil {
		scanSettings = append(scanSettings, adoptexternal.ScanSettingSource{
			Context:     "default",
			ScanSetting: *mtc.scanSetting,
		})
		scanSettingName = mtc.scanSetting.Name
	}

	plan := adoptexternal.Plan{
		Scans: map[string]adoptexternal.PlannedScan{
			mtc.adextTargetScanName: {
				ScanName: mtc.adextTargetScanName,
				Sources: []adoptexternal.SourceRef{{
					Context:         "default",
					Namespace:       mtc.adextBlockingSSB.Namespace,
					SSBName:         mtc.adextBlockingSSB.Name,
					ScanSettingName: scanSettingName,
				}},
				Status: adoptexternal.StatusBackedUp,
			},
		},
	}

	mtc.backupErr = adoptexternal.WriteBackup(dir, plan, ssbs, scanSettings)
	if mtc.backupErr != nil {
		return nil
	}

	// IMP-ADEXT-003, IMP-ADEXT-004, IMP-ADEXT-005: now decide what happens to
	// the blocked scanName, given --delete-existing / --yes and the
	// operator's answer to the confirmation prompt (if any).
	// IMP-ADEXT-006, IMP-ADEXT-007: when a settle-timeout check has been
	// configured by a prior step, ResolveBlocker also guards against GitOps
	// self-heal before reporting the blockers are confirmed gone.
	var exister adoptexternal.Exister
	if mtc.adextExister != nil {
		exister = mtc.adextExister
	}
	proceed, err := adoptexternal.ResolveBlocker(
		mtc.problems, ssbs, mtc.adextTargetScanName,
		mtc.adextDeleteExisting, mtc.adextYes,
		mtc.adextDeleter, mtc.adextConfirmer.confirm,
		exister, mtc.adextSettleTimeout, mtc.adextPoll,
	)
	deleted := proceed
	mtc.adextDeleted = deleted
	mtc.adextResolveErr = err
	if deleted {
		mtc.ssbDeleted = true
	} else if err == nil {
		mtc.skipped[mtc.adextTargetScanName] = true
	}
	return nil
}

func backupDirMustContainSSBYAML(name string) error {
	// IMP-ADEXT-002
	if mtc.backupErr != nil {
		return fmt.Errorf("WriteBackup failed: %w", mtc.backupErr)
	}
	path, err := findBackupFile(mtc.backupDir, "__ssb__"+name+".yaml")
	if err != nil {
		return err
	}
	var ssb models.ScanSettingBinding
	if err := unmarshalYAMLFile(path, &ssb); err != nil {
		return err
	}
	if ssb.Name != name {
		return fmt.Errorf("expected backed-up ScanSettingBinding name %q, got %q", name, ssb.Name)
	}
	return nil
}

func backupDirMustContainScanSettingYAML(name string) error {
	// IMP-ADEXT-002
	if mtc.backupErr != nil {
		return fmt.Errorf("WriteBackup failed: %w", mtc.backupErr)
	}
	path, err := findBackupFile(mtc.backupDir, "__scansetting__"+name+".yaml")
	if err != nil {
		return err
	}
	var ss models.ScanSetting
	if err := unmarshalYAMLFile(path, &ss); err != nil {
		return err
	}
	if ss.Name != name {
		return fmt.Errorf("expected backed-up ScanSetting name %q, got %q", name, ss.Name)
	}
	return nil
}

func backupDirMustContainPlanJSONFor(scanName string) error {
	// IMP-ADEXT-002
	if mtc.backupErr != nil {
		return fmt.Errorf("WriteBackup failed: %w", mtc.backupErr)
	}
	data, err := os.ReadFile(filepath.Join(mtc.backupDir, "plan.json"))
	if err != nil {
		return fmt.Errorf("reading plan.json: %w", err)
	}
	var plan adoptexternal.Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return fmt.Errorf("unmarshalling plan.json: %w", err)
	}
	scan, ok := plan.Scans[scanName]
	if !ok || scan.ScanName != scanName {
		return fmt.Errorf("plan.json does not describe scan configuration %q: %+v", scanName, plan)
	}
	return nil
}

func backupMustBeWrittenBeforeAnyDeletion() error {
	// IMP-ADEXT-002: the backup step has no dependency on any deletion, and
	// in this scenario no deletion has happened by the time WriteBackup
	// already completed successfully.
	if mtc.backupErr != nil {
		return fmt.Errorf("backup did not complete successfully before this check: %w", mtc.backupErr)
	}
	if mtc.ssbDeleted {
		return fmt.Errorf("a ScanSettingBinding was deleted before the backup completed")
	}
	return nil
}

// unmarshalYAMLFile reads and YAML-unmarshals the file at path into out.
func unmarshalYAMLFile(path string, out interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading backup file %q: %w", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("unmarshalling backup file %q: %w", path, err)
	}
	return nil
}

// findBackupFile returns the path to the single file in dir whose name ends
// with suffix, or an error if zero or more than one match.
func findBackupFile(dir, suffix string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("reading backup dir %q: %w", dir, err)
	}
	var matches []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			matches = append(matches, e.Name())
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no backup file ending in %q found in %q", suffix, dir)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("expected exactly one backup file ending in %q, found %v", suffix, matches)
	}
	return filepath.Join(dir, matches[0]), nil
}

// --- IMP-ADEXT-015: Backup dir conflict is rejected ---

func backupDirPointsToNonEmptyDirWithNoPlanJSON() error {
	// IMP-ADEXT-015
	dir, err := os.MkdirTemp("", "co-importer-backup-conflict-")
	if err != nil {
		return fmt.Errorf("creating temp backup dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unrelated.txt"), []byte("pre-existing"), 0o644); err != nil {
		return fmt.Errorf("writing unrelated file: %w", err)
	}
	mtc.backupDir = dir
	return nil
}

func theImporterStarts() error {
	// IMP-ADEXT-015, IMP-CLI-032
	mtc.startupErr = adoptexternal.CheckBackupDir(mtc.backupDir)
	return nil
}

func importerMustFailWithBackupDirNotEmptyError() error {
	// IMP-ADEXT-015
	if mtc.startupErr == nil {
		return fmt.Errorf("expected the importer to fail, but CheckBackupDir returned nil")
	}
	if !strings.Contains(mtc.startupErr.Error(), "not empty") {
		return fmt.Errorf("expected error to mention the backup directory is not empty, got: %v", mtc.startupErr)
	}
	return nil
}
