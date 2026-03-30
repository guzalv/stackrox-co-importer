// Package report implements the JSON report output per IMP-CLI-021.
package report

import (
	"encoding/json"
	"fmt"
	"os"
)

// Meta holds run metadata.
type Meta struct {
	DryRun         bool   `json:"dryRun"`
	NamespaceScope string `json:"namespaceScope"`
	Mode           string `json:"mode"` // "create-only" or "create-or-update"
}

// Counts holds aggregate counts for the run.
type Counts struct {
	Discovered int `json:"discovered"`
	Create     int `json:"create"`
	Skip       int `json:"skip"`
	Failed     int `json:"failed"`
}

// ItemSource identifies the CO source resource.
type ItemSource struct {
	Namespace       string `json:"namespace"`
	BindingName     string `json:"bindingName"`
	ScanSettingName string `json:"scanSettingName"`
}

// Item is one entry in the report items list.
type Item struct {
	Source          ItemSource `json:"source"`
	Action          string     `json:"action"` // "create", "skip", "fail", "update"
	Reason          string     `json:"reason"`
	Attempts        int        `json:"attempts"`
	ACSScanConfigID string     `json:"acsScanConfigId,omitempty"`
	Error           string     `json:"error,omitempty"`
}

// Problem is a structured issue entry.
type Problem struct {
	Severity    string `json:"severity"`
	Category    string `json:"category"`
	ResourceRef string `json:"resourceRef"`
	Description string `json:"description"`
	FixHint     string `json:"fixHint"`
	Skipped     bool   `json:"skipped"`
}

// Report is the top-level JSON report structure (IMP-CLI-021).
type Report struct {
	Meta     Meta      `json:"meta"`
	Counts   Counts    `json:"counts"`
	Items    []Item    `json:"items"`
	Problems []Problem `json:"problems"`
}

// WriteJSON writes the report to the given file path as formatted JSON.
func WriteJSON(path string, r Report) error {
	// Ensure nil slices serialize as [] not null
	if r.Items == nil {
		r.Items = []Item{}
	}
	if r.Problems == nil {
		r.Problems = []Problem{}
	}

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling report: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing report to %s: %w", path, err)
	}
	return nil
}
