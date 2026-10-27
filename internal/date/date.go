// Package date owns every day-boundary decision in the tool. Day boundaries
// are always computed in the configured report timezone, never in the host
// timezone, so a laptop and a VPS produce identical reports.
package date

import (
	"fmt"
	"time"

	// Embed the IANA database so a minimal VPS without system tzdata can still
	// resolve Asia/Tehran.
	_ "time/tzdata"
)

// DefaultTimezone is the report timezone used when REPORT_TIMEZONE is empty.
const DefaultTimezone = "Asia/Tehran"

// Layout is the date-only layout used by --date and by GitLab's /events
// after/before parameters.
const Layout = "2006-01-02"

// Range is a half-open day window [Start, End) in Location.
type Range struct {
	Location *time.Location
	Timezone string
	Start    time.Time
	End      time.Time
}

// ParseTimezone resolves an IANA timezone name.
func ParseTimezone(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid REPORT_TIMEZONE %q: %w", name, err)
	}
	return loc, nil
}

// NewRange returns the day window that contains day, in loc.
func NewRange(loc *time.Location, day time.Time) Range {
	y, m, d := day.In(loc).Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, loc)
	return Range{
		Location: loc,
		Timezone: loc.String(),
		Start:    start,
		End:      start.AddDate(0, 0, 1),
	}
}

// ParseDay parses a YYYY-MM-DD report date in loc.
func ParseDay(value string, loc *time.Location) (Range, error) {
	day, err := time.ParseInLocation(Layout, value, loc)
	if err != nil {
		return Range{}, fmt.Errorf("invalid date %q: want YYYY-MM-DD", value)
	}
	return NewRange(loc, day), nil
}

// Yesterday returns the range for the calendar day before now, in loc.
func Yesterday(now time.Time, loc *time.Location) Range {
	return NewRange(loc, now.In(loc).AddDate(0, 0, -1))
}

// Date is the report day as YYYY-MM-DD.
func (r Range) Date() string { return r.Start.Format(Layout) }

// EventsAfter is GitLab's exclusive `after` value for /events. The API treats
// the bounds as date-only and exclusive, so a full day needs one day of slack
// on each side.
func (r Range) EventsAfter() string { return r.Start.AddDate(0, 0, -1).Format(Layout) }

// EventsBefore is GitLab's exclusive `before` value for /events.
func (r Range) EventsBefore() string { return r.End.Format(Layout) }

// StartTimestamp is an RFC3339 timestamp with the report offset, used for
// updated_after / since parameters.
func (r Range) StartTimestamp() string { return r.Start.Format(time.RFC3339) }

// EndTimestamp is an RFC3339 timestamp with the report offset, used for
// updated_before / until parameters.
func (r Range) EndTimestamp() string { return r.End.Format(time.RFC3339) }

// Contains reports whether t falls inside the day.
func (r Range) Contains(t time.Time) bool {
	return !t.Before(r.Start) && t.Before(r.End)
}

// Clock formats t as HH:MM in the report timezone.
func (r Range) Clock(t time.Time) string { return t.In(r.Location).Format("15:04") }
