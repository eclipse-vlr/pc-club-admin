// Package checklists owns answer semantics without HTTP or database dependencies.
package checklists

import (
	"slices"
	"strings"
	"time"
)

type Entry struct {
	Code           string     `json:"code"`
	Phase          string     `json:"phase"`
	Label          string     `json:"label"`
	Help           string     `json:"help_text"`
	Allowed        []string   `json:"allowed_completion_answers"`
	Required       bool       `json:"required"`
	Order          int        `json:"sort_order"`
	EquipmentCheck bool       `json:"equipment_check"`
	Answer         string     `json:"answer"`
	Comment        string     `json:"comment"`
	EquipmentIssue bool       `json:"equipment_issue"`
	AnsweredBy     *string    `json:"answered_by"`
	AnsweredAt     *time.Time `json:"answered_at"`
	Version        int64      `json:"version"`
}
type Note struct {
	ID        string    `json:"id"`
	Code      string    `json:"checklist_code"`
	Category  string    `json:"category"`
	Object    string    `json:"object_label"`
	Text      string    `json:"text"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int64     `json:"version"`
}
type Blocker struct {
	Code    string `json:"code"`
	Item    string `json:"item_code,omitempty"`
	Message string `json:"message"`
}
type Progress struct {
	Completed int       `json:"completed"`
	Total     int       `json:"total"`
	Blockers  []Blocker `json:"blockers"`
}

func ValidAnswer(answer string) bool {
	return answer == "unanswered" || answer == "yes" || answer == "no"
}
func Evaluate(entries []Entry, notes []Note, phase string) Progress {
	result := Progress{Blockers: []Blocker{}}
	for _, e := range entries {
		if e.Phase != phase || !e.Required {
			continue
		}
		result.Total++
		if !ValidAnswer(e.Answer) || len(e.Allowed) == 0 {
			result.Blockers = append(result.Blockers, Blocker{"CHECKLIST_TEMPLATE_INVALID", e.Code, "Ошибка настройки пункта: " + e.Label})
			continue
		}
		if e.Answer == "unanswered" {
			result.Blockers = append(result.Blockers, Blocker{"CHECKLIST_INCOMPLETE", e.Code, "Не заполнено: " + e.Label})
			continue
		}
		// The only permitted exception is conflicts_recorded=no. Even corrupted
		// allowed_answers must not silently allow ordinary negative answers.
		acceptable := e.Answer == "yes" && slices.Contains(e.Allowed, "yes") || e.Code == "conflicts_recorded" && e.Answer == "no" && slices.Contains(e.Allowed, "no")
		if !acceptable {
			result.Blockers = append(result.Blockers, Blocker{"CHECKLIST_ANSWER_BLOCKS", e.Code, "Ответ блокирует завершение: " + e.Label})
			continue
		}
		result.Completed++
		category := ""
		if e.Code == "conflicts_recorded" && e.Answer == "yes" {
			category = "conflict"
		}
		if e.EquipmentCheck && e.EquipmentIssue {
			category = "equipment"
		}
		if category != "" {
			found := false
			for _, n := range notes {
				if n.Code == e.Code && n.Category == category && strings.TrimSpace(n.Text) != "" && (category != "equipment" || strings.TrimSpace(n.Object) != "") {
					found = true
					break
				}
			}
			if !found {
				result.Blockers = append(result.Blockers, Blocker{"NOTE_REQUIRED", e.Code, "Запишите замечание: " + e.Label})
			}
		}
	}
	if result.Total == 0 {
		result.Blockers = append(result.Blockers, Blocker{"CHECKLIST_TEMPLATE_INVALID", "", "Шаблон этапа пуст"})
	}
	return result
}
