// Package excelsync mirrors committed checklist snapshots into an existing XLSX.
// PostgreSQL remains authoritative; editing Excel never changes a shift.
package excelsync

import (
	"context"
	"time"

	"pc-club-admin/internal/checklists"
)

const (
	Sheet       = "Чек-лист приёма и сдачи смены"
	MIME        = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	MaxFileSize = 4 << 20
)

// Record contains no credentials, proofs or user-supplied destination.
type Record struct {
	ID, OwnerName, BusinessDate, Kind, Status string
	Version                                   int64
	Entries                                   []checklists.Entry
	Notes                                     []checklists.Note
}

// State is delivery evidence, separate from business acceptance and cash readiness.
type State struct {
	RequestedVersion int64      `json:"requested_version"`
	SyncedVersion    int64      `json:"synced_version"`
	LastAttempt      *time.Time `json:"last_attempt_at"`
	LastSuccess      *time.Time `json:"last_success_at"`
	NextAttempt      time.Time  `json:"next_attempt_at"`
	LastCode         string     `json:"last_error_code"`
}

// Failure intentionally excludes provider response bodies and credential values.
type Failure struct {
	Code       string
	Temporary  bool
	HTTPStatus int
}

func (f *Failure) Error() string { return f.Code }
func fail(code string) error     { return &Failure{Code: code} }

// Repository serializes delivery independently of the business lock. deliver runs
// after the snapshot transaction commits, with no shift/outbox row locks held.
type Repository interface {
	DeliverOne(context.Context, string, func(context.Context, Record) error) (bool, error)
}
