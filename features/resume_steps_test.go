package features

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cucumber/godog"
	"github.com/stackrox/co-importer/internal/adoptexternal"
	"github.com/stackrox/co-importer/internal/models"
	"github.com/stackrox/co-importer/internal/reconcile"
)

// registerResumeSteps registers step definitions for the "Resume after
// interruption or manual deletion" section of
// specs/08-adopt-external-ssb.feature (IMP-ADEXT-011, IMP-ADEXT-012). It
// shares mappingTestContext (mtc) with registerMappingSteps/
// registerBackupSteps/registerSettleSteps: a prior run's plan.json is
// written with adoptexternal.WriteBackup exactly as the backup scenarios do,
// then read back with adoptexternal.LoadPlan and checked with
// adoptexternal.ValidateEnvironment, the only new production code this
// section needs. Once the plan is confirmed to belong to the current
// environment, the create routes through the same reconcile.Reconciler path
// as registerCreateOrphanSteps' processCreateAfterClearWorkflow, so the
// shared "the importer MUST proceed to create ACS scan configuration"
// assertion (registered by registerSettleSteps) applies unchanged.
func registerResumeSteps(ctx *godog.ScenarioContext) {
	// @adopt-external @resume — IMP-ADEXT-011
	ctx.Step(`^a prior run persisted "plan\.json" for scan configuration "([^"]*)" in "--backup-dir"$`, priorRunPersistedPlanForScanConfig)
	ctx.Step(`^the plan's ACS endpoint and cluster IDs match the current run$`, thePlanEnvMatchesTheCurrentRun)
	ctx.Step(`^ScanSettingBinding "([^"]*)" no longer exists on any cluster$`, ssbNoLongerExistsOnAnyCluster)
	ctx.Step(`^the importer starts with the same "--backup-dir"$`, theImporterStartsWithTheSameBackupDir)
	ctx.Step(`^the importer MUST use the persisted plan instead of failing due to missing discovery$`, importerMustUseThePersistedPlan)

	// @adopt-external @resume — IMP-ADEXT-012
	ctx.Step(`^a prior run persisted "plan\.json" for ACS endpoint "([^"]*)"$`, priorRunPersistedPlanForACSEndpoint)
	ctx.Step(`^the current run's ACS endpoint is "([^"]*)"$`, theCurrentRunsACSEndpointIs)
	ctx.Step(`^config parsing or startup MUST fail with an error mentioning environment mismatch$`, configOrStartupMustFailWithEnvironmentMismatch)
	ctx.Step(`^the importer MUST NOT use the mismatched plan$`, importerMustNotUseTheMismatchedPlan)
}

// --- IMP-ADEXT-011: Resume from a persisted plan when the SSB is already gone ---

func priorRunPersistedPlanForScanConfig(scanName string) error {
	dir, err := os.MkdirTemp("", "co-importer-resume-")
	if err != nil {
		return fmt.Errorf("creating temp backup dir: %w", err)
	}
	mtc.backupDir = dir
	mtc.adextResumeTargetScan = scanName

	plan := adoptexternal.Plan{
		Env: adoptexternal.EnvStamp{
			ACSEndpoint: "https://central.example.com",
			ClusterIDs:  []string{"cluster-1"},
		},
		Scans: map[string]adoptexternal.PlannedScan{
			scanName: {
				ScanName:   scanName,
				Profiles:   []string{"ocp4-cis"},
				Schedule:   "0 0 * * *",
				ClusterIDs: []string{"cluster-1"},
				Sources: []adoptexternal.SourceRef{{
					Context:   "default",
					Namespace: "openshift-compliance",
					SSBName:   scanName,
				}},
				Status: adoptexternal.StatusBackedUp,
			},
		},
	}
	mtc.adextResumeWrittenEnv = plan.Env

	return adoptexternal.WriteBackup(dir, plan, nil, nil)
}

func thePlanEnvMatchesTheCurrentRun() error {
	// IMP-ADEXT-011: the current run reports the same environment the plan
	// was stamped with, so resuming from it is safe.
	mtc.adextResumeCurrentEnv = mtc.adextResumeWrittenEnv
	return nil
}

func ssbNoLongerExistsOnAnyCluster(_ string) error {
	// IMP-ADEXT-011: no live discovery is needed for this scenario — the
	// resume flow proceeds straight from the persisted plan.
	mtc.adextResumeSSBConfirmedGone = true
	return nil
}

