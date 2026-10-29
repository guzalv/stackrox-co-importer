// Package run orchestrates the full importer pipeline: fetch CO resources,
// map to ACS payloads, merge, reconcile, adopt-external, and report.
//
// NOTE: the former "adopt" step (patch an existing SSB's settingsRef after a successful
// ACS create) was retired. RHACS >=4.11/5.0 rejects creating or updating a scan
// configuration whose profiles overlap an existing non-ACS-managed SSB, so that create
// could never succeed while the external SSB it would have patched still existed. See
// specs/08-adopt-external-ssb.feature (IMP-ADEXT-*) for the replacement workflow
// (detect, back up, remove the external SSB, then create), wired in below.
package run

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/stackrox/co-importer/internal/acs"
	"github.com/stackrox/co-importer/internal/adoptexternal"
	"github.com/stackrox/co-importer/internal/cofetch"
	"github.com/stackrox/co-importer/internal/config"
	"github.com/stackrox/co-importer/internal/discover"
	"github.com/stackrox/co-importer/internal/filter"
	"github.com/stackrox/co-importer/internal/listssbs"
	"github.com/stackrox/co-importer/internal/mapping"
	"github.com/stackrox/co-importer/internal/merge"
	"github.com/stackrox/co-importer/internal/models"
	"github.com/stackrox/co-importer/internal/problems"
	"github.com/stackrox/co-importer/internal/reconcile"
	"github.com/stackrox/co-importer/internal/report"
	"github.com/stackrox/co-importer/internal/status"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	// ExitSuccess indicates all bindings processed successfully.
	ExitSuccess = 0
	// ExitFatalError indicates a fatal error during preflight/config.
	ExitFatalError = 1
	// ExitPartialError indicates partial success (some bindings failed).
	ExitPartialError = 2
)

// clusterSource holds the CO client and cluster ID for one kubeconfig context.
type clusterSource struct {
	kubeconfigFile string
	contextName    string
	clusterID      string
	coClient       cofetch.COClient
	dynClient      dynamic.Interface
}

// Runner orchestrates the importer pipeline.
type Runner struct {
	cfg       *config.Config
	acsClient acs.Client
	printer   *status.Printer
}

// NewRunner creates a new pipeline runner.
func NewRunner(cfg *config.Config, acsClient acs.Client) *Runner {
	return &Runner{
		cfg:       cfg,
		acsClient: acsClient,
		printer:   status.New(),
	}
}

