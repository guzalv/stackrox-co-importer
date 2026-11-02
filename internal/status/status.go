// Package status provides a simple stderr printer for progress output.
package status

import (
	"fmt"
	"io"
	"os"
)

// Printer outputs progress messages to a writer (stderr by default).
type Printer struct {
	w io.Writer
}

// New creates a new status printer that writes to stderr.
func New() *Printer {
	return &Printer{w: os.Stderr}
}

// NewWithWriter creates a status printer that writes to w. Used by tests to
// capture console output.
func NewWithWriter(w io.Writer) *Printer {
	return &Printer{w: w}
}

// Writer returns the printer's output writer, so callers can print additional
// lines (e.g. the Problems section) to the same destination.
func (p *Printer) Writer() io.Writer {
	return p.w
}

// Stage prints a stage heading with optional detail.
func (p *Printer) Stage(title, detail string) {
	if detail != "" {
		fmt.Fprintf(p.w, "==> %s: %s\n", title, detail)
	} else {
		fmt.Fprintf(p.w, "==> %s\n", title)
	}
}

// OK prints a success message.
func (p *Printer) OK(msg string) {
	fmt.Fprintf(p.w, "  [OK] %s\n", msg)
}

// Warn prints a warning message.
func (p *Printer) Warn(msg string) {
	fmt.Fprintf(p.w, "  [WARN] %s\n", msg)
}

// Fail prints a failure message.
func (p *Printer) Fail(msg string) {
	fmt.Fprintf(p.w, "  [FAIL] %s\n", msg)
}

// PrintWarnings prints each warning (e.g. config.Config.Warnings, such as
// the IMP-CLI-025 overlapping-auth-vars notice) to w using the same
// "[WARN]" format as Printer.Warn. It is a package-level function, rather
// than a Printer method, so callers can print config warnings before a
// Printer (or any other part of the pipeline) exists. IMP-CLI-025
func PrintWarnings(w io.Writer, warnings []string) {
	for _, msg := range warnings {
		fmt.Fprintf(w, "  [WARN] %s\n", msg)
	}
}
