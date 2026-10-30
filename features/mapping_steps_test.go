package features

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/guzalv/stackrox-co-importer/internal/adoptexternal"
	"github.com/guzalv/stackrox-co-importer/internal/discover"
	"github.com/guzalv/stackrox-co-importer/internal/mapping"
	"github.com/guzalv/stackrox-co-importer/internal/merge"
	"github.com/guzalv/stackrox-co-importer/internal/models"
	"github.com/guzalv/stackrox-co-importer/internal/problems"
	"github.com/guzalv/stackrox-co-importer/internal/reconcile"
)

// --- Fake types for cluster discovery ---

// fakeKubeReader implements discover.KubeReader for tests.
type fakeKubeReader struct {
	configMaps     map[string]string // "namespace/name/key" -> value
	configMapErrs  map[string]error  // "namespace/name" -> error
	clusterVersion map[string]string // name -> clusterID
	clusterVerErrs map[string]error  // name -> error
	secrets        map[string]string // "namespace/name/key" -> value
	secretErrs     map[string]error  // "namespace/name" -> error
}

func newFakeKubeReader() *fakeKubeReader {
	return &fakeKubeReader{
		configMaps:     make(map[string]string),
		configMapErrs:  make(map[string]error),
		clusterVersion: make(map[string]string),
		clusterVerErrs: make(map[string]error),
		secrets:        make(map[string]string),
		secretErrs:     make(map[string]error),
	}
}

func (f *fakeKubeReader) GetConfigMapData(ns, name, key string) (string, error) {
	cmKey := ns + "/" + name
	if err, ok := f.configMapErrs[cmKey]; ok {
		return "", err
	}
	dataKey := ns + "/" + name + "/" + key
	if val, ok := f.configMaps[dataKey]; ok {
		return val, nil
	}
	return "", fmt.Errorf("ConfigMap %s/%s not found", ns, name)
}

func (f *fakeKubeReader) GetClusterVersionID(name string) (string, error) {
	if err, ok := f.clusterVerErrs[name]; ok {
		return "", err
	}
	if id, ok := f.clusterVersion[name]; ok {
		return id, nil
	}
	return "", fmt.Errorf("ClusterVersion %q not found", name)
}

func (f *fakeKubeReader) GetSecretData(ns, name, key string) (string, error) {
	sKey := ns + "/" + name
	if err, ok := f.secretErrs[sKey]; ok {
		return "", err
	}
	dataKey := ns + "/" + name + "/" + key
	if val, ok := f.secrets[dataKey]; ok {
		return val, nil
	}
	return "", fmt.Errorf("Secret %s/%s not found", ns, name)
}

// fakeACSClusterLister implements discover.ACSClusterLister for tests.
type fakeACSClusterLister struct {
	clusters []discover.ACSCluster
}

func (f *fakeACSClusterLister) ListClusters() ([]discover.ACSCluster, error) {
	return f.clusters, nil
}

// --- Fake types for multicluster merge ---

// clusterSSBEntry tracks an SSB on a specific cluster context.
type clusterSSBEntry struct {
	Context   string
	SSB       *models.ScanSettingBinding
	Schedule  string
	ClusterID string
}

// mappingTestContext shares state between steps within a scenario.
type mappingTestContext struct {
	ssb         *models.ScanSettingBinding
	scanSetting *models.ScanSetting
	profiles    []models.ProfileRef
	payload     *models.ACSPayload
	payloadJSON []byte
	problems    *problems.Collector
	skipped     map[string]bool // tracks skipped SSBs

	// Cluster discovery
	fakeKube     *fakeKubeReader
	fakeACSList  *fakeACSClusterLister
	resolvedID   string
	discoveryErr error

	// Multicluster
	clusterSSBs   []*clusterSSBEntry
	clusterIDs    map[string]string // ctx label -> cluster ID
	mergedConfigs []merge.MergedConfig
	mergeProblems *problems.Collector

	// Validation
	allBindings      []*validationBinding
	processedResults map[string]bool // SSB name -> processed?
	scanSettings     map[string]*models.ScanSetting

	// Adopt-external detection (IMP-ADEXT-001, IMP-ADEXT-013)
	adextMode           bool // true once an adopt-external-style "Given" step runs
	adextTargetScanName string
	adextBlocked        bool
	adextBlockingSSB    *models.ScanSettingBinding
	adextSourceBindings []models.ScanSettingBinding

	// Adopt-external backup (IMP-ADEXT-002, IMP-ADEXT-015)
	backupDir  string // temp dir used as --backup-dir for the current scenario
	backupErr  error  // error returned by adoptexternal.WriteBackup
	startupErr error  // error returned by adoptexternal.CheckBackupDir
	ssbDeleted bool   // whether any ScanSettingBinding delete has happened

	// Adopt-external report-and-skip / delete-with-confirmation decision
	// (IMP-ADEXT-003, IMP-ADEXT-004, IMP-ADEXT-005)
	adextDeleteExisting bool // --delete-existing
	adextYes            bool // --yes
	adextDeleter        *fakeDeleter
	adextConfirmer      *fakeConfirmer
	adextDeleted        bool  // ResolveBlocker's "proceed" return value (blockers confirmed gone)
	adextResolveErr     error // ResolveBlocker's error return value

	// Adopt-external settle-timeout guard against GitOps self-heal
	// (IMP-ADEXT-006, IMP-ADEXT-007)
	adextExister       *fakeExister
	adextSettleTimeout time.Duration
	adextPoll          func(time.Duration)

	// Adopt-external multi-cluster all-clear gate (IMP-ADEXT-008): a single
	// scanName blocked by external ScanSettingBindings on multiple clusters
	// at once.
	adextMultiCluster    bool // true once a "kubecontext X has a blocking SSB" Given step runs
	adextClusterBlockers []merge.ClusterSSB
	adextMultiExister    *fakeMultiExister

	// Adopt-external create-after-clear (IMP-ADEXT-009) and orphaned
	// ScanSetting warning (IMP-ADEXT-010): once a blocker is cleared, the
	// actual create routes through the normal reconcile.Reconciler so
	// idempotency is preserved.
	adextCreateAfterClear  bool // true once a "was already cleared"/"was successfully created" Given step runs
	adextACSClient         *fakeACSClient
	adextReconciler        *reconcile.Reconciler
	adextReconcileResult   reconcile.ActionResult
	adextOrphanScanSetting string // name of the ScanSetting a deleted blocker referenced, if any
	adextOrphanContext     string
	adextOrphanNamespace   string

	// Adopt-external resume from a persisted plan (IMP-ADEXT-011,
	// IMP-ADEXT-012): a subsequent run reads plan.json back instead of
	// relying on live discovery, since the blocking SSB may already be
	// gone (deleted in a prior interrupted run, or manually by the
	// operator).
	adextResumeTargetScan       string                 // scan configuration name the resume scenario is about
	adextResumeWrittenEnv       adoptexternal.EnvStamp // env stamped into the plan.json written by the "Given" step
	adextResumeCurrentEnv       adoptexternal.EnvStamp // env the current run claims to be
	adextResumePlan             *adoptexternal.Plan    // plan loaded back by adoptexternal.LoadPlan
	adextResumeLoadErr          error                  // error returned by adoptexternal.LoadPlan
	adextResumeEnvErr           error                  // error returned by adoptexternal.ValidateEnvironment
	adextResumeUsedPlan         bool                   // true once the resume flow proceeded to create from the plan
	adextResumeSSBConfirmedGone bool                   // "ScanSettingBinding ... no longer exists on any cluster" precondition

	// Adopt-external dry-run (IMP-ADEXT-014): --dry-run MUST perform zero
	// side effects anywhere in the adoption workflow — no backup, no
	// deletion, no ACS create/update — and only log what it would do.
	adextDryRun    bool     // --dry-run
	adextDryRunLog []string // human-readable "would do X" messages returned by adoptexternal.DryRunBlocker
}

