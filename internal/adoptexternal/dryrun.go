package adoptexternal

import (
	"fmt"

	"github.com/guzalv/stackrox-co-importer/internal/merge"
)

// DryRunBlocker reports, for a scanName blocked by external
// ScanSettingBinding(s), what the adoption workflow would do if --dry-run
// were not set, without performing any of those actions itself.
//
// Unlike ResolveBlocker/ResolveMergedBlockers, DryRunBlocker never calls
// WriteBackup (no backup directory or file is created), never calls a
// Deleter (no ScanSettingBinding is deleted), and never calls a Confirmer
// (dry-run never prompts, since nothing destructive is about to happen). It
// only returns human-readable messages describing what would happen, for
// the caller to log.
//
// Once a blocker is confirmed gone and the workflow would move on to
// creating or updating the ACS scan configuration, callers MUST reuse the
// existing reconcile.Reconciler dry-run path (reconcile.Options.DryRun)
// rather than a second dry-run mechanism here: reconcile.Reconciler already
// never performs a write when DryRun is set, and already returns a "would
// create"/"would update" ActionResult.Reason. DryRunBlocker only covers the
// part of the workflow reconcile.Reconciler doesn't: backup and blocker
// removal.
// IMP-ADEXT-014
func DryRunBlocker(blockers []merge.ClusterSSB, scanName string, deleteExisting bool) []string {
	if !deleteExisting {
		return []string{fmt.Sprintf(
			"dry-run: %q is blocked by external ScanSettingBinding(s) %s; would report the conflict and skip (re-run with --delete-existing to remove them, after pausing GitOps reconciliation)",
			scanName, blockerRefs(blockers),
		)}
	}

	return []string{
		fmt.Sprintf("dry-run: would back up ScanSettingBinding(s) %s before removing them", blockerRefs(blockers)),
		fmt.Sprintf("dry-run: would delete blocking ScanSettingBinding(s) %s", blockerRefs(blockers)),
		fmt.Sprintf("dry-run: would create or update ACS scan configuration %q once the blocker(s) are confirmed gone", scanName),
	}
}
