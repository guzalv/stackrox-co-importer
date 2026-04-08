// Package run orchestrates the full importer pipeline: fetch CO resources,
// map to ACS payloads, merge, reconcile, adopt, and report.
package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/stackrox/co-importer/internal/acs"
	"github.com/stackrox/co-importer/internal/adopt"
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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
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

	// Step 3: For each context, list SSBs, get ScanSettings, build merge inputs
	r.printer.Stage("Fetching CO resources and building payloads", "")
	var allClusterSSBs []merge.ClusterSSB
	type ssbDetail struct {
		namespace       string
		bindingName     string
		scanSettingName string
	}
	ssbDetails := make(map[string]ssbDetail)

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
	var adoptionRequests []adopt.AdoptionRequest
	createCount, skipCount, failCount, updateCount := 0, 0, 0, 0

	for _, mc := range mergedConfigs {
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

		result := reconciler.Reconcile(ctx, payload)

		detail := ssbDetails[mc.ScanName]
		item := report.Item{
			Source: report.ItemSource{
				Namespace:       detail.namespace,
				BindingName:     detail.bindingName,
				ScanSettingName: detail.scanSettingName,
			},
			Action:          result.Action,
			Reason:          result.Reason,
			Attempts:        result.Attempts,
			ACSScanConfigID: result.AcsScanConfigID,
		}

		switch result.Action {
		case "create":
			createCount++
			r.printer.OK(fmt.Sprintf("created %q (id=%s)", mc.ScanName, result.AcsScanConfigID))
			if !r.cfg.DryRun {
				for _, cssb := range allClusterSSBs {
					if cssb.SSB.Name == mc.ScanName {
						adoptionRequests = append(adoptionRequests, adopt.AdoptionRequest{
							Context:        cssb.Context,
							Namespace:      cssb.SSB.Namespace,
							SSBName:        cssb.SSB.Name,
							CurrentSetting: cssb.SSB.ScanSettingName,
							TargetSetting:  mc.ScanName,
						})
					}
				}
			}
		case "update":
			updateCount++
			r.printer.OK(fmt.Sprintf("updated %q (id=%s)", mc.ScanName, result.AcsScanConfigID))
		case "skip":
			skipCount++
			r.printer.Warn(fmt.Sprintf("skipped %q: %s", mc.ScanName, result.Reason))
			collector.Add(problems.Problem{
				Severity:    "warning",
				Category:    "conflict",
				ResourceRef: mc.ScanName,
				Description: result.Reason,
				FixHint:     "Use --overwrite-existing to update existing scan configurations, or delete the existing configuration in ACS first",
			})
		case "fail":
			failCount++
			r.printer.Fail(fmt.Sprintf("failed %q: %s", mc.ScanName, result.Reason))
			if result.Error != nil {
				item.Error = result.Error.Error()
			}
			collector.Add(problems.Problem{
				Severity:    "error",
				Category:    "api",
				ResourceRef: mc.ScanName,
				Description: result.Reason,
				Skipped:     true,
			})
		}
		items = append(items, item)
	}

	// Step 6: Run adoption for created configs (if not dry-run)
	if len(adoptionRequests) > 0 && !r.cfg.DryRun {
		r.printer.Stage("Running adoption workflow", "")
		adoptK8s := newAdoptionK8sAdapter(sources)
		adoptResult := adopt.RunAdoption(adoptK8s, adoptionRequests, adopt.DefaultPollTimeout)
		for _, info := range adoptResult.InfoLogs {
			r.printer.OK(info)
		}
		for _, warn := range adoptResult.Warnings {
			r.printer.Warn(warn)
		}
	}

	// Step 7: Build and write report
	// For report, updates count as creates
	reportCreateCount := createCount + updateCount
	r.writeReportFull(collector, items, totalDiscovered, reportCreateCount, skipCount, failCount)

	// Step 8: Print console summary (IMP-CLI-020)
	r.printSummary(totalDiscovered, createCount+updateCount, skipCount, failCount)

	// Step 9: Return exit code
	if failCount > 0 {
		return ExitPartialError
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

// adoptionK8sAdapter adapts cluster sources to adopt.K8sClient.
type adoptionK8sAdapter struct {
	sources []clusterSource
}

func newAdoptionK8sAdapter(sources []clusterSource) *adoptionK8sAdapter {
	return &adoptionK8sAdapter{sources: sources}
}

var ssbGVR = schema.GroupVersionResource{
	Group:    "compliance.openshift.io",
	Version:  "v1alpha1",
	Resource: "scansettingbindings",
}

var ssGVR = schema.GroupVersionResource{
	Group:    "compliance.openshift.io",
	Version:  "v1alpha1",
	Resource: "scansettings",
}

func (a *adoptionK8sAdapter) ScanSettingExists(ctxName, namespace, name string) (bool, error) {
	for _, src := range a.sources {
		if src.contextName == ctxName {
			_, err := src.dynClient.Resource(ssGVR).Namespace(namespace).Get(
				context.Background(), name, metav1.GetOptions{})
			if err != nil {
				return false, nil
			}
			return true, nil
		}
	}
	return false, fmt.Errorf("context %q not found in sources", ctxName)
}

func (a *adoptionK8sAdapter) PatchSSBSettingsRef(ctxName, namespace, ssbName, newSettingsRef string) error {
	for _, src := range a.sources {
		if src.contextName != ctxName {
			continue
		}
		patch := map[string]interface{}{
			"settingsRef": map[string]interface{}{
				"name": newSettingsRef,
			},
		}
		patchBytes, err := json.Marshal(patch)
		if err != nil {
			return fmt.Errorf("marshaling patch: %w", err)
		}
		_, err = src.dynClient.Resource(ssbGVR).Namespace(namespace).Patch(
			context.Background(), ssbName, types.MergePatchType, patchBytes, metav1.PatchOptions{})
		return err
	}
	return fmt.Errorf("context %q not found in sources", ctxName)
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
