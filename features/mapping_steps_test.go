package features

import (
	"github.com/cucumber/godog"
)

// registerMappingSteps registers step definitions for specs/02-co-to-acs-mapping.feature.
// All steps start as pending — implement them alongside production code.
func registerMappingSteps(ctx *godog.ScenarioContext) {
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

// --- Step definition stubs (all return godog.ErrPending) ---

func acsEndpointAndTokenPreflightSucceeded() error     { return godog.ErrPending }
func theImporterCanReadCOResources() error              { return godog.ErrPending }
func aScanSettingBindingInNamespace(_, _ string) error   { return godog.ErrPending }
func theBindingReferencesScanSetting(_ string) error     { return godog.ErrPending }
func theBindingReferencesProfiles(_ *godog.Table) error  { return godog.ErrPending }
func theImporterBuildsTheACSPayload() error              { return godog.ErrPending }
func payloadScanNameMustEqual(_ string) error            { return godog.ErrPending }
func payloadProfilesMustEqual(_ *godog.Table) error      { return godog.ErrPending }

func aProfileReferenceWithNoKind(_ string) error         { return godog.ErrPending }
func theImporterResolvesProfileReferences() error        { return godog.ErrPending }
func profileReferenceKindMustBe(_ string) error          { return godog.ErrPending }
func acsProfileNameListMustInclude(_ string) error       { return godog.ErrPending }

func scanSettingHasSchedule(_, _ string) error           { return godog.ErrPending }
func scanSettingBindingReferences(_, _ string) error     { return godog.ErrPending }
func theImporterMapsScheduleFields() error               { return godog.ErrPending }
func payloadOneTimeScanMustBeFalse() error               { return godog.ErrPending }
func payloadScanScheduleMustBePresent() error            { return godog.ErrPending }

func theImporterBuildsPayloadJSON() error                       { return godog.ErrPending }
func jsonScheduleFieldsMustMatch() error                        { return godog.ErrPending }
func jsonIntervalTypeMustBe(_ string) error                     { return godog.ErrPending }
func weeklyDaysOfWeekMustBePresent() error                      { return godog.ErrPending }
func monthlyDaysOfMonthMustBePresent() error                    { return godog.ErrPending }
func payloadFieldNamesMustMatchProto() error                    { return godog.ErrPending }

func theImporterBuildsPayloadDescription() error                { return godog.ErrPending }
func payloadDescriptionMustContain(_ string) error              { return godog.ErrPending }
func payloadDescriptionShouldIncludeContext() error             { return godog.ErrPending }

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
func problemsMustIncludeCategory(_ string) error                { return godog.ErrPending }
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

func problemMustMentionScheduleConversionFailed() error { return godog.ErrPending }
func problemFixHintMustSuggestValidCron() error         { return godog.ErrPending }
func ssbMustBeSkipped(_ string) error                   { return godog.ErrPending }