// theImporterStartsWithTheSameBackupDir loads the plan persisted by a prior
// run, validates it was stamped for the current environment, and — if it
// matches — proceeds to create the ACS scan configuration described by the
// plan via the normal reconcile.Reconciler path, exactly as
// processCreateAfterClearWorkflow does once a blocker is otherwise confirmed
// gone. IMP-ADEXT-011, IMP-ADEXT-012
func theImporterStartsWithTheSameBackupDir() error {
	plan, err := adoptexternal.LoadPlan(mtc.backupDir)
	mtc.adextResumeLoadErr = err
	mtc.startupErr = err
	if err != nil {
		return nil
	}
	mtc.adextResumePlan = plan

	envErr := adoptexternal.ValidateEnvironment(plan, mtc.adextResumeCurrentEnv)
	mtc.adextResumeEnvErr = envErr
	mtc.startupErr = envErr
	if envErr != nil {
		return nil
	}

	scan, ok := plan.Scans[mtc.adextResumeTargetScan]
	if !ok {
		return fmt.Errorf("persisted plan does not describe scan configuration %q", mtc.adextResumeTargetScan)
	}

	mtc.adextResumeUsedPlan = true
	mtc.adextTargetScanName = scan.ScanName
	ensureAdextACSClient()
	mtc.adextReconciler = &reconcile.Reconciler{
		Client:  mtc.adextACSClient,
		Options: reconcile.Options{},
	}
	payload := models.ACSPayload{
		ScanName: scan.ScanName,
		ScanConfig: models.ACSBaseScanConfig{
			Profiles: scan.Profiles,
		},
		Clusters: scan.ClusterIDs,
	}
	mtc.adextReconcileResult = mtc.adextReconciler.Reconcile(context.Background(), payload)

	switch mtc.adextReconcileResult.Action {
	case "skip":
		mtc.skipped[mtc.adextTargetScanName] = true
	case "create":
		// Signal the shared "the importer MUST proceed to create ACS scan
		// configuration" assertion (registered by registerSettleSteps),
		// which checks mtc.adextDeleted/mtc.adextResolveErr to mean "the
		// blocker is confirmed gone and the create may proceed".
		mtc.adextDeleted = true
		mtc.adextResolveErr = nil
	}
	return nil
}

func importerMustUseThePersistedPlan() error {
	if mtc.adextResumeLoadErr != nil {
		return fmt.Errorf("expected LoadPlan to succeed, got: %v", mtc.adextResumeLoadErr)
	}
	if mtc.adextResumeEnvErr != nil {
		return fmt.Errorf("expected environment validation to succeed, got: %v", mtc.adextResumeEnvErr)
	}
	if !mtc.adextResumeUsedPlan {
		return fmt.Errorf("expected the importer to resume from the persisted plan, but it did not")
	}
	return nil
}

// --- IMP-ADEXT-012: Resume refuses a plan from a different environment ---

func priorRunPersistedPlanForACSEndpoint(endpoint string) error {
	dir, err := os.MkdirTemp("", "co-importer-resume-")
	if err != nil {
		return fmt.Errorf("creating temp backup dir: %w", err)
	}
	mtc.backupDir = dir
	scanName := "cis-weekly"
	mtc.adextResumeTargetScan = scanName

	plan := adoptexternal.Plan{
		Env: adoptexternal.EnvStamp{
			ACSEndpoint: endpoint,
			ClusterIDs:  []string{"cluster-1"},
		},
		Scans: map[string]adoptexternal.PlannedScan{
			scanName: {
				ScanName:   scanName,
				Profiles:   []string{"ocp4-cis"},
				ClusterIDs: []string{"cluster-1"},
				Status:     adoptexternal.StatusBackedUp,
			},
		},
	}
	mtc.adextResumeWrittenEnv = plan.Env

	return adoptexternal.WriteBackup(dir, plan, nil, nil)
}

func theCurrentRunsACSEndpointIs(endpoint string) error {
	mtc.adextResumeCurrentEnv = adoptexternal.EnvStamp{ACSEndpoint: endpoint}
	return nil
}

func configOrStartupMustFailWithEnvironmentMismatch() error {
	if mtc.startupErr == nil {
		return fmt.Errorf("expected startup to fail, but got nil error")
	}
	if !strings.Contains(mtc.startupErr.Error(), "environment") {
		return fmt.Errorf("expected error to mention environment mismatch, got: %v", mtc.startupErr)
	}
	return nil
}

func importerMustNotUseTheMismatchedPlan() error {
	if mtc.adextResumeUsedPlan {
		return fmt.Errorf("expected the importer to not use the mismatched plan, but it did")
	}
	return nil
}
