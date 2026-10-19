package postgres

import (
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

func (t *authTx) DashboardSource() (time.Time, string, string, bool, error) {
	var now time.Time
	var responsible, opening string
	var history bool
	err := t.tx.QueryRow(t.ctx, `SELECT clock_timestamp(),
 COALESCE((SELECT id::text FROM shifts WHERE status IN ('active','handover_pending') LIMIT 1),''),
 COALESCE((SELECT id::text FROM shifts WHERE status='opening' AND handover_parent_id IS NULL LIMIT 1),''),
 EXISTS(SELECT 1 FROM shifts)`).Scan(&now, &responsible, &opening, &history)
	return now, responsible, opening, history, err
}

// Audit IDs and cash versions preserve the club-serialized write order even
// when transaction-start timestamps precede a lock wait or the clock changes.
func (t *authTx) CashSavedReport(shiftID string) (*time.Time, int64, error) {
	var saved time.Time
	var revisionText string
	err := t.tx.QueryRow(t.ctx, `SELECT created_at,details #>> '{after,version}' FROM audit_events WHERE entity_id=$1 AND action='cash_saved' AND success ORDER BY id DESC LIMIT 1`, shiftID).Scan(&saved, &revisionText)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	revision, err := strconv.ParseInt(revisionText, 10, 64)
	if err != nil {
		return nil, 0, err
	}
	if revision < 1 {
		return nil, 0, errors.New("invalid cash report revision")
	}
	return &saved, revision, nil
}
