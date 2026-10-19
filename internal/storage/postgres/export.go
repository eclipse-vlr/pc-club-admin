package postgres

import "pc-club-admin/internal/shifts"

// ExportRows returns six shared text columns, followed by either four shift
// timestamps or seven integer-kopeck columns and the policy version. Money stays
// decimal text so numeric collection aggregates never overflow or round.
func (t *authTx) ExportRows(f shifts.ExportFilter, cash bool) ([][]string, error) {
	fields := `s.id::text,s.business_date::text,s.kind,s.status,s.owner_id::text,u.display_name`
	join := ""
	columns := 10
	if !cash {
		fields += `,s.planned_start_at::text,s.planned_end_at::text,COALESCE(s.actual_start_at::text,''),COALESCE(s.actual_end_at::text,'')`
	}
	if cash {
		columns = 14
		fields += `,COALESCE(c.opening_cash_kopecks::text,''),COALESCE(c.counted_opening_kopecks::text,''),COALESCE(c.langame_cash_amount_kopecks::text,''),COALESCE(c.extra_cash_refunds_kopecks::text,''),COALESCE(c.extra_cash_expenses_kopecks::text,''),COALESCE(c.counted_closing_kopecks::text,''),CASE WHEN c.id IS NULL THEN '' ELSE COALESCE((SELECT SUM(CASE WHEN m.kind='collection' THEN m.amount_kopecks::numeric ELSE -m.amount_kopecks::numeric END)::text FROM cash_movements m WHERE m.cash_session_id=c.id),'0') END,COALESCE(c.policy_version,'')`
		join = ` LEFT JOIN cash_sessions c ON c.shift_id=s.id `
	}
	rows, err := t.tx.Query(t.ctx, `SELECT `+fields+` FROM shifts s JOIN users u ON u.id=s.owner_id `+join+`
 WHERE s.business_date >= $1::date AND s.business_date <= $2::date
 AND (NULLIF($3,'')::uuid IS NULL OR s.owner_id=NULLIF($3,'')::uuid)
 ORDER BY s.business_date DESC,s.created_at DESC,s.id DESC LIMIT $4`, f.DateFrom, f.DateTo, f.OwnerID, shifts.ExportLimit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := [][]string{}
	for rows.Next() {
		row := make([]string, columns)
		dest := make([]any, columns)
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