// Run executes the full import pipeline and returns an exit code.
func (r *Runner) Run(ctx context.Context) int {
	collector := problems.New()

	// Step 1: List existing ACS scan configs
	r.printer.Stage("Listing existing ACS scan configurations", "")
	_, err := r.acsClient.ListScanConfigs(ctx)
	if err != nil {
		r.printer.Fail(fmt.Sprintf("failed to list existing ACS scan configs: %v", err))
		return ExitFatalError
	}

	// Step 2: Build cluster sources
	r.printer.Stage("Discovering cluster sources", "")
	sources, err := r.buildClusterSources(ctx, collector)
	if err != nil {
		r.printer.Fail(fmt.Sprintf("failed to build cluster sources: %v", err))
		return ExitFatalError
	}
	if len(sources) == 0 {
		r.printer.Warn("no cluster sources found")
		r.writeReport(collector, nil, 0)
		return ExitSuccess
	}
	r.printer.OK(fmt.Sprintf("discovered %d cluster source(s)", len(sources)))

	envStamp := adoptexternal.EnvStamp{ACSEndpoint: r.cfg.Endpoint}
	for _, src := range sources {
		envStamp.Contexts = append(envStamp.Contexts, src.contextName)
		envStamp.ClusterIDs = append(envStamp.ClusterIDs, src.clusterID)
	}

	// IMP-ADEXT-011, IMP-ADEXT-012: resume from a persisted plan when
	// --backup-dir points at one (auto-detected; no separate --resume flag).
	// A fresh/default (timestamped) --backup-dir never has a plan.json, so
	// this is a no-op unless the operator deliberately reuses a prior run's
	// --backup-dir.
	var resumedScans []adoptexternal.PlannedScan
	if r.cfg.BackupDir != "" {
		plan, loadErr := adoptexternal.LoadPlan(r.cfg.BackupDir)
		if loadErr != nil {
			r.printer.Fail(fmt.Sprintf("failed to load resumable plan from %q: %v", r.cfg.BackupDir, loadErr))
			return ExitFatalError
		}
		if plan != nil {
			if envErr := adoptexternal.ValidateEnvironment(plan, envStamp); envErr != nil {
				r.printer.Fail(envErr.Error())
				return ExitFatalError
			}
			for _, ps := range plan.Scans {
				if ps.Status != adoptexternal.StatusCreated {
					resumedScans = append(resumedScans, ps)
				}
			}
			if len(resumedScans) > 0 {
				r.printer.OK(fmt.Sprintf("resuming %d scan configuration(s) from %q", len(resumedScans), r.cfg.BackupDir))
			}
		}
	}

	// Step 3: For each context, list SSBs, get ScanSettings, build merge inputs
	r.printer.Stage("Fetching CO resources and building payloads", "")
	var allClusterSSBs []merge.ClusterSSB
	type ssbDetail struct {
		namespace       string
		bindingName     string
		scanSettingName string
	}
	ssbDetails := make(map[string]ssbDetail)
	// scanSettingsByBinding carries the ScanSetting object referenced by each
	// SSB (keyed by "context/ssbName"), so a blocking external SSB's
	// ScanSetting can be backed up (IMP-ADEXT-002) and flagged as orphaned
	// after a successful adopt-external create (IMP-ADEXT-010).
	scanSettingsByBinding := make(map[string]*models.ScanSetting)

	totalDiscovered := 0

	for _, src := range sources {
		ssbs, fetchErr := src.coClient.ListScanSettingBindings(ctx)
		if fetchErr != nil {
			r.printer.Warn(fmt.Sprintf("context %q: failed to list SSBs: %v", src.contextName, fetchErr))
			collector.Add(problems.Problem{
				Severity:    "error",
				Category:    "input",
				ResourceRef: "context:" + src.contextName,
				Description: fmt.Sprintf("failed to list ScanSettingBindings on context %q: %v", src.contextName, fetchErr),
			})
			continue
		}
		totalDiscovered += len(ssbs)

		// IMP-CLI-028: apply --exclude filter before any ACS operations.
		ssbs = filter.ExcludeSSBs(ssbs, r.cfg.ExcludePatterns)
		// IMP-ADEXT-013: stackrox-managed SSBs (ACS/Sensor-created) are never
		// treated as a source binding to import, so a re-run never
		// re-ingests what a prior adopt-external run already created.
		ssbs = adoptexternal.FilterExternal(ssbs)

		for i := range ssbs {
			ssb := &ssbs[i]
			ns := ssb.Namespace
			if ns == "" {
				ns = r.cfg.Namespace
			}

			ss, ssErr := src.coClient.GetScanSetting(ctx, ns, ssb.ScanSettingName)
			if ssErr != nil {
				ref := ns + "/" + ssb.Name
				r.printer.Warn(fmt.Sprintf("SSB %s: ScanSetting %q not found: %v", ref, ssb.ScanSettingName, ssErr))
				collector.Add(problems.Problem{
					Severity:    "error",
					Category:    "input",
					ResourceRef: ref,
					Description: fmt.Sprintf("ScanSettingBinding %q references ScanSetting %q which could not be fetched: %v", ref, ssb.ScanSettingName, ssErr),
					FixHint:     fmt.Sprintf("Create ScanSetting %q or update the binding to reference an existing ScanSetting", ssb.ScanSettingName),
					Skipped:     true,
				})
				continue
			}

			ssbDetails[ssb.Name] = ssbDetail{
				namespace:       ns,
				bindingName:     ssb.Name,
				scanSettingName: ssb.ScanSettingName,
			}
			scanSettingsByBinding[src.contextName+"/"+ssb.Name] = ss

			allClusterSSBs = append(allClusterSSBs, merge.ClusterSSB{
				Context:   src.contextName,
				ClusterID: src.clusterID,
				SSB:       ssb,
				Schedule:  ss.Schedule,
			})
		}
	}
	r.printer.OK(fmt.Sprintf("discovered %d binding(s) total", totalDiscovered))

	// Step 4: Merge same-name SSBs across clusters
	r.printer.Stage("Merging SSBs across clusters", "")
	mergedConfigs, mergeProblems := merge.MergeSSBs(allClusterSSBs)
	for _, p := range mergeProblems.All() {
		collector.Add(p)
	}
	r.printer.OK(fmt.Sprintf("%d merged configuration(s), %d conflict(s)", len(mergedConfigs), len(mergeProblems.All())))

	// Step 5: Reconcile each merged payload
	r.printer.Stage("Reconciling scan configurations", "")
	reconciler := &reconcile.Reconciler{
		Client: r.acsClient,
		Options: reconcile.Options{
			DryRun:            r.cfg.DryRun,
			OverwriteExisting: r.cfg.OverwriteExisting,
			MaxRetries:        r.cfg.MaxRetries,
		},
	}

	items := make([]report.Item, 0, len(mergedConfigs))
	createCount, skipCount, failCount, updateCount := 0, 0, 0, 0

	// adoptK8s/confirmer back the adopt-external workflow with real K8s
	// operations; backupDirChecked ensures IMP-ADEXT-015's "non-empty
	// backup-dir without plan.json" check runs once, lazily, only if a
	// blocker is actually found (never for a run with nothing to back up).
	adoptK8s := newAdoptK8sAdapter(sources)
	confirmer := interactiveConfirmer()
	backupDirChecked := false
	ensureBackupDir := func() error {
		if backupDirChecked || r.cfg.DryRun {
			return nil
		}
		backupDirChecked = true
		return adoptexternal.CheckBackupDir(r.cfg.BackupDir)
	}

	seenScanNames := make(map[string]bool)

	for _, mc := range mergedConfigs {
		seenScanNames[mc.ScanName] = true
		schedule, schedErr := mapping.ConvertCronToACSSchedule(mc.Schedule)
		if schedErr != nil {
			ref := mc.ScanName
			collector.Add(problems.Problem{
				Severity:    "error",
				Category:    "mapping",
				ResourceRef: ref,
				Description: fmt.Sprintf("schedule conversion failed for %q: %v", mc.ScanName, schedErr),
				FixHint:     "Update the ScanSetting to use a supported 5-field cron expression",
				Skipped:     true,
			})
			failCount++
			detail := ssbDetails[mc.ScanName]
			items = append(items, report.Item{
				Source: report.ItemSource{
					Namespace:       detail.namespace,
					BindingName:     detail.bindingName,
					ScanSettingName: detail.scanSettingName,
				},
				Action: "fail",
				Reason: fmt.Sprintf("schedule conversion failed: %v", schedErr),
				Error:  schedErr.Error(),
			})
			continue
		}

		payload := models.ACSPayload{
			ScanName: mc.ScanName,
			ScanConfig: models.ACSBaseScanConfig{
				OneTimeScan:  false,
				Profiles:     mc.Profiles,
				ScanSchedule: schedule,
				Description:  fmt.Sprintf("Imported from CO ScanSettingBinding %s", mc.ScanName),
			},
			Clusters: mc.ClusterIDs,
		}

		detail := ssbDetails[mc.ScanName]
		item := r.reconcileWithAdoptExternal(ctx, reconciler, collector, payload, mc.ScanName, mc.Schedule,
			allClusterSSBs, scanSettingsByBinding, envStamp, adoptK8s, confirmer, ensureBackupDir)
		item.Source = report.ItemSource{
			Namespace:       detail.namespace,
			BindingName:     detail.bindingName,
			ScanSettingName: detail.scanSettingName,
		}

		switch item.Action {
		case "create":
			createCount++
		case "update":
			updateCount++
		case "skip":
			skipCount++
		case "fail":
			failCount++
		}
		items = append(items, item)
	}

	// IMP-ADEXT-011: process resumed scans (from a persisted plan) that a
	// fresh merge didn't already cover, e.g. because their source SSB is now
	// gone from every cluster and so produced no fresh ClusterSSB at all.
	for _, ps := range resumedScans {
		if seenScanNames[ps.ScanName] {
			continue
		}
		schedule, schedErr := mapping.ConvertCronToACSSchedule(ps.Schedule)
		if schedErr != nil {
			failCount++
			continue
		}
		payload := models.ACSPayload{
			ScanName: ps.ScanName,
			ScanConfig: models.ACSBaseScanConfig{
				OneTimeScan:  false,
				Profiles:     ps.Profiles,
				ScanSchedule: schedule,
				Description:  fmt.Sprintf("Imported from CO ScanSettingBinding %s (resumed)", ps.ScanName),
			},
			Clusters: ps.ClusterIDs,
		}
		result := reconciler.Reconcile(ctx, payload)
		var item report.Item
		if len(ps.Sources) > 0 {
			item.Source = report.ItemSource{
				Namespace:       ps.Sources[0].Namespace,
				BindingName:     ps.Sources[0].SSBName,
				ScanSettingName: ps.Sources[0].ScanSettingName,
			}
		}
		r.logAndRecordResult(collector, ps.ScanName, result, &item)
		if item.Action == "create" {
			// IMP-ADEXT-010: same as the fresh-discovery path, flag every
			// ScanSetting a resumed scan's (now-deleted) source SSB(s)
			// referenced as possibly orphaned.
			for _, src := range ps.Sources {
				collector.Add(adoptexternal.OrphanWarning(src.ScanSettingName, src.Context, src.Namespace))
			}
		}
		switch item.Action {
		case "create":
			createCount++
		case "update":
			updateCount++
		case "skip":
			skipCount++
		case "fail":
			failCount++
		}
		items = append(items, item)
	}

	// Step 7: Build and write report
	// For report, updates count as creates
	reportCreateCount := createCount + updateCount
	r.writeReportFull(collector, items, totalDiscovered, reportCreateCount, skipCount, failCount)

	// Step 8: Print console summary (IMP-CLI-020)
	r.printSummary(totalDiscovered, createCount+updateCount, skipCount, failCount)

	// Step 9: Return exit code
	return exitCodeForRun(failCount, collector.All())
}

