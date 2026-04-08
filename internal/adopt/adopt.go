// Package adopt handles the adoption workflow: after creating an ACS scan config,
// poll for the ACS-created ScanSetting on the cluster and patch the SSB settingsRef.
// IMP-ADOPT-001, IMP-ADOPT-002, IMP-ADOPT-003, IMP-ADOPT-004, IMP-ADOPT-005,
// IMP-ADOPT-006, IMP-ADOPT-007, IMP-ADOPT-008
package adopt

import (
	"fmt"
	"time"
)

// K8sClient abstracts Kubernetes operations needed for adoption.
type K8sClient interface {
	// ScanSettingExists checks if a ScanSetting exists on the given cluster context.
	ScanSettingExists(ctxName, namespace, name string) (bool, error)
	// PatchSSBSettingsRef patches the SSB's settingsRef.name to the new value.
	PatchSSBSettingsRef(ctxName, namespace, ssbName, newSettingsRef string) error
}

// AdoptionRequest describes one SSB that should be adopted on a specific cluster.
type AdoptionRequest struct {
	Context        string // kubecontext name
	Namespace      string
	SSBName        string
	CurrentSetting string // current settingsRef.name
	TargetSetting  string // desired settingsRef.name (= ACS scan config name)
}

// AdoptionResult collects the outcome of the adoption step.
type AdoptionResult struct {
	InfoLogs []string
	Warnings []string
	Err      error // nil = no fatal error (partial success is OK)
}

// DefaultPollTimeout is the minimum time the adoption step polls for the
// ACS-created ScanSetting before giving up (IMP-ADOPT-004..006).
const DefaultPollTimeout = 2 * time.Second

// pollInterval is the delay between ScanSettingExists checks during polling.
const pollInterval = 200 * time.Millisecond

// RunAdoption processes adoption requests independently per cluster.
// IMP-ADOPT-003: skip if current == target.
// IMP-ADOPT-001/002: patch if ScanSetting exists.
// IMP-ADOPT-004..006: poll for at least 2s, then warn on timeout, don't error.
// IMP-ADOPT-007: patch independently per cluster.
// IMP-ADOPT-008: partial success is OK.
func RunAdoption(k8s K8sClient, requests []AdoptionRequest, pollTimeout time.Duration) AdoptionResult {
	var result AdoptionResult

	for _, req := range requests {
		// IMP-ADOPT-003: skip if already referencing the correct ScanSetting
		if req.CurrentSetting == req.TargetSetting {
			result.InfoLogs = append(result.InfoLogs,
				fmt.Sprintf("SSB %s/%s already references ScanSetting %q, skipping adoption",
					req.Namespace, req.SSBName, req.TargetSetting))
			continue
		}

		// IMP-ADOPT-004..006: poll for the ACS-created ScanSetting until timeout
		exists, err := pollForScanSetting(k8s, req, pollTimeout)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("error checking ScanSetting %q on %s: %v", req.TargetSetting, req.Context, err))
			continue
		}

		if !exists {
			// IMP-ADOPT-004..006: timeout waiting for ScanSetting
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("timeout waiting for ACS-created ScanSetting %q on %s; SSB %s/%s not modified",
					req.TargetSetting, req.Context, req.Namespace, req.SSBName))
			continue
		}

		// IMP-ADOPT-001/002: patch the SSB
		if err := k8s.PatchSSBSettingsRef(req.Context, req.Namespace, req.SSBName, req.TargetSetting); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("failed to patch SSB %s/%s on %s: %v", req.Namespace, req.SSBName, req.Context, err))
			continue
		}

		result.InfoLogs = append(result.InfoLogs,
			fmt.Sprintf("adopted SSB %s/%s on %s: settingsRef updated from %q to %q",
				req.Namespace, req.SSBName, req.Context, req.CurrentSetting, req.TargetSetting))
	}

	// IMP-ADOPT-006/008: never set fatal error for timeouts/partial failures
	return result
}

// pollForScanSetting checks for a ScanSetting, retrying until pollTimeout expires.
// A zero or negative pollTimeout means a single immediate check (no retries).
func pollForScanSetting(k8s K8sClient, req AdoptionRequest, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		exists, err := k8s.ScanSettingExists(req.Context, req.Namespace, req.TargetSetting)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		time.Sleep(pollInterval)
	}
}
