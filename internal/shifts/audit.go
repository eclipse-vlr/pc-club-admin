package shifts

import (
	"context"
	"strconv"
	"strings"
	"time"

	"pc-club-admin/internal/auth"
)

// AuditFilter uses an exclusive event ID cursor. IDs are strings on the wire to
// preserve PostgreSQL bigint precision in JavaScript clients.
type AuditFilter struct {
	ActorID  string
	EntityID string
	Action   string
	Cursor   string
	Limit    int
	BeforeID int64
	From     *time.Time
	To       *time.Time
}

type AuditRecord struct {
	ID        string    `json:"id"`
	ActorID   *string   `json:"actor_id"`
	ActorName *string   `json:"actor_name"`
	DeviceID  *string   `json:"device_id"`
	Action    string    `json:"action"`
	EntityID  string    `json:"entity_id"`
	RequestID string    `json:"request_id"`
	Success   bool      `json:"success"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

type AuditPage struct {
	Items      []AuditRecord `json:"items"`
	NextCursor string        `json:"next_cursor"`
}

func validateAuditFilter(f AuditFilter) (AuditFilter, error) {
	invalid := func() (AuditFilter, error) { return AuditFilter{}, auth.Fail("INVALID_INPUT", 400) }
	if f.ActorID != "" && !auth.ValidID(f.ActorID) || len(f.EntityID) > 256 || len(f.Action) > 100 || strings.ContainsAny(f.Action, "\r\n\x00") || strings.ContainsRune(f.EntityID, 0) {
		return invalid()
	}
	if f.From != nil && (f.From.Year() < 1 || f.From.Year() > 9999) || f.To != nil && (f.To.Year() < 1 || f.To.Year() > 9999) || f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		return invalid()
	}
	var err error
	f.Limit, err = pageLimit(f.Limit)
	if err != nil {
		return AuditFilter{}, err
	}
	// Never trust an internal cursor value supplied by another caller.
	f.BeforeID = 0
	if f.Cursor != "" {
		f.BeforeID, err = strconv.ParseInt(f.Cursor, 10, 64)
		if err != nil || f.BeforeID < 1 || strconv.FormatInt(f.BeforeID, 10) != f.Cursor {
			return invalid()
		}
	}
	return f, nil
}

func (s *Service) Audit(ctx context.Context, r auth.Request, f AuditFilter) (AuditPage, error) {
	f, err := validateAuditFilter(f)
	if err != nil {
		return AuditPage{}, err
	}
	result := AuditPage{Items: []AuditRecord{}}
	err = s.within(ctx, r, false, func(tx Tx, id auth.Identity) error {
		if !id.User.Has("manager") {
			return auth.Fail("FORBIDDEN", 403)
		}
		rows, err := tx.ListAudit(f)
		if err != nil {
			return err
		}
		if len(rows) > f.Limit {
			result.NextCursor = rows[f.Limit-1].ID
			rows = rows[:f.Limit]
		}
		result.Items = rows
		return nil
	})
	return result, err
}
