package features

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	"github.com/stackrox/co-importer/internal/filter"
	"github.com/stackrox/co-importer/internal/listssbs"
	"github.com/stackrox/co-importer/internal/models"
)

// excludeTestCtx holds per-scenario state for exclude/list-ssbs scenarios.
type excludeTestCtx struct {
	ssbs      []models.ScanSettingBinding
	filtered  []models.ScanSettingBinding
	listOut   bytes.Buffer
}

var etc *excludeTestCtx

func resetExcludeTestCtx() {
	etc = &excludeTestCtx{}
}

// ─── Given steps ─────────────────────────────────────────────────────────────

func ssbsExistInNamespace(names, namespace string) {
	etc.ssbs = nil
	for _, name := range strings.Split(names, ", ") {
		etc.ssbs = append(etc.ssbs, models.ScanSettingBinding{
			Name:      name,
			Namespace: namespace,
		})
	}
}

func noSSBsExist() {
	etc.ssbs = nil
}

// ─── When steps ──────────────────────────────────────────────────────────────

// IMP-CLI-028
func excludePatternIsApplied(pattern string) {
	etc.filtered = filter.ExcludeSSBs(etc.ssbs, []string{pattern})
}

// IMP-CLI-028
func excludePatternsAreApplied(patterns string) {
	etc.filtered = filter.ExcludeSSBs(etc.ssbs, strings.Split(patterns, ","))
}

// IMP-CLI-028
func noExcludePatternsAreApplied() {
	etc.filtered = filter.ExcludeSSBs(etc.ssbs, nil)
}

// IMP-CLI-029
func ssbListOutputRequestedNoExclude() {
	filtered := filter.ExcludeSSBs(etc.ssbs, nil)
	etc.listOut.Reset()
	listssbs.Print(filtered, &etc.listOut)
}

// IMP-CLI-029
func ssbListOutputRequestedWithExclude(pattern string) {
	filtered := filter.ExcludeSSBs(etc.ssbs, []string{pattern})
	etc.listOut.Reset()
	listssbs.Print(filtered, &etc.listOut)
}

// ─── Then steps ──────────────────────────────────────────────────────────────

// IMP-CLI-028
func remainingSSBsAre(want string) error {
	wantNames := strings.Split(want, ",")
	if len(etc.filtered) != len(wantNames) {
		got := make([]string, len(etc.filtered))
		for i, s := range etc.filtered {
			got[i] = s.Name
		}
		return fmt.Errorf("remaining SSBs: got %v, want %v", got, wantNames)
	}
	for i, ssb := range etc.filtered {
		if ssb.Name != wantNames[i] {
			return fmt.Errorf("remaining SSB[%d]: got %q, want %q", i, ssb.Name, wantNames[i])
		}
	}
	return nil
}

// IMP-CLI-029
func outputLinesAre(want string) error {
	wantLines := strings.Split(want, ",")
	gotLines := strings.Split(strings.TrimRight(etc.listOut.String(), "\n"), "\n")
	if len(gotLines) != len(wantLines) {
		return fmt.Errorf("output lines: got %v, want %v", gotLines, wantLines)
	}
	for i, l := range wantLines {
		if gotLines[i] != l {
			return fmt.Errorf("output line[%d]: got %q, want %q", i, gotLines[i], l)
		}
	}
	return nil
}

// IMP-CLI-029
func outputIsEmpty() error {
	if etc.listOut.Len() != 0 {
		return fmt.Errorf("expected empty output, got: %q", etc.listOut.String())
	}
	return nil
}

// ─── Registration ─────────────────────────────────────────────────────────────

func registerExcludeSteps(ctx *godog.ScenarioContext) {
	ctx.Before(func(goCtx context.Context, sc *godog.Scenario) (context.Context, error) {
		resetExcludeTestCtx()
		return goCtx, nil
	})

	ctx.Step(`^SSBs "([^"]*)" exist in namespace "([^"]*)"$`, ssbsExistInNamespace)
	ctx.Step(`^no SSBs exist$`, noSSBsExist)
	ctx.Step(`^exclude pattern "([^"]*)" is applied$`, excludePatternIsApplied)
	ctx.Step(`^exclude patterns "([^"]*)" are applied$`, excludePatternsAreApplied)
	ctx.Step(`^no exclude patterns are applied$`, noExcludePatternsAreApplied)
	ctx.Step(`^SSB list output is requested with no exclude patterns$`, ssbListOutputRequestedNoExclude)
	ctx.Step(`^SSB list output is requested with exclude pattern "([^"]*)"$`, ssbListOutputRequestedWithExclude)
	ctx.Step(`^the remaining SSBs are "([^"]*)"$`, remainingSSBsAre)
	ctx.Step(`^the output lines are "([^"]*)"$`, outputLinesAre)
	ctx.Step(`^the output is empty$`, outputIsEmpty)
}