// exitCodeForRun decides the process exit code. A "gitops" problem (a scan
// blocked by an external ScanSettingBinding, by default, by declining the
// --delete-existing confirmation, or by GitOps self-heal reappearing it)
// forces ExitPartialError even with zero failures: it's a routine report
// condition, not a fatal error, but it means a requested import did not
// happen, so it must not be indistinguishable from full success via exit
// code alone. IMP-CLI-019
func exitCodeForRun(failCount int, probs []problems.Problem) int {
	if failCount > 0 {
		return ExitPartialError
	}
	for _, p := range probs {
		if p.Category == "gitops" {
			return ExitPartialError
		}
	}
	return ExitSuccess
}

// buildClusterSources creates COClients and discovers ACS cluster IDs for each context.
func (r *Runner) buildClusterSources(ctx context.Context, collector *problems.Collector) ([]clusterSource, error) {
	kubeconfigFiles := cofetch.KubeconfigFiles(os.Getenv)

	if len(kubeconfigFiles) == 0 {
		return nil, fmt.Errorf("no kubeconfig files found")
	}

	acsLister := &acsClusterListerAdapter{client: r.acsClient, ctx: ctx}

	var allSources []clusterSource

	for _, kcFile := range kubeconfigFiles {
		contexts, ctxErr := cofetch.ContextsFromKubeconfig(kcFile)
		if ctxErr != nil {
			r.printer.Warn(fmt.Sprintf("skipping kubeconfig %q: %v", kcFile, ctxErr))
			continue
		}

		for _, ctxName := range contexts {
			if len(r.cfg.Contexts) > 0 && !containsString(r.cfg.Contexts, ctxName) {
				continue
			}

			coClient, clientErr := cofetch.NewClient(kcFile, ctxName, r.cfg.Namespace, r.cfg.AllNamespaces)
			if clientErr != nil {
				r.printer.Warn(fmt.Sprintf("context %q: failed to create client: %v", ctxName, clientErr))
				collector.Add(problems.Problem{
					Severity:    "error",
					Category:    "input",
					ResourceRef: "context:" + ctxName,
					Description: fmt.Sprintf("failed to create Kubernetes client for context %q: %v", ctxName, clientErr),
				})
				continue
			}

			dynClient, dynErr := cofetch.NewDynamicClientForContext(kcFile, ctxName)
			if dynErr != nil {
				r.printer.Warn(fmt.Sprintf("context %q: failed to create dynamic client: %v", ctxName, dynErr))
				continue
			}

			kubeReader := &cofetch.KubeReaderFromDynamic{DynClient: dynClient}
			disc := &discover.Discoverer{
				Kube: kubeReader,
				ACS:  acsLister,
			}
			clusterID, discErr := disc.Resolve()
			if discErr != nil {
				r.printer.Warn(fmt.Sprintf("context %q: cluster discovery failed: %v", ctxName, discErr))
				collector.Add(problems.Problem{
					Severity:    "error",
					Category:    "input",
					ResourceRef: "context:" + ctxName,
					Description: fmt.Sprintf("cluster ID discovery failed for context %q: %v", ctxName, discErr),
					FixHint:     "Ensure the cluster has StackRox sensor installed and the admission-control ConfigMap, ClusterVersion, or helm-effective-cluster-name Secret is accessible",
					Skipped:     true,
				})
				continue
			}

			r.printer.OK(fmt.Sprintf("context %q -> ACS cluster %s", ctxName, clusterID))
			allSources = append(allSources, clusterSource{
				kubeconfigFile: kcFile,
				contextName:    ctxName,
				clusterID:      clusterID,
				coClient:       coClient,
				dynClient:      dynClient,
			})
		}
	}

	return allSources, nil
}

