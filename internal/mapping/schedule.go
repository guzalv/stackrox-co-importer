package mapping

// IMP-MAP-003, IMP-MAP-004, IMP-MAP-012, IMP-MAP-013, IMP-MAP-014, IMP-MAP-015

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/stackrox/co-importer/internal/models"
)

// ConvertCronToACSSchedule converts a standard 5-field cron expression to an
// ACS Schedule object.
//
// Supported cases:
//
//	"minute hour * * *"          -> DAILY, hour=H, minute=M
//	"minute hour * * dayOfWeek"  -> WEEKLY, hour=H, minute=M, day=DOW
//	"minute hour dayOfMonth * *" -> MONTHLY, hour=H, minute=M, days=[DOM]
//
// Returns an error for unsupported or invalid cron expressions.
func ConvertCronToACSSchedule(cron string) (*models.ACSSchedule, error) {
	cron = strings.TrimSpace(cron)
	if cron == "" {
		return nil, errors.New("cron expression is empty; provide a valid 5-field cron expression (e.g. \"0 2 * * *\" for daily at 02:00)")
	}

	fields := strings.Fields(cron)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression %q has %d field(s); a standard cron requires exactly 5 fields: minute hour day-of-month month day-of-week", cron, len(fields))
	}

	minute, hour, dom, month, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

	// Reject unsupported syntax in any field.
	for _, f := range fields {
		if strings.Contains(f, "/") {
			return nil, fmt.Errorf("step notation %q is not supported; use a simple numeric cron expression (e.g. \"0 2 * * *\")", f)
		}
		if strings.Contains(f, "-") {
			return nil, fmt.Errorf("range notation %q is not supported; use a simple numeric cron expression (e.g. \"0 2 * * *\")", f)
		}
	}

	// Month must always be wildcard.
	if month != "*" {
		return nil, fmt.Errorf("specific month field %q is not supported; set month to \"*\" and use day-of-month or day-of-week instead", month)
	}

	// Parse minute.
	minVal, err := parseField(minute, "minute", 0, 59)
	if err != nil {
		return nil, err
	}

	// Parse hour.
	hourVal, err := parseField(hour, "hour", 0, 23)
	if err != nil {
		return nil, err
	}

	domIsWild := dom == "*"
	dowIsWild := dow == "*"

	switch {
	case !domIsWild && !dowIsWild:
		return nil, fmt.Errorf("cron expression %q sets both day-of-month (%s) and day-of-week (%s), which is ambiguous; set exactly one to \"*\"", cron, dom, dow)

	case domIsWild && dowIsWild:
		// DAILY
		return &models.ACSSchedule{
			IntervalType: "DAILY",
			Hour:         hourVal,
			Minute:       minVal,
		}, nil

	case domIsWild && !dowIsWild:
		// WEEKLY
		dowVal, err := parseField(dow, "day-of-week", 0, 6)
		if err != nil {
			return nil, err
		}
		return &models.ACSSchedule{
			IntervalType: "WEEKLY",
			Hour:         hourVal,
			Minute:       minVal,
			DaysOfWeek:   &models.ACSDaysOfWeek{Days: []int32{dowVal}},
		}, nil

	default:
		// MONTHLY
		domVal, err := parseField(dom, "day-of-month", 1, 31)
		if err != nil {
			return nil, err
		}
		return &models.ACSSchedule{
			IntervalType: "MONTHLY",
			Hour:         hourVal,
			Minute:       minVal,
			DaysOfMonth:  &models.ACSDaysOfMonth{Days: []int32{domVal}},
		}, nil
	}
}

func parseField(val, name string, lo, hi int) (int32, error) {
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("cron field %q (value %q) is not a valid integer; use a plain number or \"*\" for %s", name, val, name)
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("cron field %q value %d is out of range [%d, %d]", name, n, lo, hi)
	}
	return int32(n), nil
}
