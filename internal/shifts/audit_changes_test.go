package shifts

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuditChangesAllowlistAndPrecision(t *testing.T) {
	raw := json.RawMessage(`{"before":{"langame_cash_amount_kopecks":null,"pin_hash":"secret"},"after":{"langame_cash_amount_kopecks":9007199254740993,"pin_hash":"secret","movements":[{"secret":"hidden"}],"version":2}}`)
	changes := ProjectAuditChanges("cash_saved", raw)
	body, _ := json.Marshal(changes)
	if len(changes) != 2 || changes[0].Before != nil || *changes[0].After != "9007199254740993" || strings.Contains(string(body), "secret") || strings.Contains(string(body), "movements") {
		t.Fatalf("unsafe or imprecise projection: %s", body)
	}
	if len(ProjectAuditChanges("unknown", raw)) != 0 || len(ProjectAuditChanges("pin_reset", raw)) != 0 {
		t.Fatal("unreviewed event exposed")
	}
	if len(ProjectAuditChanges("user_updated", json.RawMessage(`{"before":{"active":true},"after":{"active":true,"display_name":{"token":"secret"}}}`))) != 0 {
		t.Fatal("unchanged or nested value exposed")
	}
}

func TestAuditHandoverProjectionAndMalformedMoney(t *testing.T) {
	changes := ProjectAuditChanges("handover_receipt_saved", json.RawMessage(`{"before_counted_cash_kopecks":0,"counted_cash_kopecks":1,"status":"pending","reauth_proof":"secret"}`))
	if len(changes) != 2 || *changes[0].Before != "0" || *changes[0].After != "1" {
		t.Fatalf("lost receipt transition: %+v", changes)
	}
	if len(ProjectAuditChanges("cash_saved", json.RawMessage(`{"after":{"counted_closing_kopecks":"secret"}}`))) != 0 {
		t.Fatal("non-numeric money accepted")
	}
	changes = ProjectAuditChanges("cash_collected", json.RawMessage(`{"movement":{"amount_kopecks":1,"reason":"Синтетическая инкассация","token":"secret"}}`))
	body, _ := json.Marshal(changes)
	if len(changes) != 2 || strings.Contains(string(body), "secret") {
		t.Fatal("unsafe movement projection")
	}
}
