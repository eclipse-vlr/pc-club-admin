package checklists

import "testing"

func TestRequiredAnswerSemantics(t *testing.T) {
	entries := []Entry{}
	for i := range 12 {
		entries = append(entries, Entry{Code: "item", Phase: "acceptance", Required: true, Allowed: []string{"yes"}, Answer: "yes", Order: i})
	}
	entries[11].Answer = "unanswered"
	result := Evaluate(entries, nil, "acceptance")
	if result.Completed != 11 || result.Total != 12 || len(result.Blockers) != 1 || result.Blockers[0].Code != "CHECKLIST_INCOMPLETE" {
		t.Fatalf("T01: %+v", result)
	}
	entries[11].Answer = "no"
	result = Evaluate(entries, nil, "acceptance")
	if result.Completed != 11 || result.Blockers[0].Code != "CHECKLIST_ANSWER_BLOCKS" {
		t.Fatalf("T02: %+v", result)
	}
	entries[11].Allowed = []string{"yes", "no"}
	if Evaluate(entries, nil, "acceptance").Completed != 11 {
		t.Fatal("ordinary no became allowed through config")
	}
}
func TestConflictExceptionAndNotes(t *testing.T) {
	entry := Entry{Code: "conflicts_recorded", Phase: "closing", Label: "Конфликты", Allowed: []string{"yes", "no"}, Required: true}
	for _, tt := range []struct {
		answer    string
		notes     []Note
		completed int
		blocker   string
	}{
		{"no", nil, 1, ""},
		{"unanswered", nil, 0, "CHECKLIST_INCOMPLETE"},
		{"yes", nil, 1, "NOTE_REQUIRED"},
		{"yes", []Note{{Code: "conflicts_recorded", Category: "conflict", Text: "Синтетическая ситуация"}}, 1, ""},
		{"yes", []Note{{Code: "another", Category: "conflict", Text: "Чужой пункт"}}, 1, "NOTE_REQUIRED"},
	} {
		entry.Answer = tt.answer
		p := Evaluate([]Entry{entry}, tt.notes, "closing")
		if p.Completed != tt.completed {
			t.Fatalf("%s: %+v", tt.answer, p)
		}
		if tt.blocker == "" && len(p.Blockers) != 0 {
			t.Fatalf("unexpected blocker %+v", p)
		}
		if tt.blocker != "" && (len(p.Blockers) != 1 || p.Blockers[0].Code != tt.blocker) {
			t.Fatalf("missing blocker %+v", p)
		}
	}
}
func TestEquipmentDoesNotRequireRepair(t *testing.T) {
	entry := Entry{Code: "accept_i", Phase: "acceptance", Required: true, Allowed: []string{"yes"}, Answer: "yes", EquipmentCheck: true, EquipmentIssue: true}
	if Evaluate([]Entry{entry}, nil, "acceptance").Blockers[0].Code != "NOTE_REQUIRED" {
		t.Fatal("missing equipment note accepted")
	}
	notes := []Note{{Code: "accept_i", Category: "equipment", Object: "ПК-01", Text: "Не включается; ремонт ещё не выполнен"}}
	result := Evaluate([]Entry{entry}, notes, "acceptance")
	if len(result.Blockers) != 0 {
		t.Fatalf("T06: repair incorrectly required: %+v", result)
	}
}
func TestEmptyTemplateAndUnknownAnswer(t *testing.T) {
	if Evaluate(nil, nil, "acceptance").Blockers[0].Code != "CHECKLIST_TEMPLATE_INVALID" {
		t.Fatal("empty template accepted")
	}
	for _, answer := range []string{"—", "", "na", "YES"} {
		if ValidAnswer(answer) {
			t.Fatalf("T07: accepted %q", answer)
		}
	}
}
