package mapping

import (
	"strings"
	"testing"

	"github.com/stackrox/co-importer/internal/models"
)

// IMP-MAP-001, IMP-MAP-002
func TestResolveProfiles(t *testing.T) {
	tests := []struct {
		name string
		refs []models.ProfileRef
		want []string
	}{
		{
			name: "empty input",
			refs: nil,
			want: []string{},
		},
		{
			name: "single profile",
			refs: []models.ProfileRef{{Name: "ocp4-cis"}},
			want: []string{"ocp4-cis"},
		},
		{
			name: "sorted output",
			refs: []models.ProfileRef{
				{Name: "ocp4-cis-node"},
				{Name: "ocp4-cis"},
				{Name: "ocp4-pci-dss"},
			},
			want: []string{"ocp4-cis", "ocp4-cis-node", "ocp4-pci-dss"},
		},
		{
			name: "duplicates deduplicated",
			refs: []models.ProfileRef{
				{Name: "ocp4-cis"},
				{Name: "ocp4-cis"},
				{Name: "ocp4-cis-node"},
			},
			want: []string{"ocp4-cis", "ocp4-cis-node"},
		},
		{
			name: "kind does not affect output",
			refs: []models.ProfileRef{
				{Name: "my-tp", Kind: "TailoredProfile"},
				{Name: "ocp4-cis", Kind: "Profile"},
			},
			want: []string{"my-tp", "ocp4-cis"},
		},
		{
			name: "missing kind defaults to Profile (IMP-MAP-002)",
			refs: []models.ProfileRef{
				{Name: "custom-x"},
			},
			want: []string{"custom-x"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveProfiles(tc.refs)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v (len %d), want %v (len %d)", got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// IMP-MAP-005, IMP-MAP-006
func TestBuildDescription(t *testing.T) {
	ssb := &models.ScanSettingBinding{Namespace: "openshift-compliance", Name: "cis-weekly"}
	ss := &models.ScanSetting{Name: "default-auto-apply"}

	desc := BuildDescription(ssb, ss)
	if !strings.Contains(desc, "cis-weekly") {
		t.Errorf("description missing binding name: %q", desc)
	}
	if !strings.Contains(desc, "openshift-compliance") {
		t.Errorf("description missing namespace: %q", desc)
	}
	if !strings.Contains(desc, "default-auto-apply") {
		t.Errorf("description missing ScanSetting name: %q", desc)
	}

	// nil ScanSetting should not panic and should omit ScanSetting detail
	descNoSS := BuildDescription(ssb, nil)
	if !strings.Contains(descNoSS, "cis-weekly") {
		t.Errorf("nil-ss description missing binding name: %q", descNoSS)
	}
}

// IMP-MAP-004, IMP-MAP-012..015
func TestBuildPayload_InvalidScheduleProducesError(t *testing.T) {
	ssb := &models.ScanSettingBinding{
		Namespace: "openshift-compliance",
		Name:      "bad-binding",
		Profiles:  []models.ProfileRef{{Name: "ocp4-cis"}},
	}
	ss := &models.ScanSetting{Name: "bad-ss", Schedule: "not-a-cron"}

	result := BuildPayload(ssb, ss, "cluster-id")

	if result.Payload != nil {
		t.Error("expected nil payload on invalid schedule, got non-nil")
	}
	if result.Problem == nil {
		t.Fatal("expected problem on invalid schedule, got nil")
	}
	if result.Problem.Category != "mapping" {
		t.Errorf("problem category: got %q, want %q", result.Problem.Category, "mapping")
	}
	if !result.Problem.Skipped {
		t.Error("problem.Skipped should be true")
	}
	if result.Problem.FixHint == "" {
		t.Error("problem.FixHint should not be empty")
	}
}

func TestBuildPayload_HappyPath(t *testing.T) {
	ssb := &models.ScanSettingBinding{
		Namespace: "openshift-compliance",
		Name:      "cis-weekly",
		Profiles:  []models.ProfileRef{{Name: "ocp4-cis"}, {Name: "ocp4-cis-node"}},
	}
	ss := &models.ScanSetting{Name: "default", Schedule: "0 2 * * *"}

	result := BuildPayload(ssb, ss, "cluster-abc")

	if result.Problem != nil {
		t.Fatalf("unexpected problem: %+v", result.Problem)
	}
	if result.Payload == nil {
		t.Fatal("expected non-nil payload")
	}

	p := result.Payload
	if p.ScanName != "cis-weekly" {
		t.Errorf("ScanName: got %q, want %q", p.ScanName, "cis-weekly")
	}
	if p.ScanConfig.OneTimeScan {
		t.Error("OneTimeScan should be false")
	}
	if len(p.Clusters) != 1 || p.Clusters[0] != "cluster-abc" {
		t.Errorf("Clusters: got %v, want [cluster-abc]", p.Clusters)
	}
	if len(p.ScanConfig.Profiles) != 2 {
		t.Errorf("Profiles: got %v, want 2 entries", p.ScanConfig.Profiles)
	}
	if p.ScanConfig.ScanSchedule == nil {
		t.Error("ScanSchedule should not be nil")
	}
	if p.ScanConfig.ScanSchedule.IntervalType != "DAILY" {
		t.Errorf("schedule type: got %q, want DAILY", p.ScanConfig.ScanSchedule.IntervalType)
	}
}
