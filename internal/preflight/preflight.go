// Package preflight provides a thin wrapper around the ACS client's preflight
// probe, formatting errors with actionable hints.
// IMP-CLI-015, IMP-CLI-016, IMP-CLI-016a
package preflight

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/stackrox/co-importer/internal/acs"
)

// Run probes ACS connectivity and authentication. It calls client.Preflight
// and wraps the error with user-facing hints when possible.
func Run(ctx context.Context, client acs.Client) error {
	err := client.Preflight(ctx)
	if err == nil {
		return nil
	}

	// Check for HTTP auth errors.
	var httpErr *acs.HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 401, 403:
			return fmt.Errorf("authentication failed: check ROX_API_TOKEN or ROX_ADMIN_PASSWORD: %w", err)
		default:
			return fmt.Errorf("unexpected error from ACS (HTTP %d): %w", httpErr.StatusCode, err)
		}
	}

	// Check for TLS errors -- IMP-CLI-016a.
	errMsg := err.Error()
	if isTLSError(errMsg) {
		return fmt.Errorf("TLS error connecting to ACS: %w (hint: use --ca-cert-file or --insecure-skip-verify)", err)
	}

	return fmt.Errorf("unexpected error: %w", err)
}

// isTLSError returns true if the error message suggests a TLS/certificate problem.
func isTLSError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "x509") ||
		strings.Contains(lower, "certificate") ||
		strings.Contains(lower, "tls")
}
