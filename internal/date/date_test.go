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
