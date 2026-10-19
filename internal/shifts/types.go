package shifts

import (
	"context"
	"encoding/json"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/checklists"
	"time"
)

type Shift struct {
	ID              string             `json:"id"`
	OwnerID         string             `json:"owner_id"`
	BusinessDate    string             `json:"business_date"`
	Kind            string             `json:"kind"`
	Status          string             `json:"status"`
	PlannedStart    time.Time          `json:"planned_start_at"`
	PlannedEnd      time.Time          `json:"planned_end_at"`
	ActualStart     *time.Time         `json:"actual_start_at"`
	ActualEnd       *time.Time         `json:"actual_end_at"`
	TemplateVersion int                `json:"template_version"`
	Version         int64              `json:"version"`
	InitialCash     *int64             `json:"initial_cash_kopecks"`
	BootstrapReason string             `json:"bootstrap_reason"`
	Entries         []checklists.Entry `json:"checklist"`
	Notes           []checklists.Note  `json:"notes"`
}
type Readiness struct {
	Version     int64                `json:"version"`
	Acceptance  checklists.Progress  `json:"acceptance"`
	Closing     checklists.Progress  `json:"closing"`
	CanActivate bool                 `json:"can_activate"`
	CanPrepare  bool                 `json:"can_prepare_handover"`
	Blockers    []checklists.Blocker `json:"blockers"`
}
type BootstrapInput struct {
	OwnerID      string `json:"owner_id"`
	BusinessDate string `json:"business_date"`
	Kind         string `json:"kind"`
	InitialCash  *int64 `json:"opening_cash_kopecks"`
	Reason       string `json:"reason"`
}
type AnswerInput struct {
	Answer         string `json:"answer"`
	Comment        string `json:"comment"`
	EquipmentIssue bool   `json:"equipment_issue"`
	Version        int64  `json:"version"`
}
type NoteInput struct {
	Code     string `json:"checklist_code"`
	Category string `json:"category"`
	Object   string `json:"object_label"`
	Text     string `json:"text"`
	Version  int64  `json:"version"`
}

type Tx interface {
	auth.Tx
	AnyShift() (bool, error)
	CurrentShift(owner string, manager bool) (string, error)
	LoadShift(id string) (Shift, error)
	CreateShift(shift *Shift, actor string) error
	SaveAnswer(shiftID string, entry checklists.Entry, actor string) error
	SaveNote(shiftID string, note *checklists.Note, actor string) error
	BumpShift(id string) error
	Idempotency(actor, key string) (hash string, result json.RawMessage, found bool, err error)
	SaveIdempotency(actor, key, hash string, result json.RawMessage) error
}
type Repository interface {
	Within(context.Context, func(Tx) error) error
}
