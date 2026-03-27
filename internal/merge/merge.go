// Package merge handles merging ScanSettingBindings with the same name across
// multiple clusters into a single ACS scan configuration.
// IMP-MAP-019, IMP-MAP-020, IMP-MAP-020a, IMP-MAP-021
package merge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/stackrox/co-importer/internal/mapping"
	"github.com/stackrox/co-importer/internal/models"
	"github.com/stackrox/co-importer/internal/problems"
)

// ClusterSSB represents an SSB from a specific cluster context.
type ClusterSSB struct {
	Context   string
	ClusterID string
	SSB       *models.ScanSettingBinding
	Schedule  string
}

// MergedConfig is the result of merging same-name SSBs across clusters.
type MergedConfig struct {
	ScanName   string
	Profiles   []string
	Schedule   string
	ClusterIDs []string
}

// MergeSSBs groups SSBs by name, validates that same-name SSBs have matching
// profiles and schedules, and merges their cluster IDs.
// IMP-MAP-019: same name + same profiles + same schedule => merge cluster IDs.
// IMP-MAP-020: profile mismatch => conflict problem.
// IMP-MAP-020a: schedule mismatch => conflict problem.
// IMP-MAP-021: merged payload.clusters contains all cluster IDs.
func MergeSSBs(inputs []ClusterSSB) ([]MergedConfig, *problems.Collector) {
	collector := problems.New()

	// Group by SSB name
	groups := make(map[string][]ClusterSSB)
	var order []string
	for _, input := range inputs {
		name := input.SSB.Name
		if _, seen := groups[name]; !seen {
			order = append(order, name)
		}
		groups[name] = append(groups[name], input)
	}

	var results []MergedConfig

	for _, name := range order {
		entries := groups[name]
		if len(entries) == 0 {
			continue
		}

		// Use first entry as reference
		ref := entries[0]
		refProfiles := mapping.ResolveProfiles(ref.SSB.Profiles)
		refSchedule := ref.Schedule

		conflict := false
		var clusterIDs []string

		for _, entry := range entries {
			entryProfiles := mapping.ResolveProfiles(entry.SSB.Profiles)

			// Check profile mismatch (IMP-MAP-020)
			if !profilesEqual(refProfiles, entryProfiles) {
				collector.Add(problems.Problem{
					Severity:    "error",
					Category:    "conflict",
					ResourceRef: name,
					Description: fmt.Sprintf(
						"ScanSettingBinding %q has profile mismatch across clusters: %s has %v, %s has %v",
						name, ref.Context, refProfiles, entry.Context, entryProfiles,
					),
					Skipped: true,
				})
				conflict = true
				break
			}

			// Check schedule mismatch (IMP-MAP-020a)
			if entry.Schedule != refSchedule {
				collector.Add(problems.Problem{
					Severity:    "error",
					Category:    "conflict",
					ResourceRef: name,
					Description: fmt.Sprintf(
						"ScanSettingBinding %q has schedule mismatch across clusters: %s has %q, %s has %q",
						name, ref.Context, refSchedule, entry.Context, entry.Schedule,
					),
					Skipped: true,
				})
				conflict = true
				break
			}

			if entry.ClusterID != "" {
				clusterIDs = append(clusterIDs, entry.ClusterID)
			}
		}

		if conflict {
			continue
		}

		sort.Strings(clusterIDs)
		results = append(results, MergedConfig{
			ScanName:   name,
			Profiles:   refProfiles,
			Schedule:   refSchedule,
			ClusterIDs: clusterIDs,
		})
	}

	return results, collector
}

// profilesEqual compares two sorted profile lists.
func profilesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aSorted := make([]string, len(a))
	bSorted := make([]string, len(b))
	copy(aSorted, a)
	copy(bSorted, b)
	sort.Strings(aSorted)
	sort.Strings(bSorted)
	return strings.Join(aSorted, ",") == strings.Join(bSorted, ",")
}
