package shifts

import (
	"context"
	"encoding/json"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/cash"
	"pc-club-admin/internal/checklists"
	"pc-club-admin/internal/excelsync"
	"time"
)

type Shift struct {
	HandoverDeviceID string             `json:"-"`
	ParentID         string             `json:"handover_parent_id"`
	HandoverID       string             `json:"handover_id"`
	ID               string             `json:"id"`
	OwnerID          string             `json:"owner_id"`
	BusinessDate     string             `json:"business_date"`
	Kind             string             `json:"kind"`
	Status           string             `json:"status"`
	PlannedStart     time.Time          `json:"planned_start_at"`
	PlannedEnd       time.Time          `json:"planned_end_at"`
	ActualStart      *time.Time         `json:"actual_start_at"`
	ActualEnd        *time.Time         `json:"actual_end_at"`
	TemplateVersion  int                `json:"template_version"`
	Version          int64              `json:"version"`
	InitialCash      *int64             `json:"initial_cash_kopecks"`
	BootstrapReason  string             `json:"bootstrap_reason"`
	Entries          []checklists.Entry `json:"checklist"`
	Notes            []checklists.Note  `json:"notes"`
	Cash             *cash.View         `json:"cash"`
	ExcelSync        excelsync.State    `json:"excel_sync"`
}
type Readiness struct {
	Version     int64                `json:"version"`
	Acceptance  checklists.Progress  `json:"acceptance"`
	Closing     checklists.Progress  `json:"closing"`
	CanActivate bool                 `json:"can_activate"`
	CanPrepare  bool                 `json:"can_prepare_handover"`
	Blockers    []checklists.Blocker `json:"blockers"`
	Cash        *cash.Summary        `json:"cash"`
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
	CreateHandover(*Handover, Shift, string) error
	LoadHandover(string) (Handover, error)
	SaveReceipt(Handover, *int64) error
	Acknowledge(Handover, []string, string) error
	CancelHandover(Handover, string) error
	AcceptHandover(Handover, string) error
	AnyShift() (bool, error)
	CurrentShift(owner string, manager bool) (string, error)
	LoadShift(id string) (Shift, error)
	CreateShift(shift *Shift, actor string) error
	SaveAnswer(shiftID string, entry checklists.Entry, actor string) error
	SaveNote(shiftID string, note *checklists.Note, actor string) error
	BumpShift(id string) error
	LoadCash(shiftID string) (cash.Session, error)
	SaveCash(cash.Session) error
	AppendMovement(cashID string, movement *cash.Movement) error
	ActivateFirst(shiftID, actor string, counted int64) error
	OperationResult(actor, route, key string) (hash string, result json.RawMessage, found bool, err error)
	SaveOperationResult(actor, route, key, hash string, code int, result json.RawMessage) error
	Idempotency(actor, key string) (hash string, result json.RawMessage, found bool, err error)
	SaveIdempotency(actor, key, hash string, result json.RawMessage) error
}
type Repository interface {
	Within(context.Context, func(Tx) error) error
}
