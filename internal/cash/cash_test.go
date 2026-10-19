package cash

import (
	"math"
	"testing"
)

func money(v int64) *int64 { return &v }
func has(result Summary, code string) bool {
	for _, b := range result.Blockers {
		if b.Code == code {
			return true
		}
	}
	return false
}
func TestDraftCashPolicy(t *testing.T) {
	s := Session{Opening: 1000000, Langame: money(500000), Refunds: money(0), Expenses: money(0), Counted: money(700000), Policy: PolicyVersion, Movements: []Movement{{ID: "collection-1", Kind: "collection", Amount: 800000}}}
	result := Evaluate(s)
	if len(result.Blockers) != 0 || *result.Expected != 700000 || *result.Difference != 0 || result.Verified {
		t.Fatalf("T09: %+v", result)
	}
	s.Counted = money(699999)
	if !has(Evaluate(s), "CASH_MISMATCH") {
		t.Fatal("T10: one kopeck tolerated")
	}
	s.Counted = nil
	if !has(Evaluate(s), "CASH_INPUT_MISSING") {
		t.Fatal("T08: blank became zero")
	}
	s.Counted = money(0)
	s.Opening = 0
	s.Langame = money(0)
	s.Movements = nil
	if len(Evaluate(s).Blockers) != 0 {
		t.Fatal("explicit zero rejected")
	}
	s.Refunds = nil
	if !has(Evaluate(s), "CASH_INPUT_MISSING") {
		t.Fatal("no explicit refund choice required")
	}
}
func TestNetChangeDoesNotDoubleSubtract(t *testing.T) {
	s := Session{Opening: 100000, Langame: money(90000), Refunds: money(0), Expenses: money(0), Counted: money(190000), Policy: PolicyVersion}
	if *Evaluate(s).Expected != 190000 {
		t.Fatal("T11/T12: money not based solely on cash net change and explicit extras")
	}
	s.Refunds = money(10000)
	s.Counted = money(180000)
	if *Evaluate(s).Expected != 180000 {
		t.Fatal("explicit extra refund missing")
	}
}
func TestMoneyBoundsAndCancellation(t *testing.T) {
	s := Session{Opening: math.MaxInt64, Langame: money(1), Refunds: money(0), Expenses: money(0), Counted: money(0), Policy: PolicyVersion}
	if !has(Evaluate(s), "CASH_OVERFLOW") {
		t.Fatal("T33: addition overflow accepted")
	}
	s.Opening = 0
	s.Langame = money(math.MinInt64)
	s.Refunds = money(1)
	if !has(Evaluate(s), "CASH_OVERFLOW") {
		t.Fatal("subtraction overflow accepted")
	}
	s.Langame = money(-1)
	s.Refunds = money(0)
	if !has(Evaluate(s), "CASH_NEGATIVE_EXPECTED") {
		t.Fatal("negative physical cash accepted")
	}
	id := "first"
	s.Movements = []Movement{{ID: id, Kind: "collection", Amount: 50}, {ID: "reverse", Kind: "reversal", Amount: 50, ReversalOf: &id}}
	total, err := CollectionTotal(s.Movements)
	if err != nil || total != 0 {
		t.Fatal("compensating cancellation incorrect")
	}
	s.Movements = append(s.Movements, Movement{ID: "another-reverse", Kind: "reversal", Amount: 50, ReversalOf: &id})
	if _, err := CollectionTotal(s.Movements); err == nil {
		t.Fatal("T32: double reversal accepted")
	}
	if _, err := CollectionTotal([]Movement{{ID: "one", Kind: "collection", Amount: math.MaxInt64}, {ID: "two", Kind: "collection", Amount: 1}}); err == nil {
		t.Fatal("collection overflow accepted")
	}
}
func TestParseRublesExact(t *testing.T) {
	for _, tt := range []struct {
		text string
		want int64
	}{{"123,45", 12345}, {"123.45", 12345}, {"0", 0}, {"-0.01", -1}, {"92233720368547758.07", math.MaxInt64}, {"-92233720368547758.08", math.MinInt64}} {
		got, err := ParseRubles(tt.text)
		if err != nil || got != tt.want {
			t.Fatalf("%q: got %d err %v", tt.text, got, err)
		}
	}
	for _, text := range []string{"", " ", "1.001", "1,001", "1e3", "+1", ".01", "1.", "1.2.3", "92233720368547758.08", "-92233720368547758.09"} {
		if _, err := ParseRubles(text); err == nil {
			t.Fatalf("T33: accepted %q", text)
		}
	}
}