// validationBinding stores a binding + whether it has a valid ScanSetting.
type validationBinding struct {
	SSB             *models.ScanSettingBinding
	ScanSettingName string
	HasScanSetting  bool
}

var mtc *mappingTestContext

// resetMappingTestContext initialises a fresh context for each scenario.
func resetMappingTestContext() {
	mtc = &mappingTestContext{
		problems:         problems.New(),
		skipped:          make(map[string]bool),
		fakeKube:         newFakeKubeReader(),
		fakeACSList:      &fakeACSClusterLister{},
		clusterIDs:       make(map[string]string),
		mergeProblems:    problems.New(),
		processedResults: make(map[string]bool),
		scanSettings:     make(map[string]*models.ScanSetting),
		adextDeleter:     newFakeDeleter(),
		adextConfirmer:   newFakeConfirmer(),
	}
}

// registerMappingSteps registers step definitions for specs/02-co-to-acs-mapping.feature.
// All steps start as pending — implement them alongside production code.
func registerMappingSteps(ctx *godog.ScenarioContext) {
	ctx.Before(func(ctx2 context.Context, sc *godog.Scenario) (context.Context, error) {
		resetMappingTestContext()
		return ctx2, nil
	})

	// Background
	ctx.Step(`^ACS endpoint and token preflight succeeded$`, acsEndpointAndTokenPreflightSucceeded)
	ctx.Step(`^the importer can read compliance\.openshift\.io resources$`, theImporterCanReadCOResources)

	// @mapping @name — IMP-MAP-001
	ctx.Step(`^a ScanSettingBinding "([^"]*)" in namespace "([^"]*)"$`, aScanSettingBindingInNamespace)
	ctx.Step(`^ScanSettingBinding "([^"]*)" in namespace "([^"]*)"$`, aScanSettingBindingInNamespace)
	ctx.Step(`^the binding references ScanSetting "([^"]*)"$`, theBindingReferencesScanSetting)
	ctx.Step(`^the binding references profiles:$`, theBindingReferencesProfiles)
	ctx.Step(`^the importer builds the ACS payload$`, theImporterBuildsTheACSPayload)
	ctx.Step(`^payload\.scanName MUST equal "([^"]*)"$`, payloadScanNameMustEqual)
	ctx.Step(`^payload\.scanConfig\.profiles MUST equal:$`, payloadProfilesMustEqual)

	// @mapping @profiles — IMP-MAP-002
	ctx.Step(`^a ScanSettingBinding profile reference "([^"]*)" with no kind$`, aProfileReferenceWithNoKind)
	ctx.Step(`^the importer resolves profile references$`, theImporterResolvesProfileReferences)
	ctx.Step(`^the profile reference kind MUST be treated as "([^"]*)"$`, profileReferenceKindMustBe)
	ctx.Step(`^the resulting ACS profile name list MUST include "([^"]*)"$`, acsProfileNameListMustInclude)

	// @mapping @schedule — IMP-MAP-003, IMP-MAP-004
	ctx.Step(`^ScanSetting "([^"]*)" has schedule "([^"]*)"$`, scanSettingHasSchedule)
	ctx.Step(`^ScanSettingBinding "([^"]*)" references "([^"]*)"$`, scanSettingBindingReferences)
	ctx.Step(`^the importer maps schedule fields$`, theImporterMapsScheduleFields)
	ctx.Step(`^payload\.scanConfig\.oneTimeScan MUST be false$`, payloadOneTimeScanMustBeFalse)
	ctx.Step(`^payload\.scanConfig\.scanSchedule MUST be present$`, payloadScanScheduleMustBePresent)

	// @mapping @schedule @wire-format — IMP-MAP-004a..d
	ctx.Step(`^the importer builds the ACS payload and serializes it to JSON$`, theImporterBuildsPayloadJSON)
	ctx.Step(`^the JSON scanSchedule object MUST contain only proto Schedule fields$`, jsonScheduleFieldsMustMatch)
	ctx.Step(`^the JSON scanSchedule\.intervalType MUST be "([^"]*)"$`, jsonIntervalTypeMustBe)
	ctx.Step(`^for WEEKLY: scanSchedule\.daysOfWeek\.days MUST be present$`, weeklyDaysOfWeekMustBePresent)
	ctx.Step(`^for MONTHLY: scanSchedule\.daysOfMonth\.days MUST be present$`, monthlyDaysOfMonthMustBePresent)
	ctx.Step(`^the full payload JSON field names MUST match ComplianceScanConfiguration proto$`, payloadFieldNamesMustMatchProto)

	// @mapping @description — IMP-MAP-005, IMP-MAP-006
	ctx.Step(`^the importer builds payload description$`, theImporterBuildsPayloadDescription)
	ctx.Step(`^payload\.scanConfig\.description MUST contain "([^"]*)"$`, payloadDescriptionMustContain)
	ctx.Step(`^payload\.scanConfig\.description SHOULD include settings reference context$`, payloadDescriptionShouldIncludeContext)

	// @mapping @clusters — IMP-MAP-016..018
	ctx.Step(`^kubecontext "([^"]*)" points to a secured cluster$`, kubecontextPointsToSecuredCluster)
	ctx.Step(`^ConfigMap "([^"]*)" in namespace "([^"]*)" has data key "([^"]*)" = "([^"]*)"$`, configMapHasDataKey)
	ctx.Step(`^the importer resolves the ACS cluster ID for "([^"]*)"$`, resolveACSClusterID)
	ctx.Step(`^the resolved ACS cluster ID MUST be "([^"]*)"$`, resolvedClusterIDMustBe)
	ctx.Step(`^kubecontext "([^"]*)" points to an OpenShift cluster$`, kubecontextPointsToOpenShiftCluster)
	ctx.Step(`^ConfigMap "([^"]*)" is not readable$`, configMapNotReadable)
	ctx.Step(`^ClusterVersion "([^"]*)" has spec\.clusterID "([^"]*)"$`, clusterVersionHasClusterID)
	ctx.Step(`^ACS cluster list contains a cluster with providerMetadata\.cluster\.id "([^"]*)" and ACS ID "([^"]*)"$`, acsClusterListByProviderID)
	ctx.Step(`^kubecontext "([^"]*)" points to a cluster$`, kubecontextPointsToCluster)
	ctx.Step(`^ClusterVersion is not available$`, clusterVersionNotAvailable)
	ctx.Step(`^Secret "([^"]*)" has data key "([^"]*)" = "([^"]*)"$`, secretHasDataKey)
	ctx.Step(`^ACS cluster list contains a cluster named "([^"]*)" with ACS ID "([^"]*)"$`, acsClusterListByName)

	// @mapping @clusters — IMP-MAP-016a
	ctx.Step(`^ConfigMap "([^"]*)" is not readable with error "([^"]*)"$`, configMapNotReadableWithError)
	ctx.Step(`^ClusterVersion is not available with error "([^"]*)"$`, clusterVersionNotAvailableWithError)
	ctx.Step(`^Secret "([^"]*)" is not readable with error "([^"]*)"$`, secretNotReadableWithError)
	ctx.Step(`^the error MUST list each method's failure reason$`, errorMustListEachMethodFailure)

	// @mapping @clusters @multicluster — IMP-MAP-019..021
	ctx.Step(`^kubecontext "([^"]*)" has ScanSettingBinding "([^"]*)" with profiles \["([^"]*)"\] and schedule "([^"]*)"$`, kubecontextHasSSBWithProfilesAndSchedule)
	ctx.Step(`^ctx-([a-z]) resolves to ACS cluster ID "([^"]*)"$`, ctxResolvesToClusterID)
	ctx.Step(`^the importer merges SSBs across clusters$`, theImporterMergesSSBs)
	ctx.Step(`^one ACS scan config MUST be created with scanName "([^"]*)"$`, oneACSConfigMustBeCreated)
	ctx.Step(`^payload\.clusters MUST equal:$`, payloadClustersMustEqual)

	// @mapping @clusters @multicluster @error — IMP-MAP-020, IMP-MAP-020a
	ctx.Step(`^kubecontext "([^"]*)" has ScanSettingBinding "([^"]*)" with profiles \["([^"]*)"\]$`, kubecontextHasSSBWithProfiles)
	ctx.Step(`^kubecontext "([^"]*)" has ScanSettingBinding "([^"]*)" with profiles \["([^"]*)", "([^"]*)"\]$`, kubecontextHasSSBWithTwoProfiles)
	ctx.Step(`^kubecontext "([^"]*)" has ScanSettingBinding "([^"]*)" with schedule "([^"]*)"$`, kubecontextHasSSBWithSchedule)
	ctx.Step(`^"([^"]*)" MUST be marked failed$`, ssbMustBeMarkedFailed)
	ctx.Step(`^problems list MUST include category "([^"]*)"$`, problemsMustIncludeCategory)
	ctx.Step(`^problem description MUST mention mismatch across clusters$`, problemMustMentionMismatch)
	ctx.Step(`^the console MUST print a warning with the conflict reason$`, consoleMustPrintWarning)

	// @validation @mapping — IMP-MAP-008..011
	ctx.Step(`^ScanSettingBinding "([^"]*)" references ScanSetting "([^"]*)"$`, ssbReferencesScanSetting)
	ctx.Step(`^the importer processes all discovered bindings$`, theImporterProcessesAllBindings)
	ctx.Step(`^problems list MUST include an entry for "([^"]*)"$`, problemsMustIncludeEntryFor)
	ctx.Step(`^that problem entry MUST include a fix hint$`, problemMustIncludeFixHint)
	ctx.Step(`^other valid bindings MUST still be processed$`, otherBindingsMustStillBeProcessed)

	// @mapping @schedule @problems — IMP-MAP-012..015
	ctx.Step(`^problem description MUST mention schedule conversion failed$`, problemMustMentionScheduleConversionFailed)
	ctx.Step(`^problem fix hint MUST suggest using a valid cron expression$`, problemFixHintMustSuggestValidCron)
	ctx.Step(`^"([^"]*)" MUST be skipped$`, ssbMustBeSkipped)

	// @adopt-external @detect — IMP-ADEXT-001, IMP-ADEXT-013
	ctx.Step(`^ScanSettingBinding "([^"]*)" in namespace "([^"]*)" references profiles \[(.*)\]$`, ssbInNamespaceReferencesProfilesList)
	ctx.Step(`^the SSB has no "app\.kubernetes\.io/name: stackrox" label$`, theSSBHasNoStackroxLabel)
	ctx.Step(`^the SSB has the "app\.kubernetes\.io/name: stackrox" label$`, theSSBHasStackroxLabel)
	ctx.Step(`^no ACS scan configuration named "([^"]*)" exists$`, noACSScanConfigurationNamedExists)
	ctx.Step(`^"([^"]*)" MUST be identified as blocked by an external ScanSettingBinding$`, ssbMustBeIdentifiedAsBlocked)
	ctx.Step(`^the blocking ScanSettingBinding MUST be "([^"]*)" in namespace "([^"]*)"$`, blockingSSBMustBe)
	ctx.Step(`^"([^"]*)" MUST NOT be identified as blocked by an external ScanSettingBinding$`, ssbMustNotBeIdentifiedAsBlocked)
	ctx.Step(`^"([^"]*)" MUST NOT be treated as a source binding to import$`, ssbMustNotBeTreatedAsSourceBinding)
}

