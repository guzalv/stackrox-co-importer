package adoptexternal

import (
	"testing"
)

// IMP-ADEXT-017: each blocked scan is backed up in its own WriteBackup call, so
// WriteBackup must ACCUMULATE scans into plan.json across calls, not overwrite
// it. Otherwise a re-run after manual SSB deletion (IMP-ADEXT-011) recreates
// only the last scan.
func TestIMP_ADEXT_017_WriteBackupAccumulatesPlan(t *testing.T) {
	dir := t.TempDir()

	planFor := func(name string) Plan {
		return Plan{
			Env: EnvStamp{ACSEndpoint: "https://central.example.com", ClusterIDs: []string{"c1"}},
			Scans: map[string]PlannedScan{
				name: {ScanName: name, Profiles: []string{"ocp4-cis"}, Schedule: "0 1 * * *", Status: StatusBackedUp},
			},
		}
	}

	if err := WriteBackup(dir, planFor("ssb-cis"), nil, nil); err != nil {
		t.Fatalf("first WriteBackup: %v", err)
	}
	if err := WriteBackup(dir, planFor("ssb-stig"), nil, nil); err != nil {
		t.Fatalf("second WriteBackup: %v", err)
	}

	plan, err := LoadPlan(dir)
	if err != nil {
		t.Fatalf("LoadPlan: %v", err)
	}
	if plan == nil {
		t.Fatal("expected a plan, got nil")
	}
	for _, name := range []string{"ssb-cis", "ssb-stig"} {
		if _, ok := plan.Scans[name]; !ok {
			t.Errorf("plan.json must contain scan %q after accumulation; present: %v", name, scanKeys(plan.Scans))
		}
	}
	if len(plan.Scans) != 2 {
		t.Errorf("expected 2 accumulated scans, got %d: %v", len(plan.Scans), scanKeys(plan.Scans))
	}
}

func scanKeys(m map[string]PlannedScan) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
