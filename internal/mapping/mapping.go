// Package mapping converts CO resources to ACS scan configuration payloads.
package mapping

import (
	"fmt"
	"slices"

	"github.com/stackrox/co-importer/internal/models"
	"github.com/stackrox/co-importer/internal/problems"
)

// MappingResult is returned per ScanSettingBinding.
// Exactly one of Payload or Problem will be set.
type MappingResult struct {
	Payload *models.ACSPayload
	Problem *problems.Problem
}

// BuildPayload converts a ScanSettingBinding and its referenced ScanSetting into
// an ACS create payload.
//
// IMP-MAP-001: scanName = binding.Name; profiles = sorted+deduped list of profile names.
// IMP-MAP-002: missing profile kind defaults to "Profile".
// IMP-MAP-003: oneTimeScan=false when a schedule is present.
// IMP-MAP-004: scanSchedule from ConvertCronToACSSchedule.
// IMP-MAP-005, IMP-MAP-006: description includes source info.
// IMP-MAP-012..015: invalid cron => Problem{category:"mapping", skipped:true}.
func BuildPayload(ssb *models.ScanSettingBinding, ss *models.ScanSetting, clusterID string) MappingResult {
	ref := fmt.Sprintf("%s/%s", ssb.Namespace, ssb.Name)

	// IMP-MAP-004, IMP-MAP-012..015: convert cron schedule.
	schedule, err := ConvertCronToACSSchedule(ss.Schedule)
	if err != nil {
		return MappingResult{
			Problem: &problems.Problem{
				Severity:    "error",
				Category:    "mapping",
				ResourceRef: ref,
				Description: fmt.Sprintf(
					"schedule conversion failed for ScanSettingBinding %q (ScanSetting %q, schedule %q): %v",
					ref, ss.Name, ss.Schedule, err,
				),
				FixHint: fmt.Sprintf(
					"Update ScanSetting %q to use a supported 5-field cron expression, for example: "+
						"\"0 2 * * *\" (daily at 02:00), \"0 2 * * 0\" (weekly on Sunday), "+
						"\"0 2 1 * *\" (monthly on the 1st). "+
						"Step and range notation in the cron expression are not supported.",
					ss.Name,
				),
				Skipped: true,
			},
		}
	}

	// IMP-MAP-001, IMP-MAP-002: collect profiles, dedup, sort.
	profiles := ResolveProfiles(ssb.Profiles)

	// IMP-MAP-005, IMP-MAP-006: build description.
	description := BuildDescription(ssb, ss)

	return MappingResult{
		Payload: &models.ACSPayload{
			ScanName: ssb.Name, // IMP-MAP-001
			ScanConfig: models.ACSBaseScanConfig{
				OneTimeScan:  false,       // IMP-MAP-003
				Profiles:     profiles,    // IMP-MAP-001
				ScanSchedule: schedule,    // IMP-MAP-004
				Description:  description, // IMP-MAP-005, IMP-MAP-006
			},
			Clusters: []string{clusterID},
		},
	}
}

// ResolveProfiles converts profile references to a sorted, deduplicated list of
// profile names. Missing kind defaults to "Profile" (IMP-MAP-002).
func ResolveProfiles(refs []models.ProfileRef) []string {
	seen := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		// IMP-MAP-002: resolve kind (defaulting empty to "Profile")
		_ = r.ResolvedKind()
		seen[r.Name] = struct{}{}
	}
	profiles := make([]string, 0, len(seen))
	for name := range seen {
		profiles = append(profiles, name)
	}
	slices.Sort(profiles) // IMP-MAP-001: deterministic sorted order
	return profiles
}

// BuildDescription creates the description string for an ACS scan config.
// IMP-MAP-005: contains "Imported from CO ScanSettingBinding {namespace}/{name}"
// IMP-MAP-006: includes settings reference context (ScanSetting name).
func BuildDescription(ssb *models.ScanSettingBinding, ss *models.ScanSetting) string {
	desc := fmt.Sprintf("Imported from CO ScanSettingBinding %s/%s", ssb.Namespace, ssb.Name)
	if ss != nil {
		desc += fmt.Sprintf(" (ScanSetting: %s)", ss.Name)
	}
	return desc
}
