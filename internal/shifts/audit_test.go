package shifts

import (
	"testing"
	"time"
)

func TestAuditFilterCursorAndBounds(t *testing.T) {
	now := time.Now()
	for _, f := range []AuditFilter{
		{Cursor: "0"}, {Cursor: "-1"}, {Cursor: "01"}, {Cursor: "+1"}, {Cursor: "9223372036854775808"},
		{ActorID: "invalid"}, {Limit: 101}, {Action: "bad\n"}, {EntityID: "bad\x00"}, {From: &now, To: &now},
	} {
		if _, err := validateAuditFilter(f); err == nil {
			t.Errorf("accepted invalid filter: %+v", f)
		}
	}
	f, err := validateAuditFilter(AuditFilter{Cursor: "9223372036854775807", BeforeID: 1})
	if err != nil || f.BeforeID != 9223372036854775807 || f.Limit != 50 {
		t.Fatalf("lost cursor precision: %+v %v", f, err)
	}
	f, err = validateAuditFilter(AuditFilter{BeforeID: 123})
	if err != nil || f.BeforeID != 0 {
		t.Fatalf("trusted unvalidated internal cursor: %+v %v", f, err)
	}
}
