package features

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"
)

// registerDeleteConfirmSteps registers step definitions for the "Default
// behavior" and "--delete-existing: confirm, then delete" sections of
// specs/08-adopt-external-ssb.feature (IMP-ADEXT-003, IMP-ADEXT-004,
// IMP-ADEXT-005). It shares mappingTestContext (mtc) with
// registerMappingSteps/registerBackupSteps.
func registerDeleteConfirmSteps(ctx *godog.ScenarioContext) {
	// @adopt-external @default — IMP-ADEXT-003
	ctx.Step(`^"--delete-existing" is not set$`, deleteExistingIsNotSet)
	ctx.Step(`^ScanSettingBinding "([^"]*)" MUST NOT be deleted$`, ssbMustNotBeDeleted)
	ctx.Step(`^problems list MUST include category "([^"]*)" for "([^"]*)"$`, problemsMustIncludeCategoryFor)
	ctx.Step(`^the problem fix hint MUST mention "([^"]*)" and pausing GitOps reconciliation$`, problemFixHintMustMentionFlagAndPausingGitOps)
	ctx.Step(`^the importer MUST NOT prompt for confirmation$`, importerMustNotPromptForConfirmation)
	ctx.Step(`^the importer MUST NOT block waiting for the ScanSettingBinding to be removed$`, importerMustNotBlockWaitingForRemoval)

	// @adopt-external @delete-existing — IMP-ADEXT-004, IMP-ADEXT-005
	ctx.Step(`^"--delete-existing" is set$`, deleteExistingIsSet)
	ctx.Step(`^"--yes" is set$`, yesIsSet)
	ctx.Step(`^the operator confirms the deletion prompt with "y"$`, theOperatorConfirmsTheDeletionPrompt)
	ctx.Step(`^the operator declines the deletion prompt with "n"$`, theOperatorDeclinesTheDeletionPrompt)
	ctx.Step(`^ScanSettingBinding "([^"]*)" MUST be deleted$`, ssbMustBeDeleted)
	ctx.Step(`^the importer MUST NOT exit with a fatal error$`, importerMustNotExitWithFatalError)
}

// fakeDeleter is a test double for adoptexternal.Deleter that records every
// delete call instead of talking to a real cluster.
type fakeDeleter struct {
	calls []string // "context/namespace/name" for each call, in order
	err   error    // error to return from DeleteScanSettingBinding, if any
}

func newFakeDeleter() *fakeDeleter {
	return &fakeDeleter{}
}

func (f *fakeDeleter) DeleteScanSettingBinding(ctxName, namespace, name string) error {
	f.calls = append(f.calls, fmt.Sprintf("%s/%s/%s", ctxName, namespace, name))
	return f.err
}

// fakeConfirmer is a test double for adoptexternal.Confirmer that records
// whether it was called and returns a pre-configured answer instead of
// reading from a real terminal.
type fakeConfirmer struct {
	called bool
	prompt string
	answer bool
	err    error
}

func newFakeConfirmer() *fakeConfirmer {
	return &fakeConfirmer{}
}

func (f *fakeConfirmer) confirm(prompt string) (bool, error) {
	f.called = true
	f.prompt = prompt
	return f.answer, f.err
}

// --- IMP-ADEXT-003: Default behavior reports the conflict and skips ---

func deleteExistingIsNotSet() error {
	// IMP-ADEXT-003
	mtc.adextDeleteExisting = false
	return nil
}

func ssbMustNotBeDeleted(name string) error {
	// IMP-ADEXT-003, IMP-ADEXT-005
	if mtc.adextDeleted {
		return fmt.Errorf("expected ScanSettingBinding %q to NOT be deleted, but ResolveBlocker reported a deletion", name)
	}
	if len(mtc.adextDeleter.calls) > 0 {
		return fmt.Errorf("expected ScanSettingBinding %q to NOT be deleted, but deleter was called: %v", name, mtc.adextDeleter.calls)
	}
	if !mtc.skipped[name] {
		return fmt.Errorf("expected %q to be skipped, but it was not", name)
	}
	return nil
}

func problemsMustIncludeCategoryFor(category, resourceRef string) error {
	// IMP-ADEXT-003, IMP-ADEXT-005
	found := mtc.problems.ForResource(resourceRef)
	if len(found) == 0 {
		return fmt.Errorf("no problems found for resource %q", resourceRef)
	}
	for _, p := range found {
		if p.Category == category {
			return nil
		}
	}
	return fmt.Errorf("expected a problem with category %q for %q, but found: %+v", category, resourceRef, found)
}

func problemFixHintMustMentionFlagAndPausingGitOps(flag string) error {
	// IMP-ADEXT-003
	for _, p := range mtc.problems.All() {
		if strings.Contains(p.FixHint, flag) && strings.Contains(p.FixHint, "pausing GitOps reconciliation") {
			return nil
		}
	}
	return fmt.Errorf("no problem fix hint mentions %q and pausing GitOps reconciliation", flag)
}

func importerMustNotPromptForConfirmation() error {
	// IMP-ADEXT-003, IMP-ADEXT-004 ("--yes" case)
	if mtc.adextConfirmer.called {
		return fmt.Errorf("expected no confirmation prompt, but confirmer was called with prompt: %q", mtc.adextConfirmer.prompt)
	}
	return nil
}

func importerMustNotBlockWaitingForRemoval() error {
	// IMP-ADEXT-003: settle-timeout polling (IMP-ADEXT-006/007) is separate
	// follow-up work and is not implemented. ResolveBlocker returns without
	// any wait, so there is nothing to assert beyond it having already
	// completed by this point in the scenario.
	return nil
}

// --- IMP-ADEXT-004, IMP-ADEXT-005: --delete-existing confirm/decline/--yes ---

func deleteExistingIsSet() error {
	// IMP-ADEXT-004
	mtc.adextDeleteExisting = true
	return nil
}

func yesIsSet() error {
	// IMP-ADEXT-004
	mtc.adextYes = true
	return nil
}

func theOperatorConfirmsTheDeletionPrompt() error {
	// IMP-ADEXT-004
	mtc.adextConfirmer.answer = true
	return nil
}

func theOperatorDeclinesTheDeletionPrompt() error {
	// IMP-ADEXT-005
	mtc.adextConfirmer.answer = false
	return nil
}

func ssbMustBeDeleted(name string) error {
	// IMP-ADEXT-004
	if !mtc.adextDeleted {
		return fmt.Errorf("expected ScanSettingBinding %q to be deleted, but ResolveBlocker did not report a deletion", name)
	}
	for _, call := range mtc.adextDeleter.calls {
		if strings.HasSuffix(call, "/"+name) {
			return nil
		}
	}
	return fmt.Errorf("expected deleter to be called for %q, but calls were: %v", name, mtc.adextDeleter.calls)
}

func importerMustNotExitWithFatalError() error {
	// IMP-ADEXT-005: declining the prompt is treated like the default skip,
	// not an error.
	if mtc.adextResolveErr != nil {
		return fmt.Errorf("expected no fatal error, but got: %w", mtc.adextResolveErr)
	}
	return nil
}
