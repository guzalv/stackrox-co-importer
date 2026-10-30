package status

import (
	"bytes"
	"strings"
	"testing"
)

// TestIMP_CLI_025_PrintWarnings verifies that config.Config.Warnings (e.g.
// the IMP-CLI-025 overlapping-auth-vars notice) are actually printed to
// stderr, not just computed and discarded. Spec: IMP-CLI-025 ("emit a
// warning to stderr").
func TestIMP_CLI_025_PrintWarnings(t *testing.T) {
	var buf bytes.Buffer
	warnings := []string{
		"both ROX_API_TOKEN and ROX_ADMIN_PASSWORD are set; using token auth",
	}

	PrintWarnings(&buf, warnings)

	out := buf.String()
	if !strings.Contains(out, "ROX_ADMIN_PASSWORD") {
		t.Fatalf("expected printed warnings to contain %q, got: %q", "ROX_ADMIN_PASSWORD", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Fatalf("expected printed warnings to use the standard [WARN] prefix, got: %q", out)
	}
}

// TestIMP_CLI_025_PrintWarnings_Empty verifies that an empty warnings slice
// prints nothing.
func TestIMP_CLI_025_PrintWarnings_Empty(t *testing.T) {
	var buf bytes.Buffer
	PrintWarnings(&buf, nil)
	if buf.Len() != 0 {
		t.Fatalf("expected no output for empty warnings, got: %q", buf.String())
	}
}
