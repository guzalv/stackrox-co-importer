// Package report assembles the final Report from accumulated run items and
// writes it to disk as indented JSON when --report-json is set.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/stackrox/co-importer/internal/problems"
)

// Report is the top-level structure written to --report-json.
type Report struct {
	Meta     ReportMeta      `json:"meta"`
	Counts   ReportCounts    `json:"counts"`
	Items    []ReportItem    `json:"items"`
	Problems []ReportProblem `json:"problems"`
}

// ReportMeta is metadata written at the top of the JSON report.
type ReportMeta struct {
	Timestamp      string `json:"timestamp"`
	DryRun         bool   `json:"dryRun"`
	NamespaceScope string `json:"namespaceScope"`
	Mode           string `json:"mode"` // "create-only" or "create-or-update"
}

// ReportCounts summarises action totals for the JSON report.
type ReportCounts struct {
	Discovered int `json:"discovered"`
	Create     int `json:"create"`
	Skip       int `json:"skip"`
	Failed     int `json:"failed"`
}

// ReportItem records the outcome for one ScanSettingBinding.
type ReportItem struct {
	Source          ReportSource `json:"source"`
	Action         string       `json:"action"` // create|skip|fail
	Reason         string       `json:"reason,omitempty"`
	Attempts       int          `json:"attempts,omitempty"`
	AcsScanConfigID string     `json:"acsScanConfigId,omitempty"`
	Error          string       `json:"error,omitempty"`
}

// ReportSource identifies the CO source for one report item.
type ReportSource struct {
	Namespace       string `json:"namespace"`
	BindingName     string `json:"bindingName"`
	ScanSettingName string `json:"scanSettingName"`
}

// ReportProblem is a structured issue entry in the report.
type ReportProblem struct {
	Severity    string `json:"severity"`
	Category    string `json:"category"`
	ResourceRef string `json:"resourceRef"`
	Description string `json:"description"`
	FixHint     string `json:"fixHint"`
	Skipped     bool   `json:"skipped"`
}

// Builder accumulates per-binding ReportItems during a run and produces the
// final Report once all bindings have been processed.
type Builder struct {
	dryRun    bool
	overwrite bool
	namespace string
	items     []ReportItem
}

// NewBuilder returns a Builder configured with the given settings.
func NewBuilder(dryRun, overwrite bool, namespace string) *Builder {
	return &Builder{
		dryRun:    dryRun,
		overwrite: overwrite,
		namespace: namespace,
	}
}

// RecordItem appends a single binding outcome to the builder.
func (b *Builder) RecordItem(item ReportItem) {
	b.items = append(b.items, item)
}

// Build constructs the final Report from all recorded items and the supplied
// problems list.
func (b *Builder) Build(probs []problems.Problem) Report {
	mode := "create-only"
	if b.overwrite {
		mode = "create-or-update"
	}

	meta := ReportMeta{
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		DryRun:         b.dryRun,
		NamespaceScope: b.namespace,
		Mode:           mode,
	}

	counts := ReportCounts{
		Discovered: len(b.items),
	}
	for _, it := range b.items {
		switch it.Action {
		case "create":
			counts.Create++
		case "skip":
			counts.Skip++
		case "fail":
			counts.Failed++
		}
	}

	items := b.items
	if items == nil {
		items = []ReportItem{}
	}

	reportProblems := convertProblems(probs)

	return Report{
		Meta:     meta,
		Counts:   counts,
		Items:    items,
		Problems: reportProblems,
	}
}

// WriteJSON writes report as indented JSON to path.
// Returns an error if the file cannot be created or written.
func WriteJSON(path string, rpt Report) error {
	data, err := json.MarshalIndent(rpt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report to JSON: %w", err)
	}
	// Append a trailing newline for POSIX text-file compliance.
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write report JSON to %q: %w", path, err)
	}
	return nil
}

// convertProblems converts problems.Problem entries to ReportProblem entries.
func convertProblems(probs []problems.Problem) []ReportProblem {
	if probs == nil {
		return []ReportProblem{}
	}
	result := make([]ReportProblem, len(probs))
	for i, p := range probs {
		result[i] = ReportProblem{
			Severity:    p.Severity,
			Category:    p.Category,
			ResourceRef: p.ResourceRef,
			Description: p.Description,
			FixHint:     p.FixHint,
			Skipped:     p.Skipped,
		}
	}
	return result
}
