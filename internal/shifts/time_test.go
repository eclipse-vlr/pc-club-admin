package shifts

import (
	"testing"
	"time"
)

func TestPlannedBoundsCalendarEdges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, date, kind, zone, start, end string
	}{
		{"day", "2026-10-21", "day", "Europe/Moscow", "2026-10-21T09:00:00+03:00", "2026-10-21T21:00:00+03:00"},
		{"month_end", "2026-10-31", "night", "Europe/Moscow", "2026-10-31T21:00:00+03:00", "2026-11-01T09:00:00+03:00"},
		{"year_end", "2026-12-31", "night", "Europe/Moscow", "2026-12-31T21:00:00+03:00", "2027-01-01T09:00:00+03:00"},
		{"leap_day", "2028-02-29", "night", "Europe/Moscow", "2028-02-29T21:00:00+03:00", "2028-03-01T09:00:00+03:00"},
		{"club_timezone", "2026-10-21", "night", "Asia/Vladivostok", "2026-10-21T21:00:00+10:00", "2026-10-22T09:00:00+10:00"},
		{"spring_clock_change", "2026-03-28", "night", "Europe/Berlin", "2026-03-28T21:00:00+01:00", "2026-03-29T09:00:00+02:00"},
		{"autumn_clock_change", "2026-10-24", "night", "Europe/Berlin", "2026-10-24T21:00:00+02:00", "2026-10-25T09:00:00+01:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			start, end, err := PlannedBounds(tc.date, tc.kind, tc.zone)
			if err != nil {
				t.Fatalf("PlannedBounds(%q, %q, %q) error = %v, want nil", tc.date, tc.kind, tc.zone, err)
			}
			if got := start.Format(time.RFC3339); got != tc.start {
				t.Errorf("PlannedBounds(%q, %q, %q) start = %s, want %s", tc.date, tc.kind, tc.zone, got, tc.start)
			}
			if got := end.Format(time.RFC3339); got != tc.end {
				t.Errorf("PlannedBounds(%q, %q, %q) end = %s, want %s", tc.date, tc.kind, tc.zone, got, tc.end)
			}
			nextKind, nextDate := "night", tc.date
			if tc.kind == "night" {
				nextKind, nextDate = "day", end.Format("2006-01-02")
			}
			next, _, err := PlannedBounds(nextDate, nextKind, tc.zone)
			if err != nil || !end.Equal(next) {
				t.Errorf("PlannedBounds(%q, %q, %q) next start = %v, error %v, want %v", nextDate, nextKind, tc.zone, next, err, end)
			}
		})
	}
}

func TestNightBusinessDate(t *testing.T) {
	start, end, err := PlannedBounds("2026-10-19", "night", "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	if start.Format("2006-01-02T15:04:05Z07:00") != "2026-10-19T21:00:00+03:00" || end.Format("2006-01-02T15:04:05Z07:00") != "2026-10-20T09:00:00+03:00" {
		t.Fatalf("wrong bounds: %v %v", start, end)
	}
	if _, _, err := PlannedBounds("2026-02-30", "day", "Europe/Moscow"); err == nil {
		t.Fatal("invalid date accepted")
	}
}
