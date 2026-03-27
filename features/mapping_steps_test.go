package features

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	"github.com/stackrox/co-importer/internal/mapping"
	"github.com/stackrox/co-importer/internal/models"
	"github.com/stackrox/co-importer/internal/problems"
)

// mappingTestContext shares state between steps within a scenario.
type mappingTestContext struct {
	ssb         *models.ScanSettingBinding
	scanSetting *models.ScanSetting
	profiles    []models.ProfileRef
	payload     *models.ACSPayload
	payloadJSON []byte
	problems    *problems.Collector
	err         error
	skipped     map[string]bool // tracks skipped SSBs
}

var mtc *mappingTestContext

// resetMappingTestContext initialises a fresh context for each scenario.
func resetMappingTestContext() {
	mtc = &mappingTestContext{
		problems: problems.New(),
		skipped:  make(map[string]bool),
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

	// @mapping @adopt — IMP-ADOPT-001..008
	ctx.Step(`^the SSB references ScanSetting "([^"]*)"$`, theSSBReferencesScanSetting)
	ctx.Step(`^the importer successfully creates ACS scan config "([^"]*)"$`, importerCreatesACSConfig)
	ctx.Step(`^ACS creates ScanSetting "([^"]*)" on the cluster$`, acsCreatesScanSettingOnCluster)
	ctx.Step(`^the importer runs the adoption step$`, importerRunsAdoptionStep)
	ctx.Step(`^SSB "([^"]*)" settingsRef\.name MUST be patched to "([^"]*)"$`, ssbSettingsRefMustBePatched)
	ctx.Step(`^the importer MUST log an info message about the adoption$`, importerMustLogAdoptionInfo)
	ctx.Step(`^SSB "([^"]*)" settingsRef\.name MUST NOT be modified$`, ssbSettingsRefMustNotBeModified)
	ctx.Step(`^ACS has NOT yet created ScanSetting "([^"]*)" on the cluster$`, acsHasNotCreatedScanSetting)
	ctx.Step(`^the adoption poll times out$`, adoptionPollTimesOut)
	ctx.Step(`^the importer MUST log a warning$`, importerMustLogWarning)
	ctx.Step(`^the SSB MUST NOT be modified$`, ssbMustNotBeModified)
	ctx.Step(`^the importer MUST NOT exit with an error$`, importerMustNotExitWithError)

	// @mapping @adopt @multicluster
	ctx.Step(`^kubecontext "([^"]*)" has SSB "([^"]*)" referencing ScanSetting "([^"]*)"$`, kubecontextHasSSBReferencingScanSetting)
	ctx.Step(`^the importer creates one ACS scan config "([^"]*)" for both clusters$`, importerCreatesConfigForBothClusters)
	ctx.Step(`^ACS creates ScanSetting "([^"]*)" on both clusters$`, acsCreatesScanSettingOnBothClusters)
	ctx.Step(`^SSB "([^"]*)" on ctx-([a-z]) MUST be patched to reference "([^"]*)"$`, ssbOnCtxMustBePatched)
	ctx.Step(`^SSB "([^"]*)" on ctx-([a-z]) MUST be patched$`, ssbOnCtxMustBePatchedSimple)
	ctx.Step(`^ACS creates ScanSetting "([^"]*)" on ctx-([a-z]) but NOT on ctx-([a-z])$`, acsCreatesScanSettingOnOneCtx)
	ctx.Step(`^the importer MUST warn about ctx-([a-z]) timeout$`, importerMustWarnAboutCtxTimeout)

	// @mapping @schedule @problems — IMP-MAP-012..015
	ctx.Step(`^problem description MUST mention schedule conversion failed$`, problemMustMentionScheduleConversionFailed)
	ctx.Step(`^problem fix hint MUST suggest using a valid cron expression$`, problemFixHintMustSuggestValidCron)
	ctx.Step(`^"([^"]*)" MUST be skipped$`, ssbMustBeSkipped)
}

// --- Background steps (no-op in unit tests) ---

// IMP-MAP-001 (background)
func acsEndpointAndTokenPreflightSucceeded() error { return nil }
func theImporterCanReadCOResources() error          { return nil }

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
		return fmt.Errorf("JSON marshal error: %v", err)
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

// --- Stubs for scenarios not yet implemented ---

func kubecontextPointsToSecuredCluster(_ string) error          { return godog.ErrPending }
func configMapHasDataKey(_, _, _, _ string) error               { return godog.ErrPending }
func resolveACSClusterID(_ string) error                        { return godog.ErrPending }
func resolvedClusterIDMustBe(_ string) error                    { return godog.ErrPending }
func kubecontextPointsToOpenShiftCluster(_ string) error        { return godog.ErrPending }
func configMapNotReadable(_ string) error                       { return godog.ErrPending }
func clusterVersionHasClusterID(_, _ string) error              { return godog.ErrPending }
func acsClusterListByProviderID(_, _ string) error              { return godog.ErrPending }
func kubecontextPointsToCluster(_ string) error                 { return godog.ErrPending }
func clusterVersionNotAvailable() error                         { return godog.ErrPending }
func secretHasDataKey(_, _, _ string) error                     { return godog.ErrPending }
func acsClusterListByName(_, _ string) error                    { return godog.ErrPending }

func configMapNotReadableWithError(_, _ string) error           { return godog.ErrPending }
func clusterVersionNotAvailableWithError(_ string) error        { return godog.ErrPending }
func secretNotReadableWithError(_, _ string) error              { return godog.ErrPending }
func errorMustListEachMethodFailure() error                     { return godog.ErrPending }

func kubecontextHasSSBWithProfilesAndSchedule(_, _, _, _ string) error { return godog.ErrPending }
func ctxResolvesToClusterID(_, _ string) error                  { return godog.ErrPending }
func theImporterMergesSSBs() error                              { return godog.ErrPending }
func oneACSConfigMustBeCreated(_ string) error                  { return godog.ErrPending }
func payloadClustersMustEqual(_ *godog.Table) error             { return godog.ErrPending }

func kubecontextHasSSBWithProfiles(_, _, _ string) error        { return godog.ErrPending }
func kubecontextHasSSBWithTwoProfiles(_, _, _, _ string) error  { return godog.ErrPending }
func kubecontextHasSSBWithSchedule(_, _, _ string) error        { return godog.ErrPending }
func ssbMustBeMarkedFailed(_ string) error                      { return godog.ErrPending }
func problemMustMentionMismatch() error                         { return godog.ErrPending }
func consoleMustPrintWarning() error                            { return godog.ErrPending }

func ssbReferencesScanSetting(_, _ string) error                { return godog.ErrPending }
func theImporterProcessesAllBindings() error                    { return godog.ErrPending }
func problemsMustIncludeEntryFor(_ string) error                { return godog.ErrPending }
func problemMustIncludeFixHint() error                          { return godog.ErrPending }
func otherBindingsMustStillBeProcessed() error                  { return godog.ErrPending }

func theSSBReferencesScanSetting(_ string) error                { return godog.ErrPending }
func importerCreatesACSConfig(_ string) error                   { return godog.ErrPending }
func acsCreatesScanSettingOnCluster(_ string) error             { return godog.ErrPending }
func importerRunsAdoptionStep() error                           { return godog.ErrPending }
func ssbSettingsRefMustBePatched(_, _ string) error             { return godog.ErrPending }
func importerMustLogAdoptionInfo() error                        { return godog.ErrPending }
func ssbSettingsRefMustNotBeModified(_ string) error            { return godog.ErrPending }
func acsHasNotCreatedScanSetting(_ string) error                { return godog.ErrPending }
func adoptionPollTimesOut() error                               { return godog.ErrPending }
func importerMustLogWarning() error                             { return godog.ErrPending }
func ssbMustNotBeModified() error                               { return godog.ErrPending }
func importerMustNotExitWithError() error                       { return godog.ErrPending }

func kubecontextHasSSBReferencingScanSetting(_, _, _ string) error    { return godog.ErrPending }
func importerCreatesConfigForBothClusters(_ string) error             { return godog.ErrPending }
func acsCreatesScanSettingOnBothClusters(_ string) error              { return godog.ErrPending }
func ssbOnCtxMustBePatched(_, _, _ string) error                      { return godog.ErrPending }
func ssbOnCtxMustBePatchedSimple(_, _ string) error                   { return godog.ErrPending }
func acsCreatesScanSettingOnOneCtx(_, _, _ string) error              { return godog.ErrPending }
func importerMustWarnAboutCtxTimeout(_ string) error                  { return godog.ErrPending }