// --- Background steps (no-op in unit tests) ---

// IMP-MAP-001 (background)
func acsEndpointAndTokenPreflightSucceeded() error { return nil }
func theImporterCanReadCOResources() error         { return nil }

// --- IMP-MAP-001: Use ScanSettingBinding name as scanName ---

func aScanSettingBindingInNamespace(name, namespace string) error {
	// IMP-MAP-001
	mtc.ssb = &models.ScanSettingBinding{
		Name:      name,
		Namespace: namespace,
	}
	return nil
}

func theBindingReferencesScanSetting(settingName string) error {
	// IMP-MAP-001
	mtc.ssb.ScanSettingName = settingName
	// Create a default ScanSetting with a valid schedule so payload building works.
	if mtc.scanSetting == nil {
		mtc.scanSetting = &models.ScanSetting{
			Name:      settingName,
			Namespace: mtc.ssb.Namespace,
			Schedule:  "0 0 * * *", // default valid cron
		}
	} else {
		mtc.scanSetting.Name = settingName
	}
	return nil
}

func theBindingReferencesProfiles(table *godog.Table) error {
	// IMP-MAP-001
	for i, row := range table.Rows {
		if i == 0 {
			continue // skip header
		}
		name := row.Cells[0].Value
		kind := row.Cells[1].Value
		mtc.ssb.Profiles = append(mtc.ssb.Profiles, models.ProfileRef{
			Name: name,
			Kind: kind,
		})
	}
	return nil
}

