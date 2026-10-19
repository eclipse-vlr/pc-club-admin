package postgres

import "pc-club-admin/internal/shifts"

// Deliberately project metadata only: details may evolve to contain sensitive
// authentication data and must not become public through a generic JSON dump.
func (t *authTx) ListAudit(f shifts.AuditFilter) ([]shifts.AuditRecord, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT a.id::text,a.actor_id::text,u.display_name,a.device_id::text,a.action,a.entity_id,a.request_id,a.success,a.reason,a.created_at
 FROM audit_events a LEFT JOIN users u ON u.id=a.actor_id
 WHERE (NULLIF($1,'')::uuid IS NULL OR a.actor_id=NULLIF($1,'')::uuid)
 AND ($2='' OR a.entity_id=$2) AND ($3='' OR a.action=$3)
 AND ($4::bigint=0 OR a.id<$4::bigint)
 AND ($5::timestamptz IS NULL OR a.created_at >= $5)
 AND ($6::timestamptz IS NULL OR a.created_at < $6)
 ORDER BY a.id DESC LIMIT $7`, f.ActorID, f.EntityID, f.Action, f.BeforeID, f.From, f.To, f.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []shifts.AuditRecord{}
	for rows.Next() {
		var row shifts.AuditRecord
		if err := rows.Scan(&row.ID, &row.ActorID, &row.ActorName, &row.DeviceID, &row.Action, &row.EntityID, &row.RequestID, &row.Success, &row.Reason, &row.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
