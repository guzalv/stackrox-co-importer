// Package models defines domain types for the CO-to-ACS importer.
package models

// ProfileRef represents a reference to a Compliance Operator profile.
type ProfileRef struct {
	Name string
	Kind string // "Profile" or "TailoredProfile"; empty defaults to "Profile" (IMP-MAP-002)
}

// ResolvedKind returns the kind, defaulting to "Profile" when empty (IMP-MAP-002).
func (r ProfileRef) ResolvedKind() string {
	if r.Kind == "" {
		return "Profile"
	}
	return r.Kind
}

// ScanSettingBinding is a simplified representation of the CO ScanSettingBinding resource.
type ScanSettingBinding struct {
	Namespace       string
	Name            string
	ScanSettingName string       // name of the referenced ScanSetting
	Profiles        []ProfileRef // profile references from the binding
}

// ScanSetting is a simplified representation of the CO ScanSetting resource.
type ScanSetting struct {
	Namespace string
	Name      string
	Schedule  string // cron expression
}

// ACSSchedule is the schedule portion of an ACS scan configuration.
// Fields map to the v2.Schedule proto message in proto/api/v2/common.proto.
type ACSSchedule struct {
	IntervalType string          `json:"intervalType,omitempty"`
	Hour         int32           `json:"hour"`
	Minute       int32           `json:"minute"`
	DaysOfWeek   *ACSDaysOfWeek  `json:"daysOfWeek,omitempty"`
	DaysOfMonth  *ACSDaysOfMonth `json:"daysOfMonth,omitempty"`
}

// ACSDaysOfWeek holds days for a weekly ACS schedule (Sunday=0 .. Saturday=6).
type ACSDaysOfWeek struct {
	Days []int32 `json:"days"`
}

// ACSDaysOfMonth holds days for a monthly ACS schedule.
type ACSDaysOfMonth struct {
	Days []int32 `json:"days"`
}

// ACSBaseScanConfig is the scanConfig sub-object in an ACS create payload.
type ACSBaseScanConfig struct {
	OneTimeScan  bool         `json:"oneTimeScan"`
	Profiles     []string     `json:"profiles"`
	ScanSchedule *ACSSchedule `json:"scanSchedule,omitempty"`
	Description  string       `json:"description"`
}

// ACSPayload is the request body for POST /v2/compliance/scan/configurations.
type ACSPayload struct {
	ScanName   string            `json:"scanName"`
	ScanConfig ACSBaseScanConfig `json:"scanConfig"`
	Clusters   []string          `json:"clusters"`
}