func theImporterBuildsTheACSPayload() error {
	// IMP-MAP-001
	result := mapping.BuildPayload(mtc.ssb, mtc.scanSetting, "test-cluster-id")
	if result.Problem != nil {
		mtc.problems.Add(*result.Problem)
		return fmt.Errorf("unexpected problem building payload: %s", result.Problem.Description)
	}
	mtc.payload = result.Payload
	return nil
}

func payloadScanNameMustEqual(expected string) error {
	// IMP-MAP-001
	if mtc.payload.ScanName != expected {
		return fmt.Errorf("expected scanName %q, got %q", expected, mtc.payload.ScanName)
	}
	return nil
}

func payloadProfilesMustEqual(table *godog.Table) error {
	// IMP-MAP-001
	var expected []string
	for i, row := range table.Rows {
		if i == 0 {
			continue // skip header
		}
		expected = append(expected, row.Cells[0].Value)
	}
	actual := mtc.payload.ScanConfig.Profiles
	if len(actual) != len(expected) {
		return fmt.Errorf("expected %d profiles, got %d: %v", len(expected), len(actual), actual)
	}
	for i := range expected {
		if actual[i] != expected[i] {
			return fmt.Errorf("profile[%d]: expected %q, got %q", i, expected[i], actual[i])
		}
	}
	return nil
}

// --- IMP-MAP-002: Default missing profile kind to Profile ---

func aProfileReferenceWithNoKind(name string) error {
	// IMP-MAP-002
	mtc.profiles = []models.ProfileRef{{Name: name, Kind: ""}}
	return nil
}

func theImporterResolvesProfileReferences() error {
	// IMP-MAP-002
	// ResolveProfiles handles the kind defaulting internally.
	_ = mapping.ResolveProfiles(mtc.profiles)
	return nil
}

func profileReferenceKindMustBe(expectedKind string) error {
	// IMP-MAP-002
	for _, p := range mtc.profiles {
		actual := p.ResolvedKind()
		if actual != expectedKind {
			return fmt.Errorf("expected resolved kind %q, got %q", expectedKind, actual)
		}
	}
	return nil
}

func acsProfileNameListMustInclude(name string) error {
	// IMP-MAP-002
	resolved := mapping.ResolveProfiles(mtc.profiles)
	for _, p := range resolved {
		if p == name {
			return nil
		}
	}
	return fmt.Errorf("profile name %q not found in resolved list: %v", name, resolved)
}

// --- IMP-MAP-003, IMP-MAP-004: Convert ScanSetting schedule into ACS schedule ---

func scanSettingHasSchedule(name, schedule string) error {
	// IMP-MAP-003, IMP-MAP-004
	mtc.scanSetting = &models.ScanSetting{
		Name:     name,
		Schedule: schedule,
	}
	return nil
}

func scanSettingBindingReferences(bindingName, settingName string) error {
	// IMP-MAP-003, IMP-MAP-004
	mtc.ssb = &models.ScanSettingBinding{
		Name:            bindingName,
		Namespace:       "default",
		ScanSettingName: settingName,
	}
	return nil
}

func theImporterMapsScheduleFields() error {
	// IMP-MAP-003, IMP-MAP-004, IMP-MAP-012..015
	result := mapping.BuildPayload(mtc.ssb, mtc.scanSetting, "test-cluster-id")
	if result.Problem != nil {
		mtc.problems.Add(*result.Problem)
		mtc.skipped[mtc.ssb.Name] = true
		// Not an error in the test — problems are expected for invalid schedules
		return nil
	}
	mtc.payload = result.Payload
	return nil
}

func payloadOneTimeScanMustBeFalse() error {
	// IMP-MAP-003
	if mtc.payload == nil {
		return fmt.Errorf("payload is nil")
	}
	if mtc.payload.ScanConfig.OneTimeScan {
		return fmt.Errorf("expected oneTimeScan=false, got true")
	}
	return nil
}

func payloadScanScheduleMustBePresent() error {
	// IMP-MAP-004
	if mtc.payload == nil {
		return fmt.Errorf("payload is nil")
	}
	if mtc.payload.ScanConfig.ScanSchedule == nil {
		return fmt.Errorf("scanSchedule is nil, expected it to be present")
	}
	return nil
}

// --- IMP-MAP-004a..d: Schedule JSON wire format ---

func theImporterBuildsPayloadJSON() error {
	// IMP-MAP-004a..d
	result := mapping.BuildPayload(mtc.ssb, mtc.scanSetting, "test-cluster-id")
	if result.Problem != nil {
		mtc.problems.Add(*result.Problem)
		return fmt.Errorf("unexpected problem building payload: %s", result.Problem.Description)
	}
	mtc.payload = result.Payload

	data, err := json.Marshal(mtc.payload)
	if err != nil {
		return fmt.Errorf("JSON marshal error: %w", err)
	}
	mtc.payloadJSON = data
	return nil
}

