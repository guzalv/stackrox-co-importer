package features

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/guzalv/stackrox-co-importer/internal/adoptexternal"
	"github.com/guzalv/stackrox-co-importer/internal/merge"
	"github.com/guzalv/stackrox-co-importer/internal/models"
)

// registerMulticlusterBlockerSteps registers step definitions for the
// "Multi-cluster: all clusters must be clear" section of
// specs/08-adopt-external-ssb.feature (IMP-ADEXT-008). It shares
// mappingTestContext (mtc) with registerMappingSteps/registerBackupSteps/
// registerDeleteConfirmSteps/registerSettleSteps; the shared "the importer
// processes the adoption workflow" step (registered by registerBackupSteps)
// routes to processMultiClusterAdoptionWorkflow when a scenario sets up
// per-cluster blockers via the steps below.
func registerMulticlusterBlockerSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^kubecontext "([^"]*)" has a blocking ScanSettingBinding "([^"]*)"$`, kubecontextHasABlockingSSB)
	ctx.Step(`^ScanSettingBinding "([^"]*)" on ([\w-]+) is confirmed gone$`, ssbOnContextIsConfirmedGone)
	ctx.Step(`^ScanSettingBinding "([^"]*)" on ([\w-]+) reappears within the settle timeout$`, ssbOnContextReappearsWithinTheSettleTimeout)
	ctx.Step(`^the merged scan configuration "([^"]*)" MUST NOT be created$`, mergedScanConfigMustNotBeCreated)
	ctx.Step(`^the problem description MUST mention cluster "([^"]*)"$`, problemDescriptionMustMentionCluster)
}

// fakeMultiExister is a test double for adoptexternal.Exister that answers
// per kubecontext instead of giving the same fixed answer on every call, so
// a scenario can simulate one cluster's ScanSettingBinding staying gone
// while another's reappears (GitOps self-heal on only one cluster).
type fakeMultiExister struct {
	exists map[string]bool // context -> exists
	err    error
}

func (f *fakeMultiExister) ScanSettingBindingExists(ctxName, _, _ string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.exists[ctxName], nil
}

func ensureMultiExister() {
	if mtc.adextMultiExister == nil {
		mtc.adextMultiExister = &fakeMultiExister{exists: make(map[string]bool)}
	}
}

// --- IMP-ADEXT-008: Create proceeds only once every cluster's blocking SSB is confirmed gone ---

func kubecontextHasABlockingSSB(ctxName, ssbName string) error {
	mtc.adextMultiCluster = true
	mtc.adextMode = true
	mtc.adextTargetScanName = ssbName
	mtc.adextBlocked = true
	mtc.adextClusterBlockers = append(mtc.adextClusterBlockers, merge.ClusterSSB{
		Context: ctxName,
		SSB:     &models.ScanSettingBinding{Name: ssbName, Namespace: "openshift-compliance"},
	})
	return nil
}

func ssbOnContextIsConfirmedGone(_, ctxName string) error {
	ensureMultiExister()
	mtc.adextMultiExister.exists[ctxName] = false
	mtc.adextSettleTimeout = 2 * time.Second
	mtc.adextPoll = instantPoll
	return nil
}

func ssbOnContextReappearsWithinTheSettleTimeout(_, ctxName string) error {
	ensureMultiExister()
	mtc.adextMultiExister.exists[ctxName] = true
	mtc.adextSettleTimeout = 2 * time.Second
	mtc.adextPoll = instantPoll
	return nil
}

// processMultiClusterAdoptionWorkflow backs up every cluster's blocking
// ScanSettingBinding, then resolves whether the merged scan configuration's
// create may proceed via adoptexternal.ResolveMergedBlockers, which only
// reports proceed=true once every cluster's blocker is confirmed gone.
// IMP-ADEXT-008
func processMultiClusterAdoptionWorkflow() error {
	if len(mtc.adextClusterBlockers) == 0 {
		return fmt.Errorf("no multi-cluster blocking ScanSettingBindings set up yet")
	}

	dir, err := os.MkdirTemp("", "co-importer-backup-")
	if err != nil {
		return fmt.Errorf("creating temp backup dir: %w", err)
	}
	mtc.backupDir = dir

	sources := make([]adoptexternal.SourceRef, 0, len(mtc.adextClusterBlockers))
	perClusterBlockers := make(map[string][]merge.ClusterSSB)
	for _, b := range mtc.adextClusterBlockers {
		sources = append(sources, adoptexternal.SourceRef{
			Context:   b.Context,
			Namespace: b.SSB.Namespace,
			SSBName:   b.SSB.Name,
		})
		perClusterBlockers[b.Context] = append(perClusterBlockers[b.Context], b)
	}

	plan := adoptexternal.Plan{
		Scans: map[string]adoptexternal.PlannedScan{
			mtc.adextTargetScanName: {
				ScanName: mtc.adextTargetScanName,
				Sources:  sources,
				Status:   adoptexternal.StatusBackedUp,
			},
		},
	}

	mtc.backupErr = adoptexternal.WriteBackup(dir, plan, mtc.adextClusterBlockers, nil)
	if mtc.backupErr != nil {
		return nil
	}

	var exister adoptexternal.Exister
	if mtc.adextMultiExister != nil {
		exister = mtc.adextMultiExister
	}

	proceed, err := adoptexternal.ResolveMergedBlockers(
		mtc.problems, mtc.adextTargetScanName, perClusterBlockers,
		mtc.adextDeleteExisting, mtc.adextYes,
		mtc.adextDeleter, mtc.adextConfirmer.confirm,
		exister, mtc.adextSettleTimeout, mtc.adextPoll,
	)
	mtc.adextDeleted = proceed
	mtc.adextResolveErr = err
	if proceed {
		mtc.ssbDeleted = true
	} else if err == nil {
		mtc.skipped[mtc.adextTargetScanName] = true
	}
	return nil
}

func mergedScanConfigMustNotBeCreated(name string) error {
	if mtc.adextDeleted {
		return fmt.Errorf("expected merged scan configuration %q to NOT be created, but ResolveMergedBlockers reported every cluster's blocker was confirmed gone", name)
	}
	if !mtc.skipped[name] {
		return fmt.Errorf("expected %q to be skipped, but it was not", name)
	}
	return nil
}

func problemDescriptionMustMentionCluster(ctxName string) error {
	for _, p := range mtc.problems.All() {
		if strings.Contains(p.Description, ctxName) {
			return nil
		}
	}
	return fmt.Errorf("no problem description mentions cluster %q", ctxName)
}