// writeReport writes a minimal report (used when no items are available).
func (r *Runner) writeReport(collector *problems.Collector, items []report.Item, discovered int) {
	r.writeReportFull(collector, items, discovered, 0, 0, 0)
}

// writeReportFull writes the JSON report if --report-json is configured.
func (r *Runner) writeReportFull(collector *problems.Collector, items []report.Item, discovered, creates, skips, failures int) {
	if r.cfg.ReportJSON == "" {
		return
	}

	mode := "create-only"
	if r.cfg.OverwriteExisting {
		mode = "create-or-update"
	}

	nsScope := r.cfg.Namespace
	if r.cfg.AllNamespaces {
		nsScope = "*"
	}
	if nsScope == "" {
		nsScope = "openshift-compliance"
	}

	allProblems := collector.All()
	reportProblems := make([]report.Problem, 0, len(allProblems))
	for _, p := range allProblems {
		reportProblems = append(reportProblems, report.Problem{
			Severity:    p.Severity,
			Category:    p.Category,
			ResourceRef: p.ResourceRef,
			Description: p.Description,
			FixHint:     p.FixHint,
			Skipped:     p.Skipped,
		})
	}

	rep := report.Report{
		Meta: report.Meta{
			DryRun:         r.cfg.DryRun,
			NamespaceScope: nsScope,
			Mode:           mode,
		},
		Counts: report.Counts{
			Discovered: discovered,
			Create:     creates,
			Skip:       skips,
			Failed:     failures,
		},
		Items:    items,
		Problems: reportProblems,
	}

	if err := report.WriteJSON(r.cfg.ReportJSON, rep); err != nil {
		r.printer.Fail(fmt.Sprintf("failed to write report: %v", err))
	} else {
		r.printer.OK(fmt.Sprintf("report written to %s", r.cfg.ReportJSON))
	}
}