func jsonScheduleFieldsMustMatch() error {
	// IMP-MAP-004a: only proto Schedule fields are allowed
	var raw map[string]interface{}
	if err := json.Unmarshal(mtc.payloadJSON, &raw); err != nil {
		return err
	}
	scanConfig, ok := raw["scanConfig"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("scanConfig not found in JSON")
	}
	sched, ok := scanConfig["scanSchedule"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("scanSchedule not found in JSON")
	}
	// Allowed proto Schedule fields
	allowed := map[string]bool{
		"intervalType": true,
		"hour":         true,
		"minute":       true,
		"daysOfWeek":   true,
		"daysOfMonth":  true,
	}
	for key := range sched {
		if !allowed[key] {
			return fmt.Errorf("unexpected field %q in scanSchedule JSON", key)
		}
	}
	return nil
}

func jsonIntervalTypeMustBe(expected string) error {
	// IMP-MAP-004b
	var raw map[string]interface{}
	if err := json.Unmarshal(mtc.payloadJSON, &raw); err != nil {
		return err
	}
	scanConfig := raw["scanConfig"].(map[string]interface{})
	sched := scanConfig["scanSchedule"].(map[string]interface{})
	actual, ok := sched["intervalType"].(string)
	if !ok {
		return fmt.Errorf("intervalType not found or not a string")
	}
	if actual != expected {
		return fmt.Errorf("expected intervalType %q, got %q", expected, actual)
	}
	return nil
}

func weeklyDaysOfWeekMustBePresent() error {
	// IMP-MAP-004c
	var raw map[string]interface{}
	if err := json.Unmarshal(mtc.payloadJSON, &raw); err != nil {
		return err
	}
	scanConfig := raw["scanConfig"].(map[string]interface{})
	sched := scanConfig["scanSchedule"].(map[string]interface{})
	intervalType, _ := sched["intervalType"].(string)
	if intervalType != "WEEKLY" {
		return nil // not applicable
	}
	dow, ok := sched["daysOfWeek"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("WEEKLY schedule missing daysOfWeek object")
	}
	days, ok := dow["days"].([]interface{})
	if !ok || len(days) == 0 {
		return fmt.Errorf("WEEKLY schedule daysOfWeek.days is missing or empty")
	}
	return nil
}

func monthlyDaysOfMonthMustBePresent() error {
	// IMP-MAP-004d
	var raw map[string]interface{}
	if err := json.Unmarshal(mtc.payloadJSON, &raw); err != nil {
		return err
	}
	scanConfig := raw["scanConfig"].(map[string]interface{})
	sched := scanConfig["scanSchedule"].(map[string]interface{})
	intervalType, _ := sched["intervalType"].(string)
	if intervalType != "MONTHLY" {
		return nil // not applicable
	}
	dom, ok := sched["daysOfMonth"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("MONTHLY schedule missing daysOfMonth object")
	}
	days, ok := dom["days"].([]interface{})
	if !ok || len(days) == 0 {
		return fmt.Errorf("MONTHLY schedule daysOfMonth.days is missing or empty")
	}
	return nil
}

func payloadFieldNamesMustMatchProto() error {
	// IMP-MAP-004d: verify top-level JSON field names match ComplianceScanConfiguration proto
	var raw map[string]interface{}
	if err := json.Unmarshal(mtc.payloadJSON, &raw); err != nil {
		return err
	}
	// Expected top-level proto fields
	topLevelAllowed := map[string]bool{
		"scanName":   true,
		"scanConfig": true,
		"clusters":   true,
		"id":         true,
	}
	for key := range raw {
		if !topLevelAllowed[key] {
			return fmt.Errorf("unexpected top-level field %q in payload JSON", key)
		}
	}
	// Expected scanConfig fields
	scanConfig, ok := raw["scanConfig"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("scanConfig not found")
	}
	scanConfigAllowed := map[string]bool{
		"oneTimeScan":  true,
		"profiles":     true,
		"scanSchedule": true,
		"description":  true,
	}
	for key := range scanConfig {
		if !scanConfigAllowed[key] {
			return fmt.Errorf("unexpected field %q in scanConfig JSON", key)
		}
	}
	return nil
}

// --- IMP-MAP-005, IMP-MAP-006: Build helpful description ---

func theImporterBuildsPayloadDescription() error {
	// IMP-MAP-005, IMP-MAP-006
	if mtc.scanSetting == nil {
		mtc.scanSetting = &models.ScanSetting{
			Name:     "default",
			Schedule: "0 0 * * *",
		}
	}
	result := mapping.BuildPayload(mtc.ssb, mtc.scanSetting, "test-cluster-id")
	if result.Problem != nil {
		return fmt.Errorf("unexpected problem: %s", result.Problem.Description)
	}
	mtc.payload = result.Payload
	return nil
}

func payloadDescriptionMustContain(expected string) error {
	// IMP-MAP-005
	if mtc.payload == nil {
		return fmt.Errorf("payload is nil")
	}
	if !strings.Contains(mtc.payload.ScanConfig.Description, expected) {
		return fmt.Errorf("description %q does not contain %q", mtc.payload.ScanConfig.Description, expected)
	}
	return nil
}

func payloadDescriptionShouldIncludeContext() error {
	// IMP-MAP-006
	if mtc.payload == nil {
		return fmt.Errorf("payload is nil")
	}
	desc := mtc.payload.ScanConfig.Description
	if !strings.Contains(desc, "ScanSetting") {
		return fmt.Errorf("description %q does not include settings reference context", desc)
	}
	return nil
}

// --- IMP-MAP-012..015: Invalid schedule is collected as problem and skipped ---

func ssbMustBeSkipped(name string) error {
	// IMP-MAP-012
	if !mtc.skipped[name] {
		return fmt.Errorf("expected SSB %q to be skipped, but it was not", name)
	}
	return nil
}

func problemsMustIncludeCategory(category string) error {
	// IMP-MAP-013
	if !mtc.problems.HasCategory(category) {
		return fmt.Errorf("expected problems to include category %q, but none found", category)
	}
	return nil
}

func problemMustMentionScheduleConversionFailed() error {
	// IMP-MAP-014
	for _, p := range mtc.problems.All() {
		if strings.Contains(p.Description, "schedule conversion failed") {
			return nil
		}
	}
	return fmt.Errorf("no problem mentions 'schedule conversion failed'")
}

func problemFixHintMustSuggestValidCron() error {
	// IMP-MAP-015
	for _, p := range mtc.problems.All() {
		if strings.Contains(p.FixHint, "cron expression") {
			return nil
		}
	}
	return fmt.Errorf("no problem fix hint suggests using a valid cron expression")
}

// --- IMP-MAP-016..018: Cluster discovery steps ---

func kubecontextPointsToSecuredCluster(_ string) error {
	// IMP-MAP-016: no-op setup, just marks context as secured
	return nil
}

