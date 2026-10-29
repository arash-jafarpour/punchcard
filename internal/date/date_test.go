package date

import (
	"testing"
	"time"
)

func tehran(t *testing.T) *time.Location {
	t.Helper()
	loc, err := ParseTimezone(DefaultTimezone)
	if err != nil {
		t.Fatalf("load %s: %v", DefaultTimezone, err)
	}
	return loc
}

func TestParseDay(t *testing.T) {
	loc := tehran(t)
	tests := []struct {
		name       string
		day        string
		wantStart  string
		wantAfter  string
		wantBefore string
		wantEnd    string
	}{
		{
			name:       "normal day",
			day:        "2026-10-27",
			wantStart:  "2026-10-27T00:00:00+03:30",
			wantAfter:  "2026-10-26",
			wantBefore: "2026-10-28",
			wantEnd:    "2026-10-28T00:00:00+03:30",
		},
		{
			name:       "month boundary",
			day:        "2026-11-01",
			wantStart:  "2026-11-01T00:00:00+03:30",
			wantAfter:  "2026-10-31",
			wantBefore: "2026-11-02",
			wantEnd:    "2026-11-02T00:00:00+03:30",
		},
		{
			name:       "year boundary",
			day:        "2027-01-01",
			wantStart:  "2027-01-01T00:00:00+03:30",
			wantAfter:  "2026-12-31",
			wantBefore: "2027-01-02",
			wantEnd:    "2027-01-02T00:00:00+03:30",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rng, err := ParseDay(test.day, loc)
			if err != nil {
				t.Fatalf("ParseDay(%q): %v", test.day, err)
			}
			if got := rng.Start.Format(time.RFC3339); got != test.wantStart {
				t.Errorf("start = %s, want %s", got, test.wantStart)
			}
			if got := rng.End.Format(time.RFC3339); got != test.wantEnd {
				t.Errorf("end = %s, want %s", got, test.wantEnd)
			}
			if got := rng.EventsAfter(); got != test.wantAfter {
				t.Errorf("after = %s, want %s", got, test.wantAfter)
			}
			if got := rng.EventsBefore(); got != test.wantBefore {
				t.Errorf("before = %s, want %s", got, test.wantBefore)
			}
			if got := rng.Date(); got != test.day {
				t.Errorf("date = %s, want %s", got, test.day)
			}
			if got := rng.Timezone; got != DefaultTimezone {
				t.Errorf("timezone = %s, want %s", got, DefaultTimezone)
			}
		})
	}
}

func TestYesterday(t *testing.T) {
	loc := tehran(t)
	tests := []struct {
		name string
		now  string
		want string
	}{
		{
			name: "afternoon",
			now:  "2026-10-27T14:00:00+03:30",
			want: "2026-10-26",
		},
		{
			name: "just after midnight",
			now:  "2026-10-27T00:10:00+03:30",
			want: "2026-10-26",
		},
		{
			name: "one second before midnight",
			now:  "2026-10-27T23:59:59+03:30",
			want: "2026-10-26",
		},
		{
			name: "utc input converted to tehran",
			now:  "2026-10-27T21:30:00Z", // 2026-10-28T01:00 in Tehran
			want: "2026-10-27",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, test.now)
			if err != nil {
				t.Fatal(err)
			}
			rng := Yesterday(now, loc)
			if got := rng.Date(); got != test.want {
				t.Errorf("yesterday(%s) = %s, want %s", test.now, got, test.want)
			}
			if rng.Start.Hour() != 0 || rng.Start.Minute() != 0 || rng.Start.Second() != 0 {
				t.Errorf("start is not midnight: %s", rng.Start)
			}
			if rng.Location != loc {
				t.Errorf("range location = %v, want %v", rng.Location, loc)
			}
		})
	}
}