// printSummary prints the console summary (IMP-CLI-020).
func (r *Runner) printSummary(discovered, creates, skips, failures int) {
	r.printer.Stage("Summary", "")
	dryRunTag := ""
	if r.cfg.DryRun {
		dryRunTag = " (dry-run)"
	}
	fmt.Fprintf(os.Stderr,
		"  Discovered: %d | Created: %d | Skipped: %d | Failed: %d%s\n",
		discovered, creates, skips, failures, dryRunTag,
	)
}

// adoptExternalSSBGVR is the ScanSettingBinding GroupVersionResource used by
// adoptK8sAdapter to delete/check-exists blocking external SSBs.
var adoptExternalSSBGVR = schema.GroupVersionResource{
	Group:    "compliance.openshift.io",
	Version:  "v1alpha1",
	Resource: "scansettingbindings",
}

// adoptK8sAdapter implements adoptexternal.Deleter and adoptexternal.Exister
// using each cluster source's dynamic client.
type adoptK8sAdapter struct {
	sources []clusterSource
}

func newAdoptK8sAdapter(sources []clusterSource) *adoptK8sAdapter {
	return &adoptK8sAdapter{sources: sources}
}

func (a *adoptK8sAdapter) dynClientFor(ctxName string) (dynamic.Interface, error) {
	for _, s := range a.sources {
		if s.contextName == ctxName {
			return s.dynClient, nil
		}
	}
	return nil, fmt.Errorf("context %q not found in cluster sources", ctxName)
}