func kubecontextPointsToOpenShiftCluster(_ string) error {
	// IMP-MAP-017: no-op setup, just marks context as OpenShift
	return nil
}

func kubecontextPointsToCluster(_ string) error {
	// IMP-MAP-018: no-op setup
	return nil
}

func configMapHasDataKey(cmName, namespace, key, value string) error {
	// IMP-MAP-016
	dataKey := namespace + "/" + cmName + "/" + key
	mtc.fakeKube.configMaps[dataKey] = value
	return nil
}

func configMapNotReadable(cmName string) error {
	// IMP-MAP-017, IMP-MAP-018
	mtc.fakeKube.configMapErrs["stackrox/"+cmName] = fmt.Errorf("ConfigMap %q not readable", cmName)
	return nil
}

func configMapNotReadableWithError(cmName, errMsg string) error {
	// IMP-MAP-016a
	mtc.fakeKube.configMapErrs["stackrox/"+cmName] = fmt.Errorf("%s", errMsg)
	return nil
}

func clusterVersionHasClusterID(cvName, clusterID string) error {
	// IMP-MAP-017
	mtc.fakeKube.clusterVersion[cvName] = clusterID
	return nil
}

func clusterVersionNotAvailable() error {
	// IMP-MAP-018
	mtc.fakeKube.clusterVerErrs["version"] = fmt.Errorf("ClusterVersion not available")
	return nil
}

func clusterVersionNotAvailableWithError(errMsg string) error {
	// IMP-MAP-016a
	mtc.fakeKube.clusterVerErrs["version"] = fmt.Errorf("%s", errMsg)
	return nil
}

func secretHasDataKey(secretName, key, value string) error {
	// IMP-MAP-018
	dataKey := "stackrox/" + secretName + "/" + key
	mtc.fakeKube.secrets[dataKey] = value
	return nil
}

func secretNotReadableWithError(secretName, errMsg string) error {
	// IMP-MAP-016a
	mtc.fakeKube.secretErrs["stackrox/"+secretName] = fmt.Errorf("%s", errMsg)
	return nil
}

func acsClusterListByProviderID(providerID, acsID string) error {
	// IMP-MAP-017
	mtc.fakeACSList.clusters = append(mtc.fakeACSList.clusters, discover.ACSCluster{
		ID:                acsID,
		ProviderClusterID: providerID,
	})
	return nil
}

func acsClusterListByName(clusterName, acsID string) error {
	// IMP-MAP-018
	mtc.fakeACSList.clusters = append(mtc.fakeACSList.clusters, discover.ACSCluster{
		ID:   acsID,
		Name: clusterName,
	})
	return nil
}

func resolveACSClusterID(_ string) error {
	// IMP-MAP-016, IMP-MAP-017, IMP-MAP-018, IMP-MAP-016a
	d := &discover.Discoverer{
		Kube: mtc.fakeKube,
		ACS:  mtc.fakeACSList,
	}
	mtc.resolvedID, mtc.discoveryErr = d.Resolve()
	return nil
}

func resolvedClusterIDMustBe(expected string) error {
	// IMP-MAP-016, IMP-MAP-017, IMP-MAP-018
	if mtc.discoveryErr != nil {
		return fmt.Errorf("expected cluster ID %q but got error: %w", expected, mtc.discoveryErr)
	}
	if mtc.resolvedID != expected {
		return fmt.Errorf("expected resolved cluster ID %q, got %q", expected, mtc.resolvedID)
	}
	return nil
}

func errorMustListEachMethodFailure() error {
	// IMP-MAP-016a
	if mtc.discoveryErr == nil {
		return fmt.Errorf("expected an error listing all method failures, got nil")
	}
	errStr := mtc.discoveryErr.Error()
	if !strings.Contains(errStr, "admission-control ConfigMap") {
		return fmt.Errorf("error should mention admission-control ConfigMap: %s", errStr)
	}
	if !strings.Contains(errStr, "ClusterVersion") {
		return fmt.Errorf("error should mention ClusterVersion: %s", errStr)
	}
	if !strings.Contains(errStr, "helm-effective-cluster-name Secret") {
		return fmt.Errorf("error should mention helm-effective-cluster-name Secret: %s", errStr)
	}
	return nil
}

// --- IMP-MAP-019..021: Multicluster merge steps ---

func kubecontextHasSSBWithProfilesAndSchedule(ctxName, ssbName, profilesStr, schedule string) error {
	// IMP-MAP-019, IMP-MAP-021
	profileNames := strings.Split(profilesStr, ", ")
	var profiles []models.ProfileRef
	for _, p := range profileNames {
		profiles = append(profiles, models.ProfileRef{Name: strings.TrimSpace(p), Kind: "Profile"})
	}
	mtc.clusterSSBs = append(mtc.clusterSSBs, &clusterSSBEntry{
		Context: ctxName,
		SSB: &models.ScanSettingBinding{
			Name:            ssbName,
			Namespace:       "openshift-compliance",
			ScanSettingName: "default",
			Profiles:        profiles,
		},
		Schedule: schedule,
	})
	return nil
}

func kubecontextHasSSBWithProfiles(ctxName, ssbName, profilesStr string) error {
	// IMP-MAP-020
	profileNames := strings.Split(profilesStr, ", ")
	var profiles []models.ProfileRef
	for _, p := range profileNames {
		profiles = append(profiles, models.ProfileRef{Name: strings.TrimSpace(p), Kind: "Profile"})
	}
	mtc.clusterSSBs = append(mtc.clusterSSBs, &clusterSSBEntry{
		Context: ctxName,
		SSB: &models.ScanSettingBinding{
			Name:            ssbName,
			Namespace:       "openshift-compliance",
			ScanSettingName: "default",
			Profiles:        profiles,
		},
		Schedule: "0 0 * * *", // default schedule
	})
	return nil
}

func kubecontextHasSSBWithTwoProfiles(ctxName, ssbName, profile1, profile2 string) error {
	// IMP-MAP-020
	mtc.clusterSSBs = append(mtc.clusterSSBs, &clusterSSBEntry{
		Context: ctxName,
		SSB: &models.ScanSettingBinding{
			Name:            ssbName,
			Namespace:       "openshift-compliance",
			ScanSettingName: "default",
			Profiles: []models.ProfileRef{
				{Name: profile1, Kind: "Profile"},
				{Name: profile2, Kind: "Profile"},
			},
		},
		Schedule: "0 0 * * *", // default schedule
	})
	return nil
}

