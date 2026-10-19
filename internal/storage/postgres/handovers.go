package postgres

import (
	"encoding/json"
	"time"

	"pc-club-admin/internal/shifts"
)

func (t *authTx) CreateHandover(h *shifts.Handover, next shifts.Shift, actor string) error {
	if err := t.CreateShift(&next, actor); err != nil {
		return err
	}
	h.ToShiftID = next.ID
	snapshot, err := json.Marshal(h.Snapshot)
	if err != nil {
		return err
	}
	err = t.tx.QueryRow(t.ctx, `INSERT INTO handovers(from_shift_id,to_shift_id,from_user_id,to_user_id,device_id,revision,source_version,snapshot,expected_cash_kopecks) SELECT $1,$2,$3,$4,$5,COALESCE(MAX(revision),0)+1,$6,$7,$8 FROM handovers WHERE from_shift_id=$1 RETURNING id::text`, h.FromShiftID, h.ToShiftID, h.FromUserID, h.ToUserID, h.DeviceID, h.SourceVersion, snapshot, h.Expected).Scan(&h.ID)
	if err != nil {
		return storageError(err)
	}
	if _, err = t.tx.Exec(t.ctx, `INSERT INTO handover_receipts(handover_id,receiver_id) VALUES($1,$2)`, h.ID, h.ToUserID); err != nil {
		return err
	}
	if _, err = t.tx.Exec(t.ctx, `UPDATE shifts SET status='handover_pending',version=version+1 WHERE id=$1`, h.FromShiftID); err != nil {
		return err
	}
	*h, err = t.LoadHandover(h.ID)
	return err
}
func (t *authTx) LoadHandover(id string) (shifts.Handover, error) {
	var h shifts.Handover
	var snapshot []byte
	err := t.tx.QueryRow(t.ctx, `SELECT h.id::text,h.from_shift_id::text,h.to_shift_id::text,h.from_user_id::text,h.to_user_id::text,h.device_id::text,h.revision,h.version,h.status,h.source_version,h.snapshot,h.expected_cash_kopecks,r.counted_cash_kopecks,h.prepared_at,h.accepted_at,h.cancelled_at,h.actual_boundary_at,h.cancellation_reason FROM handovers h JOIN handover_receipts r ON r.handover_id=h.id WHERE h.id=$1 FOR UPDATE OF h,r`, id).Scan(&h.ID, &h.FromShiftID, &h.ToShiftID, &h.FromUserID, &h.ToUserID, &h.DeviceID, &h.Revision, &h.Version, &h.Status, &h.SourceVersion, &snapshot, &h.Expected, &h.Counted, &h.PreparedAt, &h.AcceptedAt, &h.CancelledAt, &h.Boundary, &h.Reason)
	if err != nil {
		return h, storageError(err)
	}
	if err = json.Unmarshal(snapshot, &h.Snapshot); err != nil {
		return h, err
	}
	// Club singleton is already locked; lock both shift rows in UUID order as well.
	rows, err := t.tx.Query(t.ctx, `SELECT id FROM shifts WHERE id=$1 OR id=$2 ORDER BY id FOR UPDATE`, h.FromShiftID, h.ToShiftID)
	if err != nil {
		return h, err
	}
	for rows.Next() {
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return h, err
	}
	h.Acknowledged = []string{}
	rows, err = t.tx.Query(t.ctx, `SELECT note_id::text FROM handover_note_acknowledgements WHERE handover_id=$1 ORDER BY note_id`, h.ID)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		var note string
		if err := rows.Scan(&note); err != nil {
			return h, err
		}
		h.Acknowledged = append(h.Acknowledged, note)
	}
	return h, rows.Err()
}
func (t *authTx) SaveReceipt(h shifts.Handover, counted *int64) error {
	if _, err := t.tx.Exec(t.ctx, `UPDATE handover_receipts SET counted_cash_kopecks=$2,updated_at=clock_timestamp() WHERE handover_id=$1`, h.ID, counted); err != nil {
		return err
	}
	return t.bumpHandover(h)
}
func (t *authTx) bumpHandover(h shifts.Handover) error {
	if _, err := t.tx.Exec(t.ctx, `UPDATE handovers SET version=version+1 WHERE id=$1`, h.ID); err != nil {
		return err
	}
	return t.BumpShift(h.ToShiftID)
}
func (t *authTx) Acknowledge(h shifts.Handover, notes []string, actor string) error {
	for _, note := range notes {
		if _, err := t.tx.Exec(t.ctx, `INSERT INTO handover_note_acknowledgements(handover_id,note_id,actor_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, h.ID, note, actor); err != nil {
			return err
		}
	}
	return t.bumpHandover(h)
}
func (t *authTx) CancelHandover(h shifts.Handover, reason string) error {
	if _, err := t.tx.Exec(t.ctx, `UPDATE handovers SET status='cancelled',version=version+1,cancelled_at=clock_timestamp(),cancellation_reason=$2 WHERE id=$1`, h.ID, reason); err != nil {
		return err
	}
	if _, err := t.tx.Exec(t.ctx, `UPDATE shifts SET status=CASE WHEN id=$1 THEN 'active' ELSE 'cancelled' END,version=version+1 WHERE id=$1 OR id=$2`, h.FromShiftID, h.ToShiftID); err != nil {
		return err
	}
	return nil
}
func (t *authTx) AcceptHandover(h shifts.Handover, actor string) error {
	var boundary time.Time
	if err := t.tx.QueryRow(t.ctx, `SELECT GREATEST(clock_timestamp(),actual_start_at) FROM shifts WHERE id=$1`, h.FromShiftID).Scan(&boundary); err != nil {
		return err
	}
	if _, err := t.tx.Exec(t.ctx, `UPDATE shifts SET status='closed',actual_end_at=$2,version=version+1 WHERE id=$1`, h.FromShiftID, boundary); err != nil {
		return err
	}
	if _, err := t.tx.Exec(t.ctx, `INSERT INTO cash_sessions(shift_id,opening_cash_kopecks,counted_opening_kopecks,opening_confirmed_by,opening_confirmed_at) VALUES($1,$2,$2,$3,$4)`, h.ToShiftID, h.Expected, actor, boundary); err != nil {
		return err
	}
	if _, err := t.tx.Exec(t.ctx, `UPDATE shifts SET status='active',previous_shift_id=$2,actual_start_at=$3,version=version+1 WHERE id=$1`, h.ToShiftID, h.FromShiftID, boundary); err != nil {
		return err
	}
	if _, err := t.tx.Exec(t.ctx, `UPDATE handover_receipts SET accepted_at=$2 WHERE handover_id=$1`, h.ID, boundary); err != nil {
		return err
	}
	_, err := t.tx.Exec(t.ctx, `UPDATE handovers SET status='accepted',version=version+1,accepted_at=$2,actual_boundary_at=$2 WHERE id=$1`, h.ID, boundary)
	return err
}
