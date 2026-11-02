package adoptexternal

import (
	"fmt"

	"github.com/guzalv/stackrox-co-importer/internal/problems"
)

// OrphanNote builds the "orphan" problem recorded when a ScanSetting that
// a deleted external ScanSettingBinding referenced is intentionally left in
// place after the ACS scan configuration it blocked was created
// successfully. The importer never deletes a ScanSetting itself: it may
// still be referenced by other resources, so an operator must decide whether
// it is safe to remove. It is reported at severity "info": an informational
// note, not a failure, and exit-neutral (it never drives exit code 2).
// IMP-ADEXT-010
func OrphanNote(scanSettingName, ctxName, namespace string) problems.Problem {
	return problems.Problem{
		Severity:    "info",
		Category:    "orphan",
		ResourceRef: scanSettingName,
		Description: fmt.Sprintf("ScanSetting %q in namespace %q (context %s) may now be unused: the external ScanSettingBinding that referenced it was removed", scanSettingName, namespace, ctxName),
		FixHint:     fmt.Sprintf("Review whether ScanSetting %q is still needed and delete it manually if not", scanSettingName),
	}
}
