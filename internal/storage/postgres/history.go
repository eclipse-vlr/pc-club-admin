package postgres

import "pc-club-admin/internal/shifts"

func (t *authTx) ListHistory(f shifts.HistoryFilter, actor string, manager bool) ([]shifts.ShiftSummary, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT s.id::text,s.owner_id::text,u.display_name,s.business_date::text,s.kind,s.status,s.planned_start_at,s.planned_end_at,s.actual_start_at,s.actual_end_at,s.created_at
 FROM shifts s JOIN users u ON u.id=s.owner_id
 WHERE ($1 OR s.owner_id=$2 OR (s.status='closed' AND EXISTS(SELECT 1 FROM handovers h WHERE h.from_shift_id=s.id AND h.to_user_id=$2 AND h.status='accepted')))
 AND (NULLIF($3,'')::date IS NULL OR s.business_date>=NULLIF($3,'')::date)
 AND (NULLIF($4,'')::date IS NULL OR s.business_date<=NULLIF($4,'')::date)
 AND (NULLIF($5,'')::uuid IS NULL OR s.owner_id=NULLIF($5,'')::uuid)
 AND ($6='' OR s.kind=$6) AND ($7='' OR s.status=$7)
 AND (NULLIF($8,'')::uuid IS NULL OR (s.business_date,s.created_at,s.id)<(NULLIF($9,'')::date,$10::timestamptz,NULLIF($8,'')::uuid))
 ORDER BY s.business_date DESC,s.created_at DESC,s.id DESC LIMIT $11`, manager, actor, f.DateFrom, f.DateTo, f.OwnerID, f.Kind, f.Status, f.After.ID, f.After.Date, f.After.At, f.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []shifts.ShiftSummary{}
	for rows.Next() {
		var row shifts.ShiftSummary
		if err := rows.Scan(&row.ID, &row.OwnerID, &row.OwnerName, &row.BusinessDate, &row.Kind, &row.Status, &row.PlannedStart, &row.PlannedEnd, &row.ActualStart, &row.ActualEnd, &row.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
func (t *authTx) ReceivedClosedShift(shiftID, actor string) (bool, error) {
	var allowed bool
	err := t.tx.QueryRow(t.ctx, `SELECT EXISTS(SELECT 1 FROM handovers h JOIN shifts s ON s.id=h.from_shift_id WHERE h.from_shift_id=$1 AND s.status='closed' AND h.status='accepted' AND h.to_user_id=$2)`, shiftID, actor).Scan(&allowed)
	return allowed, err
}
func (t *authTx) ListHandoverHistory(shiftID, actor string, manager bool, after shifts.PageCursor, limit int) ([]shifts.HandoverSummary, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT h.id::text,h.from_shift_id::text,h.to_shift_id::text,a.display_name,b.display_name,h.revision,h.status,h.expected_cash_kopecks,h.prepared_at,h.accepted_at,h.cancelled_at
 FROM handovers h JOIN users a ON a.id=h.from_user_id JOIN users b ON b.id=h.to_user_id
 WHERE (h.from_shift_id=$1 OR h.to_shift_id=$1) AND ($2 OR h.from_user_id=$3 OR h.to_user_id=$3)
 AND (NULLIF($4,'')::uuid IS NULL OR (h.prepared_at,h.id)<($5::timestamptz,NULLIF($4,'')::uuid))
 ORDER BY h.prepared_at DESC,h.id DESC LIMIT $6`, shiftID, manager, actor, after.ID, after.At, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []shifts.HandoverSummary{}
	for rows.Next() {
		var row shifts.HandoverSummary
		if err := rows.Scan(&row.ID, &row.FromShiftID, &row.ToShiftID, &row.FromName, &row.ToName, &row.Revision, &row.Status, &row.Expected, &row.PreparedAt, &row.AcceptedAt, &row.CancelledAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
