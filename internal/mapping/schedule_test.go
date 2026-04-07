package mapping

import (
	"testing"
)

// IMP-MAP-003, IMP-MAP-004, IMP-MAP-012..015
func TestConvertCronToACSSchedule(t *testing.T) {
	tests := []struct {
		name     string
		cron     string
		wantErr  bool
		wantType string
		wantHour int32
		wantMin  int32
		wantDOW  []int32
		wantDOM  []int32
	}{
		// ── happy paths ───────────────────────────────────────────────────────
		{
			name: "daily midnight",
			cron: "0 0 * * *", wantType: "DAILY", wantHour: 0, wantMin: 0,
		},
		{
			name: "daily at 02:30",
			cron: "30 2 * * *", wantType: "DAILY", wantHour: 2, wantMin: 30,
		},
		{
			name: "daily last-minute-of-hour",
			cron: "59 23 * * *", wantType: "DAILY", wantHour: 23, wantMin: 59,
		},
		{
			name: "weekly sunday",
			cron: "0 3 * * 0", wantType: "WEEKLY", wantHour: 3, wantMin: 0, wantDOW: []int32{0},
		},
		{
			name: "weekly saturday",
			cron: "15 6 * * 6", wantType: "WEEKLY", wantHour: 6, wantMin: 15, wantDOW: []int32{6},
		},
		{
			name: "monthly first",
			cron: "0 1 1 * *", wantType: "MONTHLY", wantHour: 1, wantMin: 0, wantDOM: []int32{1},
		},
		{
			name: "monthly 31st",
			cron: "59 23 31 * *", wantType: "MONTHLY", wantHour: 23, wantMin: 59, wantDOM: []int32{31},
		},
		{
			name: "leading/trailing whitespace stripped",
			cron: "  0 2 * * *  ", wantType: "DAILY", wantHour: 2, wantMin: 0,
		},

		// ── error: structural ─────────────────────────────────────────────────
		{name: "empty string", cron: "", wantErr: true},
		{name: "only whitespace", cron: "   ", wantErr: true},
		{name: "too few fields", cron: "0 2 *", wantErr: true},
		{name: "too many fields", cron: "0 2 * * * *", wantErr: true},

		// ── error: unsupported syntax ─────────────────────────────────────────
		{name: "step in minute", cron: "*/5 2 * * *", wantErr: true},
		{name: "step in hour", cron: "0 */2 * * *", wantErr: true},
		{name: "range in minute", cron: "0-30 2 * * *", wantErr: true},
		{name: "range in hour", cron: "0 8-18 * * *", wantErr: true},

		// ── error: month must be wildcard ─────────────────────────────────────
		{name: "specific month", cron: "0 2 * 3 *", wantErr: true},
		{name: "specific month with dom", cron: "0 2 1 6 *", wantErr: true},

		// ── error: ambiguous dom+dow ──────────────────────────────────────────
		{name: "both dom and dow set", cron: "0 2 1 * 0", wantErr: true},

		// ── error: out of range ───────────────────────────────────────────────
		{name: "minute 60", cron: "60 2 * * *", wantErr: true},
		{name: "hour 24", cron: "0 24 * * *", wantErr: true},
		{name: "dom 0 (below min)", cron: "0 2 0 * *", wantErr: true},
		{name: "dom 32 (above max)", cron: "0 2 32 * *", wantErr: true},
		{name: "dow 7 (above max)", cron: "0 2 * * 7", wantErr: true},

		// ── error: non-numeric fields ─────────────────────────────────────────
		{name: "non-numeric minute", cron: "abc 2 * * *", wantErr: true},
		{name: "non-numeric hour", cron: "0 foo * * *", wantErr: true},
		{name: "non-numeric dom", cron: "0 2 bar * *", wantErr: true},
		{name: "non-numeric dow", cron: "0 2 * * baz", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ConvertCronToACSSchedule(tc.cron)

			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (result: %+v)", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("got nil schedule, want non-nil")
			}
			if got.IntervalType != tc.wantType {
				t.Errorf("IntervalType: got %q, want %q", got.IntervalType, tc.wantType)
			}
			if got.Hour != tc.wantHour {
				t.Errorf("Hour: got %d, want %d", got.Hour, tc.wantHour)
			}
			if got.Minute != tc.wantMin {
				t.Errorf("Minute: got %d, want %d", got.Minute, tc.wantMin)
			}

			if tc.wantDOW != nil {
				if got.DaysOfWeek == nil {
					t.Fatal("DaysOfWeek is nil, want non-nil")
				}
				if len(got.DaysOfWeek.Days) != len(tc.wantDOW) || got.DaysOfWeek.Days[0] != tc.wantDOW[0] {
					t.Errorf("DaysOfWeek.Days: got %v, want %v", got.DaysOfWeek.Days, tc.wantDOW)
				}
			} else if got.DaysOfWeek != nil {
				t.Errorf("DaysOfWeek: got %v, want nil", got.DaysOfWeek)
			}

			if tc.wantDOM != nil {
				if got.DaysOfMonth == nil {
					t.Fatal("DaysOfMonth is nil, want non-nil")
				}
				if len(got.DaysOfMonth.Days) != len(tc.wantDOM) || got.DaysOfMonth.Days[0] != tc.wantDOM[0] {
					t.Errorf("DaysOfMonth.Days: got %v, want %v", got.DaysOfMonth.Days, tc.wantDOM)
				}
			} else if got.DaysOfMonth != nil {
				t.Errorf("DaysOfMonth: got %v, want nil", got.DaysOfMonth)
			}
		})
	}
}