func TestLastDays(t *testing.T) {
	loc := tehran(t)
	tests := []struct {
		name     string
		now      string
		n        int
		want     []string
		wantEnd  string // end of the last window, as a timestamp
		lastFull bool
	}{
		{
			name:    "one day ends now",
			now:     "2026-10-29T15:00:00+03:30",
			n:       1,
			want:    []string{"2026-10-29"},
			wantEnd: "2026-10-29T15:00:00+03:30",
		},
		{
			name: "seven days",
			now:  "2026-10-29T15:00:00+03:30",
			n:    7,
			want: []string{
				"2026-10-23", "2026-10-24", "2026-10-25", "2026-10-26",
				"2026-10-27", "2026-10-28", "2026-10-29",
			},
			wantEnd: "2026-10-29T15:00:00+03:30",
		},
		{
			name: "seven days across a month boundary",
			now:  "2026-11-02T09:00:00+03:30",
			n:    7,
			want: []string{
				"2026-10-27", "2026-10-28", "2026-10-29", "2026-10-30",
				"2026-10-31", "2026-11-01", "2026-11-02",
			},
			wantEnd: "2026-11-02T09:00:00+03:30",
		},
		{
			name: "seven days across a year boundary",
			now:  "2027-01-02T10:00:00+03:30",
			n:    7,
			want: []string{
				"2026-12-27", "2026-12-28", "2026-12-29", "2026-12-30",
				"2026-12-31", "2027-01-01", "2027-01-02",
			},
			wantEnd: "2027-01-02T10:00:00+03:30",
		},
		{
			name:    "utc input converted to tehran",
			now:     "2026-10-29T22:30:00Z", // 2026-10-30T02:00 in Tehran
			n:       2,
			want:    []string{"2026-10-29", "2026-10-30"},
			wantEnd: "2026-10-30T02:00:00+03:30",
		},
		{
			name:    "just after midnight",
			now:     "2026-10-30T00:10:00+03:30",
			n:       2,
			want:    []string{"2026-10-29", "2026-10-30"},
			wantEnd: "2026-10-30T00:10:00+03:30",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := mustParse(t, test.now)
			days := LastDays(now, loc, test.n)
			if len(days) != len(test.want) {
				t.Fatalf("days = %d, want %d", len(days), len(test.want))
			}
			for i, rng := range days {
				if got := rng.Date(); got != test.want[i] {
					t.Errorf("day[%d] = %s, want %s", i, got, test.want[i])
				}
				if rng.Start.Hour() != 0 || rng.Start.Minute() != 0 || rng.Start.Second() != 0 {
					t.Errorf("day[%d] does not start at midnight: %s", i, rng.Start)
				}
				if rng.Location != loc || rng.Timezone != DefaultTimezone {
					t.Errorf("day[%d] location = %v/%s", i, rng.Location, rng.Timezone)
				}
			}
			// Every window but the partial today must end at the next
			// midnight and meet the following window exactly.
			for i := 0; i < len(days)-1; i++ {
				if !days[i].End.Equal(days[i+1].Start) {
					t.Errorf("day[%d] ends at %s, next starts at %s",
						i, days[i].End, days[i+1].Start)
				}
				if days[i].EventsBefore() != days[i+1].Date() {
					t.Errorf("day[%d] events before = %s, want %s",
						i, days[i].EventsBefore(), days[i+1].Date())
				}
			}
			last := days[len(days)-1]
			if got := last.End.Format(time.RFC3339); got != test.wantEnd {
				t.Errorf("last end = %s, want %s", got, test.wantEnd)
			}
			// The partial today still asks /events for the whole day.
			wantBefore := last.Start.AddDate(0, 0, 1).Format(Layout)
			if got := last.EventsBefore(); got != wantBefore {
				t.Errorf("last events before = %s, want %s", got, wantBefore)
			}
			if !last.Contains(now) && !now.Equal(last.End) {
				t.Errorf("now %s is not in the last window", now)
			}
		})
	}
}

func TestLastDaysRejectsNonPositiveCount(t *testing.T) {
	loc := tehran(t)
	for _, n := range []int{0, -1} {
		if days := LastDays(mustParse(t, "2026-10-29T15:00:00+03:30"), loc, n); days != nil {
			t.Errorf("LastDays(n=%d) = %v, want nil", n, days)
		}
	}
}

func TestWindowHelpers(t *testing.T) {
	loc := tehran(t)
	now := mustParse(t, "2026-10-29T15:00:00+03:30")

	one := LastDays(now, loc, 1)
	if got := WindowStem(one); got != "2026-10-29" {
		t.Errorf("one-day stem = %q", got)
	}
	if got := WindowLabel(one); got != "2026-10-29" {
		t.Errorf("one-day label = %q", got)
	}
	if got := WindowEnd(one); got != "2026-10-29" {
		t.Errorf("one-day end = %q", got)
	}

	week := LastDays(now, loc, 7)
	if got := WindowStem(week); got != "2026-10-23_2026-10-29" {
		t.Errorf("week stem = %q", got)
	}
	if got := WindowLabel(week); got != "2026-10-23 → 2026-10-29" {
		t.Errorf("week label = %q", got)
	}
	if got := WindowEnd(week); got != "2026-10-29" {
		t.Errorf("week end = %q", got)
	}

	year := LastDays(mustParse(t, "2027-01-02T10:00:00+03:30"), loc, 7)
	if got := WindowStem(year); got != "2026-12-27_2027-01-02" {
		t.Errorf("year-boundary stem = %q", got)
	}
	if got := WindowLabel(year); got != "2026-12-27 → 2027-01-02" {
		t.Errorf("year-boundary label = %q", got)
	}

	if got := WindowStem(nil); got != "" {
		t.Errorf("empty stem = %q", got)
	}
	if got := WindowLabel(nil); got != "" {
		t.Errorf("empty label = %q", got)
	}
	if got := WindowEnd(nil); got != "" {
		t.Errorf("empty end = %q", got)
	}
}

func TestContainsAndClock(t *testing.T) {
	loc := tehran(t)
	rng, err := ParseDay("2026-10-27", loc)
	if err != nil {
		t.Fatal(err)
	}

	inside := mustParse(t, "2026-10-27T15:55:00+03:30")
	before := mustParse(t, "2026-10-27T00:00:00+03:30").Add(-time.Second)
	atEnd := mustParse(t, "2026-10-28T00:00:00+03:30")

	if !rng.Contains(inside) {
		t.Error("inside timestamp not contained")
	}
	if rng.Contains(before) {
		t.Error("timestamp before start contained")
	}
	if rng.Contains(atEnd) {
		t.Error("end timestamp must be exclusive")
	}
	if got := rng.Clock(inside); got != "15:55" {
		t.Errorf("clock = %s, want 15:55", got)
	}
}

func TestParseDayRejectsGarbage(t *testing.T) {
	loc := tehran(t)
	for _, value := range []string{"", "2026-13-01", "27-10-2026", "yesterday"} {
		if _, err := ParseDay(value, loc); err == nil {
			t.Errorf("ParseDay(%q) succeeded, want an error", value)
		}
	}
}

func TestParseTimezoneRejectsUnknown(t *testing.T) {
	if _, err := ParseTimezone("Mars/Olympus"); err == nil {
		t.Error("expected an error for an unknown timezone")
	}
}

func mustParse(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}
