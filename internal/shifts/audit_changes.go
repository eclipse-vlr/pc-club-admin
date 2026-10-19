package shifts

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

type AuditChange struct {
	Field  string  `json:"field"`
	Before *string `json:"before"`
	After  *string `json:"after"`
}

// ProjectAuditChanges exposes only explicitly reviewed fields of known events.
// It never traverses arbitrary maps, tokens, proofs, hashes or nested movements.
func ProjectAuditChanges(action string, raw json.RawMessage) []AuditChange {
	var fields []string
	root := false
	movement := false
	switch action {
	case "user_updated":
		fields = []string{"display_name", "active", "version"}
	case "checklist_answered":
		fields = []string{"answer", "comment", "equipment_issue", "version"}
	case "shift_note_saved":
		fields = []string{"checklist_code", "category", "object_label", "text", "version"}
	case "cash_saved":
		fields = []string{"opening_cash_kopecks", "counted_opening_kopecks", "langame_cash_amount_kopecks", "extra_cash_refunds_kopecks", "extra_cash_expenses_kopecks", "counted_closing_kopecks", "policy_version", "version"}
	case "handover_prepared":
		root = true
		fields = []string{"from_shift_id", "to_shift_id", "revision", "expected_cash_kopecks"}
	case "handover_accepted", "handover_cancelled", "handover_receipt_saved", "handover_notes_acknowledged":
		root = true
		fields = []string{"from_shift_id", "to_shift_id", "revision", "version", "counted_cash_kopecks", "status", "cancellation_reason", "actual_boundary_at"}
	case "cash_collected", "cash_collection_reversed":
		movement = true
		fields = []string{"id", "kind", "amount_kopecks", "reversal_of", "reason", "recipient", "occurred_at", "actor_id"}
	case "first_shift_activated":
		root = true
		fields = []string{"accepted_opening_kopecks", "policy_version"}
	case "shift_bootstrapped":
		root = true
		fields = []string{"owner_id", "opening_cash_kopecks", "reason", "template_version"}
	case "csv_exported":
		root = true
		fields = []string{"cash", "date_from", "date_to", "owner_id", "row_count"}
	default:
		return []AuditChange{}
	}
	var event struct {
		Before map[string]json.RawMessage `json:"before"`
		After  map[string]json.RawMessage `json:"after"`
	}
	if len(raw) > 1<<20 || json.Unmarshal(raw, &event) != nil {
		return []AuditChange{}
	}
	if root || movement {
		var values map[string]json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return []AuditChange{}
		}
		event.Before = map[string]json.RawMessage{}
		if movement {
			if json.Unmarshal(values["movement"], &event.After) != nil {
				return []AuditChange{}
			}
		} else {
			event.After = values
			if before, ok := values["before_counted_cash_kopecks"]; ok {
				event.Before["counted_cash_kopecks"] = before
			}
		}
	}
	isInteger := func(field string) bool {
		return strings.HasSuffix(field, "_kopecks") || field == "version" || field == "revision" || field == "template_version" || field == "row_count"
	}
	isBoolean := func(field string) bool { return field == "active" || field == "equipment_issue" || field == "cash" }
	scalar := func(field string, raw json.RawMessage) *string {
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			return nil
		}
		if len(raw) > 0 && raw[0] == '"' && !isInteger(field) && !isBoolean(field) {
			var value string
			if json.Unmarshal(raw, &value) != nil || len(value) > 16000 {
				return nil
			}
			return &value
		}
		text := string(raw)
		if isBoolean(field) && (text == "true" || text == "false") {
			return &text
		}
		if _, err := strconv.ParseInt(text, 10, 64); err == nil && !strings.ContainsAny(text, ".eE+") && isInteger(field) {
			return &text
		}
		return nil
	}
	result := []AuditChange{}
	for _, field := range fields {
		before, after := scalar(field, event.Before[field]), scalar(field, event.After[field])
		if before == nil && after == nil || before != nil && after != nil && *before == *after {
			continue
		}
		result = append(result, AuditChange{Field: field, Before: before, After: after})
	}
	return result
}
