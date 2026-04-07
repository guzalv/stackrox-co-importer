// Package listssbs handles the --list-ssbs output mode.
package listssbs

import (
	"fmt"
	"io"
	"sort"

	"github.com/stackrox/co-importer/internal/models"
)

// Print writes one "namespace/name" line per SSB to w, sorted lexicographically.
// IMP-CLI-029
func Print(ssbs []models.ScanSettingBinding, w io.Writer) {
	lines := make([]string, 0, len(ssbs))
	for _, ssb := range ssbs {
		ns := ssb.Namespace
		if ns == "" {
			ns = "openshift-compliance"
		}
		lines = append(lines, ns+"/"+ssb.Name)
	}
	sort.Strings(lines)
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}
