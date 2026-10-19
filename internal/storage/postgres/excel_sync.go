package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/excelsync"
)

type ExcelRepository struct{ Pool *pgxpool.Pool }

func (t *authTx) loadExcelState(shiftID string) (excelsync.State, error) {
	var state excelsync.State
	err := t.tx.QueryRow(t.ctx, `SELECT requested_version,synced_version,last_attempt_at,last_success_at,next_attempt_at,last_error_code FROM excel_sync_queue WHERE shift_id=$1`, shiftID).Scan(&state.RequestedVersion, &state.SyncedVersion, &state.LastAttempt, &state.LastSuccess, &state.NextAttempt, &state.LastCode)
	return state, err
}

// PrepareTarget runs before serving requests. Changing a destination requires
// stopping old API processes; delivered evidence from another file is not reused.
func (r ExcelRepository) PrepareTarget(ctx context.Context, target string) error {
	_, err := r.Pool.Exec(ctx, `UPDATE excel_sync_queue SET synced_version=0,last_success_at=NULL,last_error_code='',next_attempt_at=clock_timestamp() WHERE synced_version>0 AND synced_target_hash<>$1`, target)
	return err
}

// DeliverOne keeps only an independent advisory lock during remote I/O. It holds
// no club/shift/outbox row lock then, so editing a checklist survives a Google
// outage. Snapshot and result transactions are short and use committed DB state.
func (r ExcelRepository) DeliverOne(ctx context.Context, target string, deliver func(context.Context, excelsync.Record) error) (bool, error) {
	guard, err := r.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackExcel(guard)
	var locked bool
	if err := guard.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(728391005)").Scan(&locked); err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}
	var record excelsync.Record
	var attempts int
	err = securityTransaction(ctx, r.Pool, func(tx *authTx) error {
		var shiftID string
		err := tx.tx.QueryRow(tx.ctx, `SELECT shift_id::text,attempts FROM excel_sync_queue WHERE (requested_version>synced_version OR synced_target_hash<>$1) AND next_attempt_at<=clock_timestamp() ORDER BY next_attempt_at,requested_at,shift_id LIMIT 1`, target).Scan(&shiftID, &attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		shift, err := tx.LoadShift(shiftID)
		if err != nil {
			return err
		}
		owner, err := tx.User(shift.OwnerID)
		if err != nil {
			return err
		}
		record = excelsync.Record{ID: shift.ID, OwnerName: owner.Name, BusinessDate: shift.BusinessDate, Kind: shift.Kind, Status: shift.Status, Version: shift.Version, Entries: shift.Entries, Notes: shift.Notes}
		return nil
	})
	if err != nil || record.ID == "" {
		return false, err
	}
	deliveryErr := deliver(ctx, record)
	if ctx.Err() != nil {
		return true, ctx.Err()
	} // outcome remains unconfirmed; next run reconciles
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return true, err
	}
	defer rollbackExcel(tx)
	code := ""
	if deliveryErr == nil {
		_, err = tx.Exec(ctx, `UPDATE excel_sync_queue SET synced_version=CASE WHEN synced_target_hash=$3 THEN GREATEST(synced_version,$2) ELSE $2 END,synced_target_hash=$3,last_attempt_at=clock_timestamp(),last_success_at=clock_timestamp(),last_error_code='',attempts=0,next_attempt_at=clock_timestamp() WHERE shift_id=$1`, record.ID, record.Version, target)
	} else {
		code = excelsync.Code(deliveryErr)
		delay := excelsync.RetryDelay(deliveryErr, attempts)
		_, err = tx.Exec(ctx, `UPDATE excel_sync_queue SET last_attempt_at=clock_timestamp(),last_error_code=$2,attempts=LEAST(attempts+1,1000),next_attempt_at=clock_timestamp()+$3*interval '1 second' WHERE shift_id=$1`, record.ID, code, int64(delay/time.Second))
	}
	if err != nil {
		return true, err
	}
	audit := &authTx{ctx: ctx, tx: tx}
	if err := audit.Audit(auth.Event{Action: "excel_synchronization", EntityID: record.ID, RequestID: "excel-worker", Success: deliveryErr == nil, Reason: code, Details: map[string]any{"shift_version": record.Version, "test_mode": true}}); err != nil {
		return true, err
	}
	if err := tx.Commit(ctx); err != nil {
		return true, err
	}
	return true, nil
}

func rollbackExcel(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx) // cleanup after commit/cancellation; no operation is retried here
}

var _ excelsync.Repository = ExcelRepository{}
