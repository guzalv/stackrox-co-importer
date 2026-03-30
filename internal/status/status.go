// Package status provides a simple stderr printer for import progress.
package status

import (
	"fmt"
	"io"
	"os"
)

// Printer writes structured status messages to an output writer.
type Printer struct {
	out io.Writer
}

// New creates a Printer that writes to os.Stderr.
func New() *Printer {
	return &Printer{out: os.Stderr}
}

// NewWithWriter creates a Printer that writes to the given writer.
// Useful for testing.
func NewWithWriter(w io.Writer) *Printer {
	return &Printer{out: w}
}

// Stage prints a stage header with an optional detail string.
func (p *Printer) Stage(title, detail string) {
	if detail != "" {
		fmt.Fprintf(p.out, "=> %s: %s\n", title, detail)
	} else {
		fmt.Fprintf(p.out, "=> %s\n", title)
	}
}

// OK prints a success message.
func (p *Printer) OK(msg string) {
	fmt.Fprintf(p.out, "   OK: %s\n", msg)
}

// Warn prints a warning message.
func (p *Printer) Warn(msg string) {
	fmt.Fprintf(p.out, "   WARN: %s\n", msg)
}

// Fail prints a failure message.
func (p *Printer) Fail(msg string) {
	fmt.Fprintf(p.out, "   FAIL: %s\n", msg)
}
