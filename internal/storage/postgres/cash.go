package postgres

import (
	"encoding/json"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/cash"
)

func (t *authTx) LoadCash(shiftID string) (cash.Session, error) {
	var s cash.Session
	err := t.tx.QueryRow(t.ctx, `SELECT id::text,shift_id::text,version,opening_cash_kopecks,counted_opening_kopecks,opening_confirmed_by::text,opening_confirmed_at,langame_cash_amount_kopecks,extra_cash_refunds_kopecks,extra_cash_expenses_kopecks,counted_closing_kopecks,policy_version FROM cash_sessions WHERE shift_id=$1 FOR UPDATE`, shiftID).Scan(&s.ID, &s.ShiftID, &s.Version, &s.Opening, &s.CountedOpening, &s.OpeningConfirmedBy, &s.OpeningConfirmedAt, &s.Langame, &s.Refunds, &s.Expenses, &s.Counted, &s.Policy)
	if err != nil {
		return s, storageError(err)
	}
	s.Movements = []cash.Movement{}
	rows, err := t.tx.Query(t.ctx, `SELECT id::text,kind,amount_kopecks,reversal_of::text,reason,recipient,occurred_at,created_at,actor_id::text FROM cash_movements WHERE cash_session_id=$1 ORDER BY created_at,id`, s.ID)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var m cash.Movement
		if err := rows.Scan(&m.ID, &m.Kind, &m.Amount, &m.ReversalOf, &m.Reason, &m.Recipient, &m.OccurredAt, &m.CreatedAt, &m.ActorID); err != nil {
			return s, err
		}
		s.Movements = append(s.Movements, m)
	}
	return s, rows.Err()
}
func (t *authTx) SaveCash(s cash.Session) error {
	tag, err := t.tx.Exec(t.ctx, `UPDATE cash_sessions SET langame_cash_amount_kopecks=$2,extra_cash_refunds_kopecks=$3,extra_cash_expenses_kopecks=$4,counted_closing_kopecks=$5,version=$6 WHERE id=$1 AND version=$7`, s.ID, s.Langame, s.Refunds, s.Expenses, s.Counted, s.Version, s.Version-1)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return auth.Fail("VERSION_CONFLICT", 409)
	}
	return nil
}
func (t *authTx) AppendMovement(cashID string, m *cash.Movement) error {
	return t.tx.QueryRow(t.ctx, `INSERT INTO cash_movements(cash_session_id,kind,amount_kopecks,reversal_of,reason,recipient,occurred_at,actor_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text,created_at`, cashID, m.Kind, m.Amount, m.ReversalOf, m.Reason, m.Recipient, m.OccurredAt, m.ActorID).Scan(&m.ID, &m.CreatedAt)
}
func (t *authTx) ActivateFirst(shiftID, actor string, counted int64) error {
	tag, err := t.tx.Exec(t.ctx, `UPDATE cash_sessions SET counted_opening_kopecks=$2,opening_confirmed_by=$3,opening_confirmed_at=now(),version=version+1 WHERE shift_id=$1 AND opening_cash_kopecks=$2 AND opening_confirmed_at IS NULL`, shiftID, counted, actor)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return auth.Fail("INVALID_STATE", 409)
	}
	tag, err = t.tx.Exec(t.ctx, `UPDATE shifts SET status='active',actual_start_at=clock_timestamp(),version=version+1 WHERE id=$1 AND owner_id=$2 AND status='opening' AND previous_shift_id IS NULL AND initial_cash_kopecks IS NOT NULL`, shiftID, actor)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return auth.Fail("INVALID_STATE", 409)
	}
	return nil
}
func (t *authTx) OperationResult(actor, route, key string) (string, json.RawMessage, bool, error) {
	var hash string
	var body json.RawMessage
	err := t.tx.QueryRow(t.ctx, `SELECT payload_hash,result_body FROM idempotency_records WHERE actor_id=$1 AND route=$2 AND key=$3 AND expires_at>now()`, actor, route, key).Scan(&hash, &body)
	if storageError(err) == auth.ErrMissing {
		return "", nil, false, nil
	}
	return hash, body, err == nil, err
}
func (t *authTx) SaveOperationResult(actor, route, key, hash string, code int, body json.RawMessage) error {
	_, err := t.tx.Exec(t.ctx, `INSERT INTO idempotency_records(actor_id,route,key,payload_hash,result_code,result_body) VALUES($1,$2,$3,$4,$5,$6)`, actor, route, key, hash, code, body)
	return err
}
