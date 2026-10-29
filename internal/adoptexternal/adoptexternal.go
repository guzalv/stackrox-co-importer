// Package adoptexternal detects ScanSettingBindings that are managed outside ACS
// (i.e. that lack the "app.kubernetes.io/name: stackrox" label) and that would
// block ACS from creating or updating a scan configuration with the same name,
// and backs up those resources (and a resumable plan) before anything is
// deleted.
//
// RHACS >=4.11/5.0 Central rejects creating or updating a scan configuration
// whose profiles overlap an existing ScanSettingBinding that is not labeled
// "app.kubernetes.io/name: stackrox" (an "external" SSB). This package
// implements detection of those blockers, the always-before-any-removal
// backup step, the report-and-skip/delete-with-confirmation decision for a
// blocked scanName, and the single-cluster settle-timeout guard against
// GitOps self-heal; the multi-cluster all-clear gate, resume, and CLI wiring
// are separate follow-up work.
//
// IMP-ADEXT-001, IMP-ADEXT-002, IMP-ADEXT-003, IMP-ADEXT-004, IMP-ADEXT-005,
// IMP-ADEXT-006, IMP-ADEXT-007, IMP-ADEXT-008, IMP-ADEXT-009, IMP-ADEXT-010,
// IMP-ADEXT-011, IMP-ADEXT-012, IMP-ADEXT-013, IMP-ADEXT-014, IMP-ADEXT-015
package adoptexternal

import (
	"github.com/stackrox/co-importer/internal/merge"
	"github.com/stackrox/co-importer/internal/models"
)

// StackroxManagedLabelKey and StackroxManagedLabelValue identify a
// ScanSettingBinding as managed by ACS. The exact string match mirrors the
// canonical check in stackrox/stackrox
// central/complianceoperator/v2/compliancemanager/manager_impl.go.
const (
	StackroxManagedLabelKey   = "app.kubernetes.io/name"
	StackroxManagedLabelValue = "stackrox"
)

// IsExternal reports whether ssb is NOT managed by ACS, i.e. it lacks the
// app.kubernetes.io/name=stackrox label. External SSBs are candidate blockers
// for ACS scan configuration creates/updates and must never be treated as
// ACS-owned.
// IMP-ADEXT-001, IMP-ADEXT-013
func IsExternal(ssb *models.ScanSettingBinding) bool {
	if ssb == nil {
		return false
	}
	return ssb.Labels[StackroxManagedLabelKey] != StackroxManagedLabelValue
}

// FilterExternal keeps only the SSBs that are not stackrox-managed. Stackrox-managed
// SSBs are excluded from source discovery entirely: they are never treated as a
// binding to import.
// IMP-ADEXT-013
func FilterExternal(ssbs []models.ScanSettingBinding) []models.ScanSettingBinding {
	result := make([]models.ScanSettingBinding, 0, len(ssbs))
	for _, ssb := range ssbs {
		ssb := ssb
		if IsExternal(&ssb) {
			result = append(result, ssb)
		}
	}
	return result
}

// DetectBlockers returns the per-cluster SSBs named targetScanName that are
// external and therefore block ACS from creating or updating a scan
// configuration named targetScanName.
// IMP-ADEXT-001
func DetectBlockers(clusterSSBs []merge.ClusterSSB, targetScanName string) []merge.ClusterSSB {
	var blockers []merge.ClusterSSB
	for _, c := range clusterSSBs {
		if c.SSB == nil || c.SSB.Name != targetScanName {
			continue
		}
		if IsExternal(c.SSB) {
			blockers = append(blockers, c)
		}
	}
	return blockers
}
