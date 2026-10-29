package adoptexternal

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/stackrox/co-importer/internal/merge"
	"github.com/stackrox/co-importer/internal/problems"
)

// Deleter abstracts the K8s delete operation needed to remove a blocking
// ScanSettingBinding. A real dynamic-client-backed implementation is wired
// into internal/run.go by a later task; ResolveBlocker only needs the
// interface so tests can supply a fake.
type Deleter interface {
	DeleteScanSettingBinding(ctxName, namespace, name string) error
}

// Exister abstracts checking whether a ScanSettingBinding currently exists.
// SettleGuard uses it after a blocking ScanSettingBinding has been deleted,
// to detect a GitOps tool recreating it (self-heal) before the settle
// timeout elapses. A real dynamic-client-backed implementation is wired into
// internal/run.go by a later task; ResolveBlocker and SettleGuard only need
// the interface so tests can supply a fake.
type Exister interface {
	ScanSettingBindingExists(ctxName, namespace, name string) (bool, error)
}

// Confirmer prompts the operator with prompt and reports whether they
// confirmed (true) or declined (false) the action it describes. A real
// stdin-backed implementation is wired in by a later task; ResolveBlocker
// only needs the function type so tests can supply a fake.
type Confirmer func(prompt string) (bool, error)

// ResolveBlocker decides what happens to scanName given the external
// ScanSettingBindings (blockers) that block ACS from creating or updating
// it, and executes that decision:
//
//   - deleteExisting is false (the default, IMP-ADEXT-003): the conflict is
//     recorded as a "gitops" problem and the scan is skipped. deleter and
//     confirmer are never called, and nothing blocks waiting for the
//     ScanSettingBinding to be removed.
//   - deleteExisting is true and yes is true (IMP-ADEXT-004): confirmer is
//     never called; every blocker is deleted immediately.
//   - deleteExisting is true and yes is false (IMP-ADEXT-004, IMP-ADEXT-005):
//     confirmer is called once; if it confirms, every blocker is deleted; if
//     it declines, the scan is skipped exactly like the default case (not
//     treated as a fatal error).
//
// Once every blocker is deleted, and only if exister is non-nil and
// settleTimeout is positive, ResolveBlocker guards against GitOps self-heal
// (IMP-ADEXT-006, IMP-ADEXT-007) by polling exister via SettleGuard for each
// deleted blocker. If any blocker's ScanSettingBinding reappears within
// settleTimeout, that is recorded as a "gitops" problem and the scan is
// skipped, exactly like declining the confirmation prompt. poll is forwarded
// to SettleGuard; pass nil in production to use time.Sleep, or an
// instant-returning fake in tests. When exister is nil or settleTimeout is
// zero, no settle check is performed (used by callers that have not
// configured one, e.g. IMP-ADEXT-004's scenarios).
//
// Recorded problems are added to collector. ResolveBlocker returns whether
// scanName's blockers are confirmed gone, i.e. whether the caller may
// proceed to create or update the ACS scan configuration.
func ResolveBlocker(collector *problems.Collector, blockers []merge.ClusterSSB, scanName string, deleteExisting, yes bool, deleter Deleter, confirmer Confirmer, exister Exister, settleTimeout time.Duration, poll func(time.Duration)) (bool, error) {
	// IMP-ADEXT-003: default behavior reports the conflict and skips. No
	// deleter or confirmer call, no waiting.
	if !deleteExisting {
		collector.Add(blockerSkipProblem(blockers, scanName))
		return false, nil
	}

	// IMP-ADEXT-004, IMP-ADEXT-005
	confirmed, err := confirmDeletion(blockers, scanName, yes, confirmer)
	if err != nil {
		return false, err
	}
	if !confirmed {
		// IMP-ADEXT-005: declining is treated like the default skip, not a fatal error.
		collector.Add(blockerSkipProblem(blockers, scanName))
		return false, nil
	}

	// IMP-ADEXT-004
	if err := deleteBlockers(blockers, deleter); err != nil {
		return false, err
	}

	// IMP-ADEXT-006, IMP-ADEXT-007: settle-timeout guard against GitOps
	// self-heal. Skipped entirely when the caller has not configured a
	// settle check.
	if exister != nil && settleTimeout > 0 {
		for _, b := range blockers {
			if b.SSB == nil {
				continue
			}
			reappeared, err := SettleGuard(exister, b.Context, b.SSB.Namespace, b.SSB.Name, settleTimeout, poll)
			if err != nil {
				return false, fmt.Errorf("checking whether ScanSettingBinding %s/%s on %s reappeared: %w", b.SSB.Namespace, b.SSB.Name, b.Context, err)
			}
			if reappeared {
				collector.Add(blockerReappearedProblem(blockers, scanName))
				return false, nil
			}
		}
	}

	return true, nil
}

