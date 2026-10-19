package shifts

import (
	"testing"
)

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
