package features

import (
	"github.com/cucumber/godog"
)

// registerIdempotencySteps registers step definitions for specs/03-idempotency-dry-run-retries.feature.
// All steps start as pending — implement them alongside production code.
func registerIdempotencySteps(ctx *godog.ScenarioContext) {
	// Background (shared with mapping — only register if not already done)
	// acsEndpointAndTokenPreflightSucceeded — registered in mapping_steps
	ctx.Step(`^desired payload for source "([^"]*)" is computed$`, desiredPayloadIsComputed)

	// @idempotency — IMP-IDEM-001
	ctx.Step(`^ACS has no scan configuration with scanName "([^"]*)"$`, acsHasNoScanConfig)
	ctx.Step(`^importer executes in apply mode$`, importerExecutesInApplyMode)
	ctx.Step(`^importer MUST send POST to create scan configuration$`, importerMustSendPOST)
	ctx.Step(`^action MUST be "([^"]*)"$`, actionMustBe)

	// @idempotency — IMP-IDEM-002, IMP-IDEM-003
	ctx.Step(`^ACS has scan configuration with scanName "([^"]*)"$`, acsHasScanConfig)
	ctx.Step(`^--overwrite-existing is false$`, overwriteExistingIsFalse)
	ctx.Step(`^importer MUST NOT send PUT$`, importerMustNotSendPUT)
	ctx.Step(`^reason MUST include "([^"]*)"$`, reasonMustInclude)
	ctx.Step(`^problems list MUST include conflict category$`, problemsMustIncludeConflictCategory)

	// @idempotency @overwrite — IMP-IDEM-008, IMP-IDEM-009
	ctx.Step(`^ACS has scan configuration with scanName "([^"]*)" and id "([^"]*)"$`, acsHasScanConfigWithID)
	ctx.Step(`^--overwrite-existing is true$`, overwriteExistingIsTrue)
	ctx.Step(`^importer MUST send PUT to update scan configuration "([^"]*)"$`, importerMustSendPUT)

	// @dryrun — IMP-IDEM-004..007
	ctx.Step(`^importer is started with --dry-run$`, importerStartedWithDryRun)
	ctx.Step(`^at least one action would be create in apply mode$`, atLeastOneActionWouldBeCreate)
	ctx.Step(`^importer completes$`, importerCompletes)
	ctx.Step(`^importer MUST NOT send POST$`, importerMustNotSendPOST)
	ctx.Step(`^planned actions MUST be included in report$`, plannedActionsMustBeInReport)
	ctx.Step(`^problems list MUST still be populated for problematic resources$`, problemsMustBePopulated)

	// @retry — IMP-ERR-001, IMP-ERR-002, IMP-ERR-004
	ctx.Step(`^an ACS create operation returns HTTP (\d+) for first (\d+) attempts$`, acsReturnsHTTPForFirstAttempts)
	ctx.Step(`^the (\d+)(?:st|nd|rd|th) attempt succeeds$`, attemptSucceeds)
	ctx.Step(`^operation MUST be retried with backoff$`, operationMustBeRetriedWithBackoff)
	ctx.Step(`^total attempts MUST be (\d+)$`, totalAttemptsMustBe)
	ctx.Step(`^an ACS create operation returns HTTP (\d+)$`, acsReturnsHTTP)
	ctx.Step(`^operation MUST NOT be retried$`, operationMustNotBeRetried)
	ctx.Step(`^the item MUST be skipped and recorded as a problem$`, itemMustBeSkippedAndRecorded)

	// @exitcodes — IMP-ERR-003
	ctx.Step(`^importer run ends with outcome "([^"]*)"$`, importerRunEndsWithOutcome)
	ctx.Step(`^process exit code MUST be (\d+)$`, processExitCodeMustBe)
}

// --- Step definition stubs (all return godog.ErrPending) ---

func desiredPayloadIsComputed(_ string) error            { return godog.ErrPending }
func acsHasNoScanConfig(_ string) error                  { return godog.ErrPending }
func importerExecutesInApplyMode() error                 { return godog.ErrPending }
func importerMustSendPOST() error                        { return godog.ErrPending }
func actionMustBe(_ string) error                        { return godog.ErrPending }

func acsHasScanConfig(_ string) error                    { return godog.ErrPending }
func overwriteExistingIsFalse() error                    { return godog.ErrPending }
func importerMustNotSendPUT() error                      { return godog.ErrPending }
func reasonMustInclude(_ string) error                   { return godog.ErrPending }
func problemsMustIncludeConflictCategory() error         { return godog.ErrPending }

func acsHasScanConfigWithID(_, _ string) error           { return godog.ErrPending }
func overwriteExistingIsTrue() error                     { return godog.ErrPending }
func importerMustSendPUT(_ string) error                 { return godog.ErrPending }

func importerStartedWithDryRun() error                   { return godog.ErrPending }
func atLeastOneActionWouldBeCreate() error               { return godog.ErrPending }
func importerCompletes() error                           { return godog.ErrPending }
func importerMustNotSendPOST() error                     { return godog.ErrPending }
func plannedActionsMustBeInReport() error                { return godog.ErrPending }
func problemsMustBePopulated() error                     { return godog.ErrPending }

func acsReturnsHTTPForFirstAttempts(_, _ int) error      { return godog.ErrPending }
func attemptSucceeds(_ int) error                        { return godog.ErrPending }
func operationMustBeRetriedWithBackoff() error           { return godog.ErrPending }
func totalAttemptsMustBe(_ int) error                    { return godog.ErrPending }
func acsReturnsHTTP(_ int) error                         { return godog.ErrPending }
func operationMustNotBeRetried() error                   { return godog.ErrPending }
func itemMustBeSkippedAndRecorded() error                { return godog.ErrPending }

func importerRunEndsWithOutcome(_ string) error          { return godog.ErrPending }
func processExitCodeMustBe(_ int) error                  { return godog.ErrPending }