// ResolveMergedBlockers extends ResolveBlocker to the case where a single
// merged ACS scan configuration (scanName) is blocked by external
// ScanSettingBindings on multiple clusters at once. Because the merged
// create spans every one of those clusters in a single ACS API call (see
// merge.MergedConfig.ClusterIDs), it may only proceed once every cluster's
// blocker is confirmed gone.
//
// perClusterBlockers groups the blockers by the kubecontext they came from.
// The --delete-existing/--yes/confirmation decision (IMP-ADEXT-003,
// IMP-ADEXT-004, IMP-ADEXT-005) and the deletion itself are made once, for
// every blocker across every cluster together, exactly like ResolveBlocker
// does for a single cluster. Once deleted, the settle-timeout guard against
// GitOps self-heal (IMP-ADEXT-006, IMP-ADEXT-007) is run independently per
// cluster: IMP-ADEXT-008 requires that a reappearance on even one cluster
// blocks the merged create, even if every other cluster's blocker stayed
// gone. Exactly one "gitops" problem is recorded, naming every cluster whose
// blocker reappeared.
//
// ResolveMergedBlockers returns whether every cluster's blocker is confirmed
// gone, i.e. whether the caller may proceed with the merged create.
// IMP-ADEXT-008
func ResolveMergedBlockers(collector *problems.Collector, scanName string, perClusterBlockers map[string][]merge.ClusterSSB, deleteExisting, yes bool, deleter Deleter, confirmer Confirmer, exister Exister, settleTimeout time.Duration, poll func(time.Duration)) (bool, error) {
	contexts := make([]string, 0, len(perClusterBlockers))
	for ctxName := range perClusterBlockers {
		contexts = append(contexts, ctxName)
	}
	sort.Strings(contexts)

	var allBlockers []merge.ClusterSSB
	for _, ctxName := range contexts {
		allBlockers = append(allBlockers, perClusterBlockers[ctxName]...)
	}

	if !deleteExisting {
		collector.Add(blockerSkipProblem(allBlockers, scanName))
		return false, nil
	}

	confirmed, err := confirmDeletion(allBlockers, scanName, yes, confirmer)
	if err != nil {
		return false, err
	}
	if !confirmed {
		collector.Add(blockerSkipProblem(allBlockers, scanName))
		return false, nil
	}

	if err := deleteBlockers(allBlockers, deleter); err != nil {
		return false, err
	}

	if exister == nil || settleTimeout <= 0 {
		return true, nil
	}

	// IMP-ADEXT-008: check every cluster independently; the merged create
	// may only proceed if none of them reappeared.
	var reappearedOn []string
	for _, ctxName := range contexts {
		for _, b := range perClusterBlockers[ctxName] {
			if b.SSB == nil {
				continue
			}
			reappeared, err := SettleGuard(exister, b.Context, b.SSB.Namespace, b.SSB.Name, settleTimeout, poll)
			if err != nil {
				return false, fmt.Errorf("checking whether ScanSettingBinding %s/%s on %s reappeared: %w", b.SSB.Namespace, b.SSB.Name, b.Context, err)
			}
			if reappeared {
				reappearedOn = append(reappearedOn, ctxName)
				break
			}
		}
	}

	if len(reappearedOn) > 0 {
		collector.Add(mergedBlockerReappearedProblem(allBlockers, scanName, reappearedOn))
		return false, nil
	}

	return true, nil
}

// confirmDeletion handles the --delete-existing confirm/skip decision shared
// by ResolveBlocker and ResolveMergedBlockers: when yes is true the operator
// already opted into skipping the prompt (IMP-ADEXT-004); otherwise
// confirmer is called once with a prompt describing blockers
// (IMP-ADEXT-004, IMP-ADEXT-005).
func confirmDeletion(blockers []merge.ClusterSSB, scanName string, yes bool, confirmer Confirmer) (bool, error) {
	if yes {
		return true, nil
	}
	if confirmer == nil {
		return false, fmt.Errorf("--delete-existing requires a confirmation prompt but none was provided for %q", scanName)
	}
	confirmed, err := confirmer(blockerDeletePrompt(blockers, scanName))
	if err != nil {
		return false, fmt.Errorf("confirming deletion of blocking ScanSettingBinding(s) for %q: %w", scanName, err)
	}
	return confirmed, nil
}

// deleteBlockers deletes every blocker via deleter, shared by ResolveBlocker
// and ResolveMergedBlockers. IMP-ADEXT-004
func deleteBlockers(blockers []merge.ClusterSSB, deleter Deleter) error {
	for _, b := range blockers {
		if b.SSB == nil {
			continue
		}
		if err := deleter.DeleteScanSettingBinding(b.Context, b.SSB.Namespace, b.SSB.Name); err != nil {
			return fmt.Errorf("deleting blocking ScanSettingBinding %s/%s on %s: %w", b.SSB.Namespace, b.SSB.Name, b.Context, err)
		}
	}
	return nil
}

