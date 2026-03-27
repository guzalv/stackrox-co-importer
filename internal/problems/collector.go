// Package problems provides a collector for structured issues during import runs.
package problems

// Problem is a structured issue entry recorded during an importer run.
type Problem struct {
	Severity    string `json:"severity"`    // "error" or "warning"
	Category    string `json:"category"`    // "mapping", "conflict", "input", etc.
	ResourceRef string `json:"resourceRef"` // "namespace/name" or synthetic
	Description string `json:"description"`
	FixHint     string `json:"fixHint"`
	Skipped     bool   `json:"skipped"`
}

// Collector accumulates problems during a run.
type Collector struct {
	problems []Problem
}

// New creates a new problem collector.
func New() *Collector {
	return &Collector{}
}

// Add records a problem.
func (c *Collector) Add(p Problem) {
	c.problems = append(c.problems, p)
}

// All returns all collected problems.
func (c *Collector) All() []Problem {
	return c.problems
}

// HasCategory returns true if any problem has the given category.
func (c *Collector) HasCategory(cat string) bool {
	for _, p := range c.problems {
		if p.Category == cat {
			return true
		}
	}
	return false
}

// ForResource returns problems matching the given resource reference.
func (c *Collector) ForResource(ref string) []Problem {
	var result []Problem
	for _, p := range c.problems {
		if p.ResourceRef == ref {
			result = append(result, p)
		}
	}
	return result
}
