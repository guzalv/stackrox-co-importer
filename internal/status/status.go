// Package status provides a simple stderr printer for progress output.
package status

import (
	"fmt"
	"io"
	"os"
)

// Printer outputs progress messages to stderr.
type Printer struct{}

// New creates a new status printer.
func New() *Printer {
	return &Printer{}
}

// Stage prints a stage heading with optional detail.
func (p *Printer) Stage(title, detail string) {
	if detail != "" {
		fmt.Fprintf(os.Stderr, "==> %s: %s\n", title, detail)
	} else {
		fmt.Fprintf(os.Stderr, "==> %s\n", title)
	}
}

// OK prints a success message.
func (p *Printer) OK(msg string) {
	fmt.Fprintf(os.Stderr, "  [OK] %s\n", msg)
}

// Warn prints a warning message.
func (p *Printer) Warn(msg string) {
	fmt.Fprintf(os.Stderr, "  [WARN] %s\n", msg)
}

// Fail prints a failure message.
func (p *Printer) Fail(msg string) {
	fmt.Fprintf(os.Stderr, "  [FAIL] %s\n", msg)
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