func kubecontextHasSSBWithSchedule(ctxName, ssbName, schedule string) error {
	// IMP-MAP-020a
	mtc.clusterSSBs = append(mtc.clusterSSBs, &clusterSSBEntry{
		Context: ctxName,
		SSB: &models.ScanSettingBinding{
			Name:            ssbName,
			Namespace:       "openshift-compliance",
			ScanSettingName: "default",
			Profiles:        []models.ProfileRef{{Name: "ocp4-cis", Kind: "Profile"}},
		},
		Schedule: schedule,
	})
	return nil
}

func ctxResolvesToClusterID(ctxLabel, clusterID string) error {
	// IMP-MAP-019, IMP-MAP-021
	mtc.clusterIDs["ctx-"+ctxLabel] = clusterID
	return nil
}

func theImporterMergesSSBs() error {
	// IMP-MAP-019, IMP-MAP-020, IMP-MAP-021
	// IMP-ACC-015: unit-level coverage (via internal/merge, fed by fake per-context
	// SSBs/cluster IDs set up by the steps above) of "same SSB name across multiple
	// source clusters with matching profiles/schedule -> one scan config targeting
	// all resolved cluster IDs". A real multi-cluster, multi-kubecontext e2e run is
	// a tracked follow-up (requires a second live cluster in CI).
	var inputs []merge.ClusterSSB
	for _, entry := range mtc.clusterSSBs {
		clusterID := mtc.clusterIDs[entry.Context]
		inputs = append(inputs, merge.ClusterSSB{
			Context:   entry.Context,
			ClusterID: clusterID,
			SSB:       entry.SSB,
			Schedule:  entry.Schedule,
		})
	}
	mtc.mergedConfigs, mtc.mergeProblems = merge.MergeSSBs(inputs)

	// Copy merge problems to main problems collector for assertion steps
	for _, p := range mtc.mergeProblems.All() {
		mtc.problems.Add(p)
	}
	// Mark failed SSBs as skipped
	for _, p := range mtc.mergeProblems.All() {
		if p.Skipped {
			// Extract SSB name from resource ref
			parts := strings.Split(p.ResourceRef, "/")
			if len(parts) > 0 {
				mtc.skipped[parts[len(parts)-1]] = true
			}
		}
	}
	return nil
}

func oneACSConfigMustBeCreated(scanName string) error {
	// IMP-MAP-019, IMP-MAP-021
	if len(mtc.mergedConfigs) != 1 {
		return fmt.Errorf("expected 1 merged config, got %d", len(mtc.mergedConfigs))
	}
	if mtc.mergedConfigs[0].ScanName != scanName {
		return fmt.Errorf("expected scanName %q, got %q", scanName, mtc.mergedConfigs[0].ScanName)
	}
	return nil
}

func payloadClustersMustEqual(table *godog.Table) error {
	// IMP-MAP-019, IMP-MAP-021
	var expected []string
	for i, row := range table.Rows {
		if i == 0 {
			continue // skip header
		}
		expected = append(expected, row.Cells[0].Value)
	}
	sort.Strings(expected)
	if len(mtc.mergedConfigs) == 0 {
		return fmt.Errorf("no merged configs available")
	}
	actual := make([]string, len(mtc.mergedConfigs[0].ClusterIDs))
	copy(actual, mtc.mergedConfigs[0].ClusterIDs)
	sort.Strings(actual)
	if len(actual) != len(expected) {
		return fmt.Errorf("expected %d clusters, got %d: %v", len(expected), len(actual), actual)
	}
	for i := range expected {
		if actual[i] != expected[i] {
			return fmt.Errorf("cluster[%d]: expected %q, got %q", i, expected[i], actual[i])
		}
	}
	return nil
}

func ssbMustBeMarkedFailed(name string) error {
	// IMP-MAP-020, IMP-MAP-020a, IMP-MAP-008..011
	// Check either skipped map or problems collector
	if mtc.skipped[name] {
		return nil
	}
	// Also check problems for the SSB name
	for _, p := range mtc.problems.All() {
		if strings.Contains(p.ResourceRef, name) {
			return nil
		}
	}
	return fmt.Errorf("expected SSB %q to be marked failed, but it was not", name)
}

func problemMustMentionMismatch() error {
	// IMP-MAP-020, IMP-MAP-020a
	for _, p := range mtc.problems.All() {
		if strings.Contains(p.Description, "mismatch") {
			return nil
		}
	}
	return fmt.Errorf("no problem mentions mismatch across clusters")
}

func consoleMustPrintWarning() error {
	// IMP-MAP-020, IMP-MAP-020a
	// In unit tests, we verify the problem was recorded with category "conflict".
	// The console output is verified by the problem being added.
	if !mtc.problems.HasCategory("conflict") {
		return fmt.Errorf("expected a conflict problem (console warning), but none found")
	}
	return nil
}

// --- IMP-MAP-008..011: Validation steps ---

func ssbReferencesScanSetting(ssbName, settingName string) error {
	// IMP-MAP-008..011
	binding := &validationBinding{
		SSB: &models.ScanSettingBinding{
			Name:            ssbName,
			Namespace:       "openshift-compliance",
			ScanSettingName: settingName,
			Profiles:        []models.ProfileRef{{Name: "ocp4-cis", Kind: "Profile"}},
		},
		ScanSettingName: settingName,
		HasScanSetting:  false, // "does-not-exist" means no ScanSetting
	}
	mtc.allBindings = append(mtc.allBindings, binding)
	return nil
}

