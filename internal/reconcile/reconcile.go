// Package reconcile implements the create/skip/update decision logic for ACS scan configurations.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/stackrox/co-importer/internal/acs"
	"github.com/stackrox/co-importer/internal/models"
)

// Options controls reconciler behavior.
type Options struct {
	DryRun            bool
	OverwriteExisting bool
	MaxRetries        int
}

// ActionResult describes the outcome of a reconcile operation.
type ActionResult struct {
	Action          string // "create", "skip", "update", "fail"
	Reason          string
	Attempts        int
	AcsScanConfigID string
	Error           error
}

// Reconciler decides whether to create, skip, or update an ACS scan configuration.
type Reconciler struct {
	Client  acs.Client
	Options Options
}

// isTransient returns true if the HTTP status code indicates a transient error
// that should be retried (429, 502, 503, 504). // IMP-ERR-001
func isTransient(statusCode int) bool {
	switch statusCode {
	case 429, 502, 503, 504:
		return true
	default:
		return false
	}
}

// Reconcile decides and executes the appropriate action for the given payload.
// IMP-IDEM-001, IMP-IDEM-002, IMP-IDEM-003, IMP-IDEM-004, IMP-IDEM-005,
// IMP-IDEM-006, IMP-IDEM-007, IMP-IDEM-008, IMP-IDEM-009
func (r *Reconciler) Reconcile(ctx context.Context, payload models.ACSPayload) ActionResult {
	// List existing scan configs to check if scanName already exists.
	existing, err := r.Client.ListScanConfigs(ctx)
	if err != nil {
		return ActionResult{
			Action:   "fail",
			Reason:   fmt.Sprintf("failed to list scan configs: %v", err),
			Attempts: 1,
			Error:    err,
		}
	}

	var found *acs.ScanConfig
	for i, sc := range existing {
		if sc.ScanName == payload.ScanName {
			found = &existing[i]
			break
		}
	}

	if found != nil && !r.Options.OverwriteExisting {
		// IMP-IDEM-002, IMP-IDEM-003: skip when exists and overwrite is false
		return ActionResult{
			Action:          "skip",
			Reason:          fmt.Sprintf("scan configuration %q already exists", payload.ScanName),
			Attempts:        0,
			AcsScanConfigID: found.ID,
		}
	}

	if found != nil && r.Options.OverwriteExisting {
		// IMP-IDEM-008: update when exists and overwrite is true
		if r.Options.DryRun {
			return ActionResult{
				Action:          "update",
				Reason:          fmt.Sprintf("would update scan configuration %q (dry-run)", payload.ScanName),
				AcsScanConfigID: found.ID,
			}
		}
		result := r.executeWithRetry(ctx, func() (string, error) {
			return found.ID, r.Client.UpdateScanConfig(ctx, found.ID, payload)
		})
		if result.Error == nil {
			result.Action = "update"
			result.AcsScanConfigID = found.ID
		}
		return result
	}

	// IMP-IDEM-001, IMP-IDEM-009: create when not exists
	if r.Options.DryRun {
		// IMP-IDEM-004..007: dry-run does not write
		return ActionResult{
			Action: "create",
			Reason: fmt.Sprintf("would create scan configuration %q (dry-run)", payload.ScanName),
		}
	}

	result := r.executeWithRetry(ctx, func() (string, error) {
		return r.Client.CreateScanConfig(ctx, payload)
	})
	if result.Error == nil {
		result.Action = "create"
	}
	return result
}

// executeWithRetry runs the given operation with retry for transient errors.
// IMP-ERR-001, IMP-ERR-002, IMP-ERR-004
func (r *Reconciler) executeWithRetry(ctx context.Context, op func() (string, error)) ActionResult {
	maxAttempts := r.Options.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		id, err := op()
		if err == nil {
			return ActionResult{
				Action:          "create",
				Attempts:        attempt,
				AcsScanConfigID: id,
			}
		}

		lastErr = err

		// Check if the error is an HTTPError and whether it's transient.
		var httpErr *acs.HTTPError
		if errors.As(err, &httpErr) {
			if !isTransient(httpErr.StatusCode) {
				// IMP-ERR-002, IMP-ERR-004: non-transient, don't retry
				return ActionResult{
					Action:   "fail",
					Reason:   fmt.Sprintf("non-transient error (HTTP %d): %s", httpErr.StatusCode, httpErr.Message),
					Attempts: attempt,
					Error:    err,
				}
			}
		}

		// Transient error — backoff before retry (skip sleep on last attempt).
		if attempt < maxAttempts {
			backoff := time.Duration(attempt) * 100 * time.Millisecond
			select {
			case <-ctx.Done():
				return ActionResult{
					Action:   "fail",
					Reason:   "context cancelled during retry",
					Attempts: attempt,
					Error:    ctx.Err(),
				}
			case <-time.After(backoff):
			}
		}
	}

	return ActionResult{
		Action:   "fail",
		Reason:   fmt.Sprintf("exhausted %d retries: %v", maxAttempts, lastErr),
		Attempts: maxAttempts,
		Error:    lastErr,
	}
}

// ExitCode returns the process exit code for a given outcome string.
// IMP-ERR-003
func ExitCode(outcome string) int {
	switch outcome {
	case "all successful":
		return 0
	case "fatal preflight failure":
		return 1
	case "partial binding failures":
		return 2
	default:
		return 1
	}
}