// DeleteScanSettingBinding implements adoptexternal.Deleter. IMP-ADEXT-004
func (a *adoptK8sAdapter) DeleteScanSettingBinding(ctxName, namespace, name string) error {
	dyn, err := a.dynClientFor(ctxName)
	if err != nil {
		return err
	}
	return dyn.Resource(adoptExternalSSBGVR).Namespace(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
}

// ScanSettingBindingExists implements adoptexternal.Exister. IMP-ADEXT-006, IMP-ADEXT-007
func (a *adoptK8sAdapter) ScanSettingBindingExists(ctxName, namespace, name string) (bool, error) {
	dyn, err := a.dynClientFor(ctxName)
	if err != nil {
		return false, err
	}
	_, getErr := dyn.Resource(adoptExternalSSBGVR).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if getErr != nil {
		if apierrors.IsNotFound(getErr) {
			return false, nil
		}
		return false, getErr
	}
	return true, nil
}

// interactiveConfirmer prompts the operator on stderr/stdin for the
// --delete-existing confirmation. config.Parse already guarantees stdin is a
// TTY whenever this is reached without --yes (IMP-CLI-034), so a blocking
// read here is safe.
// IMP-ADEXT-004, IMP-ADEXT-005
func interactiveConfirmer() adoptexternal.Confirmer {
	return func(prompt string) (bool, error) {
		fmt.Fprintf(os.Stderr, "%s ", prompt)
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return false, err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		return answer == "y" || answer == "yes", nil
	}
}

// plannedScanFor builds the resumable PlannedScan entry for a scanName
// that's about to have its blocking external ScanSettingBinding(s) backed
// up and (possibly) deleted. IMP-ADEXT-002, IMP-ADEXT-011
func plannedScanFor(scanName, cronSchedule string, payload models.ACSPayload, blockers []merge.ClusterSSB) adoptexternal.PlannedScan {
	ps := adoptexternal.PlannedScan{
		ScanName:   scanName,
		Profiles:   payload.ScanConfig.Profiles,
		Schedule:   cronSchedule,
		ClusterIDs: payload.Clusters,
		Status:     adoptexternal.StatusBackedUp,
	}
	for _, b := range blockers {
		if b.SSB == nil {
			continue
		}
		ps.Sources = append(ps.Sources, adoptexternal.SourceRef{
			Context:         b.Context,
			Namespace:       b.SSB.Namespace,
			SSBName:         b.SSB.Name,
			ScanSettingName: b.SSB.ScanSettingName,
		})
	}
	return ps
}

// logAndRecordResult logs result to the console and records its outcome
// (including any "conflict"/"api" problem) into item, matching the
// console/report conventions used throughout Step 5. item.Action/Reason are
// always set from result; item.Source is left to the caller.
func (r *Runner) logAndRecordResult(collector *problems.Collector, scanName string, result reconcile.ActionResult, item *report.Item) {
	item.Action = result.Action
	item.Reason = result.Reason
	item.Attempts = result.Attempts
	item.ACSScanConfigID = result.AcsScanConfigID

	switch result.Action {
	case "create":
		r.printer.OK(fmt.Sprintf("created %q (id=%s)", scanName, result.AcsScanConfigID))
	case "update":
		r.printer.OK(fmt.Sprintf("updated %q (id=%s)", scanName, result.AcsScanConfigID))
	case "skip":
		r.printer.Warn(fmt.Sprintf("skipped %q: %s", scanName, result.Reason))
		collector.Add(problems.Problem{
			Severity:    "warning",
			Category:    "conflict",
			ResourceRef: scanName,
			Description: result.Reason,
			FixHint:     "Use --overwrite-existing to update existing scan configurations, or delete the existing configuration in ACS first",
		})
	case "fail":
		r.printer.Fail(fmt.Sprintf("failed %q: %s", scanName, result.Reason))
		if result.Error != nil {
			item.Error = result.Error.Error()
		}
		collector.Add(problems.Problem{
			Severity:    "error",
			Category:    "api",
			ResourceRef: scanName,
			Description: result.Reason,
			Skipped:     true,
		})
	}
}

// reconcileWithAdoptExternal reconciles one merged scan configuration,
// resolving any blocking external ScanSettingBinding first (IMP-ADEXT-*)
// when reconcile would otherwise attempt a create or update. It returns a
// report.Item with Action/Reason/Attempts/ACSScanConfigID/Error set; the
// caller fills in Source.
func (r *Runner) reconcileWithAdoptExternal(
	ctx context.Context,
	reconciler *reconcile.Reconciler,
	collector *problems.Collector,
	payload models.ACSPayload,
	scanName, cronSchedule string,
	allClusterSSBs []merge.ClusterSSB,
	scanSettingsByBinding map[string]*models.ScanSetting,
	envStamp adoptexternal.EnvStamp,
	adoptK8s *adoptK8sAdapter,
	confirmer adoptexternal.Confirmer,
	ensureBackupDir func() error,
) report.Item {
	var item report.Item

	// Peek (dry-run, regardless of the overall run mode) to learn the
	// action reconcile would take without any side effect: a plain "skip"
	// (already exists, not overwriting) needs no blocker resolution at all,
	// and attempting a blocked create/update against ACS directly would
	// fail with a confusing raw error instead of our actionable "gitops"
	// problem.
	peekReconciler := &reconcile.Reconciler{Client: reconciler.Client, Options: reconciler.Options}
	peekReconciler.Options.DryRun = true
	peek := peekReconciler.Reconcile(ctx, payload)

	if peek.Action == "skip" {
		r.logAndRecordResult(collector, scanName, peek, &item)
		return item
	}

	// A write (create or update) would be attempted: find any blocking
	// external ScanSettingBinding(s). IMP-ADEXT-001
	blockers := adoptexternal.DetectBlockers(allClusterSSBs, scanName)
	if len(blockers) == 0 {
		result := reconciler.Reconcile(ctx, payload)
		r.logAndRecordResult(collector, scanName, result, &item)
		return item
	}

	if r.cfg.DryRun {
		// IMP-ADEXT-014: dry-run never backs up, deletes, or writes to ACS.
		for _, msg := range adoptexternal.DryRunBlocker(blockers, scanName, r.cfg.DeleteExisting) {
			r.printer.OK(msg)
		}
		r.logAndRecordResult(collector, scanName, peek, &item)
		return item
	}

	perCluster := make(map[string][]merge.ClusterSSB)
	for _, b := range blockers {
		perCluster[b.Context] = append(perCluster[b.Context], b)
	}

	if err := ensureBackupDir(); err != nil {
		r.printer.Fail(err.Error())
		collector.Add(problems.Problem{
			Severity:    "error",
			Category:    "gitops",
			ResourceRef: scanName,
			Description: err.Error(),
			FixHint:     "Point --backup-dir at an empty directory, or one containing a plan.json from a prior adopt-external run",
			Skipped:     true,
		})
		item.Action, item.Reason, item.Error = "fail", err.Error(), err.Error()
		return item
	}

	plan := adoptexternal.Plan{
		Env:   envStamp,
		Scans: map[string]adoptexternal.PlannedScan{scanName: plannedScanFor(scanName, cronSchedule, payload, blockers)},
	}
	var scanSettings []adoptexternal.ScanSettingSource
	for _, b := range blockers {
		if b.SSB == nil {
			continue
		}
		if ss, ok := scanSettingsByBinding[b.Context+"/"+b.SSB.Name]; ok && ss != nil {
			scanSettings = append(scanSettings, adoptexternal.ScanSettingSource{Context: b.Context, ScanSetting: *ss})
		}
	}
	// IMP-ADEXT-002: backup MUST be written, and complete, before any
	// ScanSettingBinding is deleted.
	if err := adoptexternal.WriteBackup(r.cfg.BackupDir, plan, blockers, scanSettings); err != nil {
		r.printer.Fail(fmt.Sprintf("failed to back up blocking resources for %q: %v", scanName, err))
		item.Action, item.Reason, item.Error = "fail", err.Error(), err.Error()
		return item
	}
	r.printer.OK(fmt.Sprintf("backed up %d blocking resource(s) for %q to %s", len(blockers), scanName, r.cfg.BackupDir))

	// IMP-ADEXT-003..008: report-and-skip, or delete-with-confirmation plus
	// the settle-timeout self-heal guard, independently per cluster.
	proceed, resolveErr := adoptexternal.ResolveMergedBlockers(
		collector, scanName, perCluster, r.cfg.DeleteExisting, r.cfg.ConfirmYes,
		adoptK8s, confirmer, adoptK8s, r.cfg.SettleTimeout, nil,
	)
	if resolveErr != nil {
		r.printer.Fail(fmt.Sprintf("failed to resolve blocking resources for %q: %v", scanName, resolveErr))
		item.Action, item.Reason, item.Error = "fail", resolveErr.Error(), resolveErr.Error()
		return item
	}
	if !proceed {
		reason := "blocked by external ScanSettingBinding(s); see problems for remediation"
		r.printer.Warn(fmt.Sprintf("skipped %q: %s", scanName, reason))
		item.Action, item.Reason = "skip", reason
		return item
	}

	// IMP-ADEXT-009: the actual create/update always goes through the
	// normal reconcile path (idempotent pre-list), never a second/duplicate
	// write mechanism.
	result := reconciler.Reconcile(ctx, payload)
	r.logAndRecordResult(collector, scanName, result, &item)
	if item.Action == "create" {
		// IMP-ADEXT-010: the ScanSetting(s) the deleted external SSB(s)
		// referenced are intentionally left in place; flag them as
		// possibly orphaned rather than deleting them ourselves.
		for _, ss := range scanSettings {
			collector.Add(adoptexternal.OrphanWarning(ss.ScanSetting.Name, ss.Context, ss.ScanSetting.Namespace))
		}
	}
	return item
}

// acsClusterListerAdapter adapts acs.Client to discover.ACSClusterLister.
type acsClusterListerAdapter struct {
	client acs.Client
	ctx    context.Context
}

func (a *acsClusterListerAdapter) ListClusters() ([]discover.ACSCluster, error) {
	clusters, err := a.client.ListClusters(a.ctx)
	if err != nil {
		return nil, err
	}
	result := make([]discover.ACSCluster, len(clusters))
	for i, c := range clusters {
		result[i] = discover.ACSCluster{
			ID:                c.ID,
			Name:              c.Name,
			ProviderClusterID: c.ProviderMetadataClusterID,
		}
	}
	return result, nil
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// ListSSBs implements --list-ssbs mode (IMP-CLI-029): discovers SSBs from all
// cluster sources, applies --exclude filtering, prints namespace/name sorted to
// w, and returns exit code 0. ACS is not contacted.
func ListSSBs(ctx context.Context, cfg *config.Config, w io.Writer) int {
	printer := status.New()
	collector := problems.New()

	sources, err := (&Runner{cfg: cfg, acsClient: nil, printer: printer}).buildClusterSources(ctx, collector)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return ExitFatalError
	}

	var allSSBs []models.ScanSettingBinding
	for _, src := range sources {
		ssbs, fetchErr := src.coClient.ListScanSettingBindings(ctx)
		if fetchErr != nil {
			printer.Warn(fmt.Sprintf("context %q: failed to list SSBs: %v", src.contextName, fetchErr))
			continue
		}
		allSSBs = append(allSSBs, ssbs...)
	}

	allSSBs = filter.ExcludeSSBs(allSSBs, cfg.ExcludePatterns)
	listssbs.Print(allSSBs, w)
	return ExitSuccess
}