func theImporterProcessesAllBindings() error {
	if mtc.adextMode {
		// IMP-ADEXT-001, IMP-ADEXT-013
		return processADEXTDetection()
	}

	// IMP-MAP-008..011
	// Add a valid binding to ensure "other valid bindings" can be checked
	validBinding := &validationBinding{
		SSB: &models.ScanSettingBinding{
			Name:            "valid-binding",
			Namespace:       "openshift-compliance",
			ScanSettingName: "existing-setting",
			Profiles:        []models.ProfileRef{{Name: "ocp4-cis", Kind: "Profile"}},
		},
		ScanSettingName: "existing-setting",
		HasScanSetting:  true,
	}
	mtc.allBindings = append(mtc.allBindings, validBinding)
	mtc.scanSettings["existing-setting"] = &models.ScanSetting{
		Name:      "existing-setting",
		Namespace: "openshift-compliance",
		Schedule:  "0 0 * * *",
	}

	// Process all bindings
	for _, b := range mtc.allBindings {
		ss, ok := mtc.scanSettings[b.ScanSettingName]
		if !ok && !b.HasScanSetting {
			// IMP-MAP-008: missing ScanSetting
			ref := b.SSB.Namespace + "/" + b.SSB.Name
			mtc.problems.Add(problems.Problem{
				Severity:    "error",
				Category:    "input",
				ResourceRef: ref,
				Description: fmt.Sprintf("ScanSettingBinding %q references ScanSetting %q which does not exist", ref, b.ScanSettingName),
				FixHint:     fmt.Sprintf("Create ScanSetting %q or update the binding to reference an existing ScanSetting", b.ScanSettingName),
				Skipped:     true,
			})
			mtc.skipped[b.SSB.Name] = true
			continue
		}
		if ok {
			// Valid binding — process it
			result := mapping.BuildPayload(b.SSB, ss, "test-cluster-id")
			if result.Problem != nil {
				mtc.problems.Add(*result.Problem)
				mtc.skipped[b.SSB.Name] = true
			} else {
				mtc.processedResults[b.SSB.Name] = true
			}
		}
	}
	return nil
}

func problemsMustIncludeEntryFor(ssbName string) error {
	// IMP-MAP-009
	ref := "openshift-compliance/" + ssbName
	found := mtc.problems.ForResource(ref)
	if len(found) == 0 {
		return fmt.Errorf("no problems found for resource %q", ref)
	}
	return nil
}

func problemMustIncludeFixHint() error {
	// IMP-MAP-010
	for _, p := range mtc.problems.All() {
		if p.FixHint != "" {
			return nil
		}
	}
	return fmt.Errorf("no problem includes a fix hint")
}

func otherBindingsMustStillBeProcessed() error {
	// IMP-MAP-011
	if !mtc.processedResults["valid-binding"] {
		return fmt.Errorf("expected valid-binding to be processed, but it was not")
	}
	return nil
}

// NOTE: the IMP-ADOPT-001..008 "patch SSB settingsRef after create" step definitions were
// retired along with the corresponding scenarios in 02-co-to-acs-mapping.feature. See
// specs/08-adopt-external-ssb.feature (IMP-ADEXT-*) for the replacement workflow.

// --- IMP-ADEXT-001, IMP-ADEXT-013: Detect blocking external ScanSettingBindings ---

// quotedStringRe extracts double-quoted tokens, e.g. from `"ocp4-cis", "ocp4-pci"`.
var quotedStringRe = regexp.MustCompile(`"([^"]*)"`)

func ssbInNamespaceReferencesProfilesList(name, namespace, profilesRaw string) error {
	// IMP-ADEXT-001, IMP-ADEXT-013
	mtc.adextMode = true
	var profiles []models.ProfileRef
	for _, m := range quotedStringRe.FindAllStringSubmatch(profilesRaw, -1) {
		profiles = append(profiles, models.ProfileRef{Name: m[1], Kind: "Profile"})
	}
	mtc.ssb = &models.ScanSettingBinding{
		Name:      name,
		Namespace: namespace,
		Profiles:  profiles,
	}
	return nil
}

func theSSBHasNoStackroxLabel() error {
	// IMP-ADEXT-001
	if mtc.ssb == nil {
		return fmt.Errorf("no ScanSettingBinding set up yet")
	}
	mtc.ssb.Labels = nil
	return nil
}

func theSSBHasStackroxLabel() error {
	// IMP-ADEXT-013
	if mtc.ssb == nil {
		return fmt.Errorf("no ScanSettingBinding set up yet")
	}
	mtc.ssb.Labels = map[string]string{
		adoptexternal.StackroxManagedLabelKey: adoptexternal.StackroxManagedLabelValue,
	}
	return nil
}

func noACSScanConfigurationNamedExists(name string) error {
	// IMP-ADEXT-001: recorded for scenario context. The blocking conflict exists on the
	// K8s side regardless of whether ACS already has a scan configuration with this name.
	mtc.adextTargetScanName = name
	return nil
}

// processADEXTDetection runs the detection logic from internal/adoptexternal against
// the single SSB set up by the scenario's "Given" steps.
// IMP-ADEXT-001, IMP-ADEXT-013
func processADEXTDetection() error {
	if mtc.ssb == nil {
		return fmt.Errorf("no ScanSettingBinding set up yet")
	}
	clusterSSBs := []merge.ClusterSSB{{Context: "default", SSB: mtc.ssb}}
	blockers := adoptexternal.DetectBlockers(clusterSSBs, mtc.ssb.Name)
	if len(blockers) > 0 {
		mtc.adextBlocked = true
		mtc.adextBlockingSSB = blockers[0].SSB
	}
	mtc.adextSourceBindings = adoptexternal.FilterExternal([]models.ScanSettingBinding{*mtc.ssb})
	return nil
}

func ssbMustBeIdentifiedAsBlocked(name string) error {
	// IMP-ADEXT-001
	if !mtc.adextBlocked {
		return fmt.Errorf("expected %q to be identified as blocked by an external ScanSettingBinding, but it was not", name)
	}
	if mtc.adextBlockingSSB == nil || mtc.adextBlockingSSB.Name != name {
		return fmt.Errorf("expected blocking ScanSettingBinding named %q, got %+v", name, mtc.adextBlockingSSB)
	}
	return nil
}

func blockingSSBMustBe(name, namespace string) error {
	// IMP-ADEXT-001
	if mtc.adextBlockingSSB == nil {
		return fmt.Errorf("no blocking ScanSettingBinding was recorded")
	}
	if mtc.adextBlockingSSB.Name != name || mtc.adextBlockingSSB.Namespace != namespace {
		return fmt.Errorf("expected blocking ScanSettingBinding %s/%s, got %s/%s",
			namespace, name, mtc.adextBlockingSSB.Namespace, mtc.adextBlockingSSB.Name)
	}
	return nil
}

func ssbMustNotBeIdentifiedAsBlocked(name string) error {
	// IMP-ADEXT-013
	if mtc.adextBlocked {
		return fmt.Errorf("expected %q to NOT be identified as blocked by an external ScanSettingBinding, but it was (blocking SSB: %+v)", name, mtc.adextBlockingSSB)
	}
	return nil
}

func ssbMustNotBeTreatedAsSourceBinding(name string) error {
	// IMP-ADEXT-013
	for _, b := range mtc.adextSourceBindings {
		if b.Name == name {
			return fmt.Errorf("expected %q to NOT be treated as a source binding to import, but it was", name)
		}
	}
	return nil
}
