package adoptexternal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// LoadPlan reads and JSON-unmarshals plan.json from dir, the resumable state
// written by WriteBackup. It returns (nil, nil) when dir has no plan.json,
// so a caller can distinguish "nothing to resume" from a real I/O or parse
// error. IMP-ADEXT-011, IMP-ADEXT-012
func LoadPlan(dir string) (*Plan, error) {
	data, err := os.ReadFile(filepath.Join(dir, "plan.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading plan.json in %q: %w", dir, err)
	}
	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("unmarshalling plan.json in %q: %w", dir, err)
	}
	return &plan, nil
}

// ValidateEnvironment compares a persisted plan's stamped environment
// against the current run's environment, and returns an error mentioning
// "environment mismatch" if the plan belongs to a different ACS Central. A
// resumed plan MUST NOT be used across environments, since the cluster IDs
// and scan configuration it describes only make sense against the Central
// (and clusters) it was created for. IMP-ADEXT-012
func ValidateEnvironment(plan *Plan, currentEnv EnvStamp) error {
	if plan == nil {
		return fmt.Errorf("no plan to validate")
	}
	if plan.Env.ACSEndpoint != currentEnv.ACSEndpoint {
		return fmt.Errorf(
			"environment mismatch: plan.json was created for ACS endpoint %q, but the current run targets %q",
			plan.Env.ACSEndpoint, currentEnv.ACSEndpoint,
		)
	}
	return nil
}
