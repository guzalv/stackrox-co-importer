// Package filter provides SSB filtering utilities.
package filter

import (
	"regexp"

	"github.com/stackrox/co-importer/internal/models"
)

// ExcludeSSBs returns the subset of ssbs whose names do NOT match any of the
// given Go regular expression patterns (IMP-CLI-028). Patterns are assumed to
// be valid — they were already compiled successfully during config parsing.
func ExcludeSSBs(ssbs []models.ScanSettingBinding, patterns []string) []models.ScanSettingBinding {
	if len(patterns) == 0 {
		return ssbs
	}

	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			// Patterns were validated at config parse time; this should never happen.
			continue
		}
		compiled = append(compiled, re)
	}

	result := ssbs[:0:0] // same underlying type, zero length, no shared backing array
	for i := range ssbs {
		if !matchesAny(ssbs[i].Name, compiled) {
			result = append(result, ssbs[i])
		}
	}
	return result
}

func matchesAny(name string, patterns []*regexp.Regexp) bool {
	for _, re := range patterns {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}
