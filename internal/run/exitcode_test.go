package run

import (
	"testing"

	"github.com/guzalv/stackrox-co-importer/internal/problems"
)

// TestIMP_CLI_019_ExitCodeForRun verifies the exit code decision: any failed
// binding forces ExitPartialError, and so does a "gitops" problem even with
// zero failures, since it means a requested import did not happen (reported
// by default, declined at the confirmation prompt, or blocked by GitOps
// self-heal) and so must not be indistinguishable from full success.
// IMP-CLI-019
func TestIMP_CLI_019_ExitCodeForRun(t *testing.T) {
	tests := []struct {
		name      string
		failCount int
		probs     []problems.Problem
		want      int
	}{
		{
			name:      "no failures, no problems",
			failCount: 0,
			probs:     nil,
			want:      ExitSuccess,
		},
		{
			name:      "failures present",
			failCount: 1,
			probs:     nil,
			want:      ExitPartialError,
		},
		{
			name:      "no failures, unrelated problem category",
			failCount: 0,
			probs:     []problems.Problem{{Category: "orphan"}},
			want:      ExitSuccess,
		},
		{
			name:      "no failures, gitops-blocked skip",
			failCount: 0,
			probs:     []problems.Problem{{Category: "conflict"}, {Category: "gitops"}},
			want:      ExitPartialError,
		},
		{
			name:      "failures and gitops both present",
			failCount: 2,
			probs:     []problems.Problem{{Category: "gitops"}},
			want:      ExitPartialError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exitCodeForRun(tt.failCount, tt.probs)
			if got != tt.want {
				t.Errorf("exitCodeForRun(%d, %v) = %d, want %d", tt.failCount, tt.probs, got, tt.want)
			}
		})
	}
}
