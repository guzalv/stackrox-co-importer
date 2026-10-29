package adoptexternal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"

	"github.com/stackrox/co-importer/internal/merge"
	"github.com/stackrox/co-importer/internal/models"
)

// Status values for PlannedScan.Status. Only StatusPending and StatusBackedUp
// are reachable today; StatusDeleted and StatusCreated are reserved for the
// delete/create workflow that is not yet built.
const (
	StatusPending  = "pending"
	StatusBackedUp = "backed-up"
	StatusDeleted  = "deleted"
	StatusCreated  = "created"
)

// EnvStamp identifies the ACS environment and clusters a plan was created
// against. A future resume (IMP-ADEXT-011, IMP-ADEXT-012) MUST compare this
// against the current run's environment before reusing a persisted plan, to
// refuse a plan that belongs to a different Central or cluster set.
type EnvStamp struct {
	ACSEndpoint string   `json:"acsEndpoint"`
	ClusterIDs  []string `json:"clusterIds"`
	Contexts    []string `json:"contexts"`
}

// SourceRef identifies the cluster-local resources backing one cluster's
// contribution to a planned ACS scan configuration.
type SourceRef struct {
	Context         string `json:"context"`
	Namespace       string `json:"namespace"`
	SSBName         string `json:"ssbName"`
	ScanSettingName string `json:"scanSettingName"`
}

// PlannedScan is the resumable state for one ACS scan configuration being
// adopted from one or more external ScanSettingBindings.
type PlannedScan struct {
	ScanName   string      `json:"scanName"`
	Profiles   []string    `json:"profiles"`
	Schedule   string      `json:"schedule"`
	ClusterIDs []string    `json:"clusterIds"`
	Sources    []SourceRef `json:"sources"`
	Status     string      `json:"status"`
}

// Plan is the full resumable state persisted to plan.json before any
// destructive action (deleting an external ScanSettingBinding) is taken, so
// an interrupted or manually-corrected run can resume (IMP-ADEXT-011,
// IMP-ADEXT-012) instead of re-discovering from scratch.
type Plan struct {
	Env   EnvStamp               `json:"env"`
	Scans map[string]PlannedScan `json:"scans"`
}

// ScanSettingSource pairs a ScanSetting with the kubecontext it was read
// from, so its backup file name can encode where it came from.
type ScanSettingSource struct {
	Context     string
	ScanSetting models.ScanSetting
}

// WriteBackup writes a YAML copy of every blocking ScanSettingBinding in
// ssbs, a YAML copy of every referenced ScanSetting in scanSettings, and the
// resumable plan describing the scan configuration(s) being adopted, into
// dir. It creates dir if it does not already exist.
//
// WriteBackup performs no deletion of any kind and has no dependency on any
// prior or subsequent deletion. The (future) delete workflow MUST call
// WriteBackup and wait for it to return a nil error before deleting any
// ScanSettingBinding, so a backup always exists before the first removal
// action (IMP-ADEXT-002).
func WriteBackup(dir string, plan Plan, ssbs []merge.ClusterSSB, scanSettings []ScanSettingSource) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating backup directory %q: %w", dir, err)
	}

	for _, entry := range ssbs {
		if entry.SSB == nil {
			continue
		}
		data, err := yaml.Marshal(entry.SSB)
		if err != nil {
			return fmt.Errorf("marshalling ScanSettingBinding %q to YAML: %w", entry.SSB.Name, err)
		}
		fileName := fmt.Sprintf("%s__%s__ssb__%s.yaml", entry.Context, entry.SSB.Namespace, entry.SSB.Name)
		if err := os.WriteFile(filepath.Join(dir, fileName), data, 0o644); err != nil {
			return fmt.Errorf("writing backup file %q: %w", fileName, err)
		}
	}

	for _, entry := range scanSettings {
		data, err := yaml.Marshal(entry.ScanSetting)
		if err != nil {
			return fmt.Errorf("marshalling ScanSetting %q to YAML: %w", entry.ScanSetting.Name, err)
		}
		fileName := fmt.Sprintf("%s__%s__scansetting__%s.yaml", entry.Context, entry.ScanSetting.Namespace, entry.ScanSetting.Name)
		if err := os.WriteFile(filepath.Join(dir, fileName), data, 0o644); err != nil {
			return fmt.Errorf("writing backup file %q: %w", fileName, err)
		}
	}

	planData, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling plan.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), planData, 0o644); err != nil {
		return fmt.Errorf("writing plan.json: %w", err)
	}

	return nil
}

// CheckBackupDir validates dir against the --backup-dir contract (IMP-CLI-032):
// the directory must be either new (not yet existing), empty, or contain a
// plan.json from a prior run. Any other non-empty directory is rejected so
// the importer never writes backup files into an unrelated directory
// (IMP-ADEXT-015).
func CheckBackupDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("checking backup directory %q: %w", dir, err)
	}
	if len(entries) == 0 {
		return nil
	}
	for _, entry := range entries {
		if !entry.IsDir() && entry.Name() == "plan.json" {
			return nil
		}
	}
	return fmt.Errorf("backup directory %q is not empty and does not contain a plan.json from a prior run", dir)
}
