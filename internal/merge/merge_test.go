package merge

import (
	"testing"

	"github.com/stackrox/co-importer/internal/models"
)

func ssb(name string, profiles ...string) *models.ScanSettingBinding {
	refs := make([]models.ProfileRef, len(profiles))
	for i, p := range profiles {
		refs[i] = models.ProfileRef{Name: p}
	}
	return &models.ScanSettingBinding{Name: name, Namespace: "openshift-compliance", Profiles: refs}
}

// IMP-MAP-019
func TestMergeSSBs_SingleCluster(t *testing.T) {
	inputs := []ClusterSSB{
		{Context: "ctx-a", ClusterID: "cluster-a", SSB: ssb("cis-weekly", "ocp4-cis"), Schedule: "0 2 * * *"},
	}

	results, collector := MergeSSBs(inputs)

	if len(collector.All()) != 0 {
		t.Errorf("expected no problems, got: %v", collector.All())
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.ScanName != "cis-weekly" {
		t.Errorf("ScanName: got %q, want %q", r.ScanName, "cis-weekly")
	}
	if len(r.ClusterIDs) != 1 || r.ClusterIDs[0] != "cluster-a" {
		t.Errorf("ClusterIDs: got %v, want [cluster-a]", r.ClusterIDs)
	}
}

// IMP-MAP-019, IMP-MAP-021
func TestMergeSSBs_TwoClustersCompatible(t *testing.T) {
	inputs := []ClusterSSB{
		{Context: "ctx-a", ClusterID: "cluster-a", SSB: ssb("cis-weekly", "ocp4-cis", "ocp4-cis-node"), Schedule: "0 2 * * *"},
		{Context: "ctx-b", ClusterID: "cluster-b", SSB: ssb("cis-weekly", "ocp4-cis-node", "ocp4-cis"), Schedule: "0 2 * * *"},
	}

	results, collector := MergeSSBs(inputs)

	if len(collector.All()) != 0 {
		t.Errorf("expected no problems, got: %v", collector.All())
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if len(r.ClusterIDs) != 2 {
		t.Errorf("expected 2 cluster IDs, got %v", r.ClusterIDs)
	}
	// Cluster IDs must be sorted.
	if r.ClusterIDs[0] != "cluster-a" || r.ClusterIDs[1] != "cluster-b" {
		t.Errorf("ClusterIDs not sorted: got %v", r.ClusterIDs)
	}
}

// IMP-MAP-020
func TestMergeSSBs_ProfileMismatch(t *testing.T) {
	inputs := []ClusterSSB{
		{Context: "ctx-a", ClusterID: "cluster-a", SSB: ssb("cis-weekly", "ocp4-cis"), Schedule: "0 2 * * *"},
		{Context: "ctx-b", ClusterID: "cluster-b", SSB: ssb("cis-weekly", "ocp4-pci-dss"), Schedule: "0 2 * * *"},
	}

	results, collector := MergeSSBs(inputs)

	if len(results) != 0 {
		t.Errorf("expected 0 results on mismatch, got %d", len(results))
	}
	if !collector.HasCategory("conflict") {
		t.Error("expected conflict problem, got none")
	}
}

// IMP-MAP-020a
func TestMergeSSBs_ScheduleMismatch(t *testing.T) {
	inputs := []ClusterSSB{
		{Context: "ctx-a", ClusterID: "cluster-a", SSB: ssb("cis-weekly", "ocp4-cis"), Schedule: "0 2 * * *"},
		{Context: "ctx-b", ClusterID: "cluster-b", SSB: ssb("cis-weekly", "ocp4-cis"), Schedule: "0 3 * * *"},
	}

	results, collector := MergeSSBs(inputs)

	if len(results) != 0 {
		t.Errorf("expected 0 results on schedule mismatch, got %d", len(results))
	}
	if !collector.HasCategory("conflict") {
		t.Error("expected conflict problem, got none")
	}
}

// Multiple SSB names: one merges, one conflicts
func TestMergeSSBs_MixedOutcomes(t *testing.T) {
	inputs := []ClusterSSB{
		{Context: "ctx-a", ClusterID: "cluster-a", SSB: ssb("ok-binding", "ocp4-cis"), Schedule: "0 2 * * *"},
		{Context: "ctx-b", ClusterID: "cluster-b", SSB: ssb("ok-binding", "ocp4-cis"), Schedule: "0 2 * * *"},
		{Context: "ctx-a", ClusterID: "cluster-a", SSB: ssb("bad-binding", "ocp4-cis"), Schedule: "0 2 * * *"},
		{Context: "ctx-b", ClusterID: "cluster-b", SSB: ssb("bad-binding", "ocp4-pci-dss"), Schedule: "0 2 * * *"},
	}

	results, collector := MergeSSBs(inputs)

	if len(results) != 1 {
		t.Fatalf("expected 1 successful result, got %d", len(results))
	}
	if results[0].ScanName != "ok-binding" {
		t.Errorf("expected ok-binding to succeed, got %q", results[0].ScanName)
	}
	if !collector.HasCategory("conflict") {
		t.Error("expected conflict problem for bad-binding")
	}
}

func TestMergeSSBs_EmptyInput(t *testing.T) {
	results, collector := MergeSSBs(nil)

	if len(results) != 0 {
		t.Errorf("expected 0 results for empty input, got %d", len(results))
	}
	if len(collector.All()) != 0 {
		t.Errorf("expected no problems for empty input, got %v", collector.All())
	}
}

// Insertion order of distinct SSB names must be preserved in output.
func TestMergeSSBs_OutputOrderPreserved(t *testing.T) {
	inputs := []ClusterSSB{
		{Context: "ctx-a", ClusterID: "cluster-a", SSB: ssb("zzz", "ocp4-cis"), Schedule: "0 1 * * *"},
		{Context: "ctx-a", ClusterID: "cluster-a", SSB: ssb("aaa", "ocp4-cis"), Schedule: "0 2 * * *"},
		{Context: "ctx-a", ClusterID: "cluster-a", SSB: ssb("mmm", "ocp4-cis"), Schedule: "0 3 * * *"},
	}

	results, _ := MergeSSBs(inputs)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	want := []string{"zzz", "aaa", "mmm"}
	for i, r := range results {
		if r.ScanName != want[i] {
			t.Errorf("result[%d]: got %q, want %q", i, r.ScanName, want[i])
		}
	}
}

func TestProfilesEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want bool
	}{
		{name: "both empty", a: nil, b: nil, want: true},
		{name: "same single", a: []string{"x"}, b: []string{"x"}, want: true},
		{name: "same multiple sorted", a: []string{"a", "b"}, b: []string{"a", "b"}, want: true},
		{name: "same multiple unsorted", a: []string{"b", "a"}, b: []string{"a", "b"}, want: true},
		{name: "different values", a: []string{"a"}, b: []string{"b"}, want: false},
		{name: "different lengths", a: []string{"a", "b"}, b: []string{"a"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := profilesEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("profilesEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