// SettleGuard polls exister for up to settleTimeout to detect whether the
// ScanSettingBinding named name (namespace/name on ctxName) reappears after
// being deleted, i.e. a GitOps tool self-healing by recreating it. It
// returns true as soon as the ScanSettingBinding is observed to exist, or
// false once settleTimeout elapses without it reappearing.
//
// poll is called between checks and should block for approximately the
// given duration; pass nil to use time.Sleep. Tests inject a fast or
// instant-returning poll function so the guard can be exercised
// deterministically without waiting out settleTimeout for real.
// IMP-ADEXT-006, IMP-ADEXT-007
func SettleGuard(exister Exister, ctxName, namespace, name string, settleTimeout time.Duration, poll func(time.Duration)) (bool, error) {
	if poll == nil {
		poll = time.Sleep
	}

	const steps = 5
	interval := settleTimeout / steps
	if interval <= 0 {
		interval = settleTimeout
	}

	for elapsed := time.Duration(0); ; {
		exists, err := exister.ScanSettingBindingExists(ctxName, namespace, name)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
		if elapsed >= settleTimeout {
			return false, nil
		}
		poll(interval)
		elapsed += interval
	}
}

// blockerSkipProblem builds the "gitops" problem recorded whenever a
// blocking external ScanSettingBinding is left in place, whether because
// --delete-existing was not set (IMP-ADEXT-003) or because the operator
// declined the confirmation prompt (IMP-ADEXT-005).
func blockerSkipProblem(blockers []merge.ClusterSSB, scanName string) problems.Problem {
	return problems.Problem{
		Severity:    "warning",
		Category:    "gitops",
		ResourceRef: scanName,
		Description: fmt.Sprintf("%q is blocked by external ScanSettingBinding(s): %s", scanName, blockerRefs(blockers)),
		FixHint:     "Re-run with --delete-existing to remove the blocking ScanSettingBinding(s), after pausing GitOps reconciliation for them",
		Skipped:     true,
	}
}

// blockerReappearedProblem builds the "gitops" problem recorded when a
// deleted blocking ScanSettingBinding reappears within the settle timeout,
// i.e. a GitOps tool self-healed it before the create could safely proceed.
// IMP-ADEXT-006, IMP-ADEXT-007
func blockerReappearedProblem(blockers []merge.ClusterSSB, scanName string) problems.Problem {
	return problems.Problem{
		Severity:    "warning",
		Category:    "gitops",
		ResourceRef: scanName,
		Description: fmt.Sprintf("%q: ScanSettingBinding(s) reappeared within the settle timeout after deletion, indicating GitOps self-heal: %s", scanName, blockerRefs(blockers)),
		FixHint:     "Pause GitOps reconciliation for the blocking ScanSettingBinding(s), then re-run with --delete-existing",
		Skipped:     true,
	}
}

// mergedBlockerReappearedProblem builds the "gitops" problem recorded when a
// merged scan configuration's create cannot proceed because at least one
// cluster's deleted blocking ScanSettingBinding reappeared within the settle
// timeout. reappearedOn lists the kubecontext(s) where a reappearance was
// observed, so the operator knows exactly which cluster(s) still need GitOps
// reconciliation paused.
// IMP-ADEXT-008
func mergedBlockerReappearedProblem(blockers []merge.ClusterSSB, scanName string, reappearedOn []string) problems.Problem {
	return problems.Problem{
		Severity:    "warning",
		Category:    "gitops",
		ResourceRef: scanName,
		Description: fmt.Sprintf(
			"%q spans multiple clusters and cannot be created: ScanSettingBinding(s) reappeared within the settle timeout on cluster(s) %s, indicating GitOps self-heal: %s",
			scanName, strings.Join(reappearedOn, ", "), blockerRefs(blockers),
		),
		FixHint: "Pause GitOps reconciliation for the blocking ScanSettingBinding(s) on the listed cluster(s), then re-run with --delete-existing",
		Skipped: true,
	}
}

// blockerDeletePrompt builds the confirmation prompt shown to the operator
// before deleting blockers.
func blockerDeletePrompt(blockers []merge.ClusterSSB, scanName string) string {
	return fmt.Sprintf("Delete external ScanSettingBinding(s) %s blocking ACS scan configuration %q? [y/N]", blockerRefs(blockers), scanName)
}

// blockerRefs formats blockers as a human-readable "namespace/name (context
// ctx)" list for problem descriptions and confirmation prompts.
func blockerRefs(blockers []merge.ClusterSSB) string {
	refs := make([]string, 0, len(blockers))
	for _, b := range blockers {
		if b.SSB == nil {
			continue
		}
		refs = append(refs, fmt.Sprintf("%s/%s (context %s)", b.SSB.Namespace, b.SSB.Name, b.Context))
	}
	return strings.Join(refs, ", ")
}
