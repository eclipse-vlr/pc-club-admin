package shifts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"pc-club-admin/internal/auth"
)

type HistoryFilter struct {
	DateFrom string
	DateTo   string
	OwnerID  string
	Kind     string
	Status   string
	Limit    int
	Cursor   string
	After    PageCursor
}
type PageCursor struct {
	Date string    `json:"date,omitempty"`
	At   time.Time `json:"at"`
	ID   string    `json:"id"`
}
type ShiftSummary struct {
	ID           string     `json:"id"`
	OwnerID      string     `json:"owner_id"`
	OwnerName    string     `json:"owner_name"`
	BusinessDate string     `json:"business_date"`
	Kind         string     `json:"kind"`
	Status       string     `json:"status"`
	PlannedStart time.Time  `json:"planned_start_at"`
	PlannedEnd   time.Time  `json:"planned_end_at"`
	ActualStart  *time.Time `json:"actual_start_at"`
	ActualEnd    *time.Time `json:"actual_end_at"`
	CreatedAt    time.Time  `json:"-"`
}
type HistoryPage struct {
	Items      []ShiftSummary `json:"items"`
	NextCursor string         `json:"next_cursor"`
}
type HandoverSummary struct {
	ID          string     `json:"id"`
	FromShiftID string     `json:"from_shift_id"`
	ToShiftID   string     `json:"to_shift_id"`
	FromName    string     `json:"from_name"`
	ToName      string     `json:"to_name"`
	Revision    int64      `json:"revision"`
	Status      string     `json:"status"`
	Expected    int64      `json:"expected_cash_kopecks"`
	PreparedAt  time.Time  `json:"prepared_at"`
	AcceptedAt  *time.Time `json:"accepted_at"`
	CancelledAt *time.Time `json:"cancelled_at"`
}
type HandoverPage struct {
	Items      []HandoverSummary `json:"items"`
	NextCursor string            `json:"next_cursor"`
}

func validDate(date string) bool {
	parsed, err := time.Parse("2006-01-02", date)
	return err == nil && parsed.Year() > 0 && parsed.Format("2006-01-02") == date
}
func parseCursor(raw string, dated bool) (PageCursor, error) {
	var cursor PageCursor
	if raw == "" {
		return cursor, nil
	}
	if len(raw) > 1024 {
		return cursor, auth.Fail("INVALID_INPUT", 400)
	}
	body, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(body, &cursor) != nil || !auth.ValidID(cursor.ID) || cursor.At.IsZero() || cursor.At.Year() < 1 || cursor.At.Year() > 9999 {
		return PageCursor{}, auth.Fail("INVALID_INPUT", 400)
	}
	if dated && !validDate(cursor.Date) || !dated && cursor.Date != "" {
		return PageCursor{}, auth.Fail("INVALID_INPUT", 400)
	}
	return cursor, nil
}
func encodeCursor(cursor PageCursor) string {
	body, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(body)
}
func pageLimit(limit int) (int, error) {
	if limit == 0 {
		return 50, nil
	}
	if limit < 1 || limit > 100 {
		return 0, auth.Fail("INVALID_INPUT", 400)
	}
	return limit, nil
}
func historyIdentity(id auth.Identity) error {
	if id.User.Has("manager") {
		return nil
	}
	if !id.User.Has("admin") {
		return auth.Fail("FORBIDDEN", 403)
	}
	if !id.PINSession || !id.DeviceTrusted {
		return auth.Fail("DEVICE_REQUIRED", 403)
	}
	return nil
}
func (s *Service) History(ctx context.Context, r auth.Request, filter HistoryFilter) (HistoryPage, error) {
	if filter.DateFrom != "" && !validDate(filter.DateFrom) || filter.DateTo != "" && !validDate(filter.DateTo) || filter.DateFrom != "" && filter.DateTo != "" && filter.DateFrom > filter.DateTo || filter.OwnerID != "" && !auth.ValidID(filter.OwnerID) || filter.Kind != "" && filter.Kind != "day" && filter.Kind != "night" {
		return HistoryPage{}, auth.Fail("INVALID_INPUT", 400)
	}
	switch filter.Status {
	case "", "opening", "active", "handover_pending", "closed", "cancelled":
	default:
		return HistoryPage{}, auth.Fail("INVALID_INPUT", 400)
	}
	var err error
	filter.Limit, err = pageLimit(filter.Limit)
	if err != nil {
		return HistoryPage{}, err
	}
	filter.After, err = parseCursor(filter.Cursor, true)
	if err != nil {
		return HistoryPage{}, err
	}
	result := HistoryPage{Items: []ShiftSummary{}}
	err = s.within(ctx, r, false, func(tx Tx, id auth.Identity) error {
		if err := historyIdentity(id); err != nil {
			return err
		}
		rows, err := tx.ListHistory(filter, id.User.ID, id.User.Has("manager"))
		if err != nil {
			return err
		}
		if len(rows) > filter.Limit {
			last := rows[filter.Limit-1]
			result.NextCursor = encodeCursor(PageCursor{Date: last.BusinessDate, At: last.CreatedAt, ID: last.ID})
			rows = rows[:filter.Limit]
		}
		result.Items = rows
		return nil
	})
	return result, err
}

// Only the owner/manager sees live shift details. An accepted receiver can also
// read the immutable closed outgoing shift; cancelled snapshots remain available
// through Handover, without exposing the sender's subsequent live edits.
func (s *Service) shiftReadable(tx Tx, shift Shift, id auth.Identity) (bool, error) {
	if readable(shift, id) {
		return true, nil
	}
	if shift.Status != "closed" || historyIdentity(id) != nil {
		return false, nil
	}
	return tx.ReceivedClosedShift(shift.ID, id.User.ID)
}
func (s *Service) HandoverHistory(ctx context.Context, r auth.Request, shiftID, cursor string, limit int) (HandoverPage, error) {
	if !auth.ValidID(shiftID) {
		return HandoverPage{}, auth.Fail("INVALID_INPUT", 400)
	}
	limit, err := pageLimit(limit)
	if err != nil {
		return HandoverPage{}, err
	}
	after, err := parseCursor(cursor, false)
	if err != nil {
		return HandoverPage{}, err
	}
	result := HandoverPage{Items: []HandoverSummary{}}
	err = s.within(ctx, r, false, func(tx Tx, id auth.Identity) error {
		if err := historyIdentity(id); err != nil {
			return err
		}
		shift, err := tx.LoadShift(shiftID)
		if err != nil {
			return err
		}
		allowed, err := s.shiftReadable(tx, shift, id)
		if err != nil {
			return err
		}
		if !allowed {
			return auth.ErrMissing
		}
		rows, err := tx.ListHandoverHistory(shiftID, id.User.ID, id.User.Has("manager"), after, limit)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			last := rows[limit-1]
			result.NextCursor = encodeCursor(PageCursor{At: last.PreparedAt, ID: last.ID})
			rows = rows[:limit]
		}
		result.Items = rows
		return nil
	})
	return result, err
}
