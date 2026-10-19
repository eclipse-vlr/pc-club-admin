package postgres

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/checklists"
	"pc-club-admin/internal/shifts"
)

type ShiftRepository struct{ Pool *pgxpool.Pool }

func (r ShiftRepository) Within(ctx context.Context, fn func(shifts.Tx) error) error {
	return securityTransaction(ctx, r.Pool, func(tx *authTx) error { return fn(tx) })
}
func (t *authTx) AnyShift() (bool, error) {
	var exists bool
	err := t.tx.QueryRow(t.ctx, "SELECT EXISTS(SELECT 1 FROM shifts)").Scan(&exists)
	return exists, err
}
func (t *authTx) CurrentShift(owner string, manager bool) (string, error) {
	var id string
	err := t.tx.QueryRow(t.ctx, `SELECT id::text FROM shifts WHERE status IN ('opening','active','handover_pending') AND ($2 OR owner_id=$1) ORDER BY CASE WHEN status='opening' THEN 1 ELSE 0 END,created_at DESC LIMIT 1`, owner, manager).Scan(&id)
	return id, storageError(err)
}
func (t *authTx) LoadShift(id string) (shifts.Shift, error) {
	var s shifts.Shift
	err := t.tx.QueryRow(t.ctx, `SELECT id::text,owner_id::text,business_date::text,kind,status,planned_start_at,planned_end_at,actual_start_at,actual_end_at,template_version,version,initial_cash_kopecks,bootstrap_reason FROM shifts WHERE id=$1 FOR UPDATE`, id).Scan(&s.ID, &s.OwnerID, &s.BusinessDate, &s.Kind, &s.Status, &s.PlannedStart, &s.PlannedEnd, &s.ActualStart, &s.ActualEnd, &s.TemplateVersion, &s.Version, &s.InitialCash, &s.BootstrapReason)
	if err != nil {
		return s, storageError(err)
	}
	s.Entries = []checklists.Entry{}
	s.Notes = []checklists.Note{}
	rows, err := t.tx.Query(t.ctx, `SELECT code,phase,label,help_text,allowed_answers,required,sort_order,equipment_check,answer,comment,equipment_issue,answered_by::text,answered_at,version FROM shift_checklist_entries WHERE shift_id=$1 ORDER BY sort_order`, id)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var e checklists.Entry
		if err := rows.Scan(&e.Code, &e.Phase, &e.Label, &e.Help, &e.Allowed, &e.Required, &e.Order, &e.EquipmentCheck, &e.Answer, &e.Comment, &e.EquipmentIssue, &e.AnsweredBy, &e.AnsweredAt, &e.Version); err != nil {
			rows.Close()
			return s, err
		}
		s.Entries = append(s.Entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return s, err
	}
	rows, err = t.tx.Query(t.ctx, `SELECT id::text,COALESCE(checklist_code,''),category,object_label,text,created_by::text,created_at,updated_at,version FROM shift_notes WHERE shift_id=$1 ORDER BY created_at,id`, id)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var n checklists.Note
		if err := rows.Scan(&n.ID, &n.Code, &n.Category, &n.Object, &n.Text, &n.CreatedBy, &n.CreatedAt, &n.UpdatedAt, &n.Version); err != nil {
			rows.Close()
			return s, err
		}
		s.Notes = append(s.Notes, n)
	}
	rows.Close()
	return s, rows.Err()
}
func (t *authTx) CreateShift(s *shifts.Shift, actor string) error {
	if err := t.tx.QueryRow(t.ctx, "SELECT version FROM checklist_template_versions WHERE published ORDER BY version DESC LIMIT 1").Scan(&s.TemplateVersion); err != nil {
		return auth.Fail("CHECKLIST_TEMPLATE_INVALID", 503)
	}
	var acceptance, closing int
	if err := t.tx.QueryRow(t.ctx, `SELECT count(*) FILTER(WHERE phase='acceptance' AND required),count(*) FILTER(WHERE phase='closing' AND required) FROM checklist_template_items WHERE version=$1`, s.TemplateVersion).Scan(&acceptance, &closing); err != nil {
		return err
	}
	if acceptance == 0 || closing == 0 {
		return auth.Fail("CHECKLIST_TEMPLATE_INVALID", 503)
	}
	if err := t.tx.QueryRow(t.ctx, `INSERT INTO shifts(owner_id,business_date,kind,status,planned_start_at,planned_end_at,template_version,initial_cash_kopecks,bootstrap_reason,created_by) VALUES($1,$2::date,$3,'opening',$4,$5,$6,$7,$8,$9) RETURNING id::text`, s.OwnerID, s.BusinessDate, s.Kind, s.PlannedStart, s.PlannedEnd, s.TemplateVersion, s.InitialCash, s.BootstrapReason, actor).Scan(&s.ID); err != nil {
		return storageError(err)
	}
	if _, err := t.tx.Exec(t.ctx, `INSERT INTO shift_checklist_entries(shift_id,code,phase,label,help_text,allowed_answers,required,sort_order,equipment_check) SELECT $1,code,phase,label,help_text,allowed_answers,required,sort_order,equipment_check FROM checklist_template_items WHERE version=$2`, s.ID, s.TemplateVersion); err != nil {
		return err
	}
	loaded, err := t.LoadShift(s.ID)
	if err == nil {
		*s = loaded
	}
	return err
}
func (t *authTx) SaveAnswer(shiftID string, e checklists.Entry, actor string) error {
	tag, err := t.tx.Exec(t.ctx, `UPDATE shift_checklist_entries SET answer=$3,comment=$4,equipment_issue=$5,answered_by=$6,answered_at=now(),version=$7 WHERE shift_id=$1 AND code=$2 AND version=$8`, shiftID, e.Code, e.Answer, e.Comment, e.EquipmentIssue, actor, e.Version, e.Version-1)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return auth.Fail("VERSION_CONFLICT", 409)
	}
	return nil
}
func (t *authTx) BumpShift(id string) error {
	_, err := t.tx.Exec(t.ctx, "UPDATE shifts SET version=version+1 WHERE id=$1", id)
	return err
}
func (t *authTx) SaveNote(shiftID string, n *checklists.Note, actor string) error {
	if n.ID == "" {
		return t.tx.QueryRow(t.ctx, `INSERT INTO shift_notes(shift_id,checklist_code,category,object_label,text,created_by,updated_by) VALUES($1,NULLIF($2,''),$3,$4,$5,$6,$6) RETURNING id::text,created_at,updated_at`, shiftID, n.Code, n.Category, n.Object, n.Text, actor).Scan(&n.ID, &n.CreatedAt, &n.UpdatedAt)
	}
	err := t.tx.QueryRow(t.ctx, `UPDATE shift_notes SET checklist_code=NULLIF($3,''),category=$4,object_label=$5,text=$6,updated_by=$7,updated_at=now(),version=$8 WHERE id=$1 AND shift_id=$2 AND version=$9 RETURNING created_at,updated_at`, n.ID, shiftID, n.Code, n.Category, n.Object, n.Text, actor, n.Version, n.Version-1).Scan(&n.CreatedAt, &n.UpdatedAt)
	if storageError(err) == auth.ErrMissing {
		return auth.Fail("VERSION_CONFLICT", 409)
	}
	return err
}

func (t *authTx) Idempotency(actor, key string) (string, json.RawMessage, bool, error) {
	var hash string
	var body json.RawMessage
	err := t.tx.QueryRow(t.ctx, `SELECT payload_hash,result_body FROM idempotency_records WHERE actor_id=$1 AND route='POST /api/v1/shifts/bootstrap' AND key=$2 AND expires_at>now()`, actor, key).Scan(&hash, &body)
	if storageError(err) == auth.ErrMissing {
		return "", nil, false, nil
	}
	return hash, body, err == nil, err
}
func (t *authTx) SaveIdempotency(actor, key, hash string, body json.RawMessage) error {
	_, err := t.tx.Exec(t.ctx, `INSERT INTO idempotency_records(actor_id,route,key,payload_hash,result_code,result_body) VALUES($1,'POST /api/v1/shifts/bootstrap',$2,$3,201,$4)`, actor, key, hash, body)
	return err
}

var _ shifts.Repository = ShiftRepository{}
var _ shifts.Tx = (*authTx)(nil)
