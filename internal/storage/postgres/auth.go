package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"pc-club-admin/internal/auth"
	"time"
)

type AuthRepository struct{ Pool *pgxpool.Pool }
type authTx struct {
	ctx context.Context
	tx  pgx.Tx
}

func (r AuthRepository) Within(ctx context.Context, fn func(auth.Tx) error) error {
	return securityTransaction(ctx, r.Pool, func(tx *authTx) error { return fn(tx) })
}
func securityTransaction(ctx context.Context, pool *pgxpool.Pool, fn func(*authTx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	// One small club: serialize security mutations/read-validation using its singleton.
	// Rechecked session/device state and audit commit share the same transaction.
	if _, err := tx.Exec(ctx, "SELECT id FROM club_settings WHERE id=1 FOR UPDATE"); err != nil {
		return err
	}
	if err := fn(&authTx{ctx, tx}); err != nil {
		return storageError(err)
	}
	return storageError(tx.Commit(ctx))
}
func storageError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.ErrMissing
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return auth.Fail("ALREADY_EXISTS", 409)
	}
	return err
}

const userSelect = `SELECT u.id::text,u.display_name,COALESCE(u.manager_login,''),u.active,u.pin_hash,u.password_hash,u.auth_version,u.version,ARRAY(SELECT role FROM user_roles WHERE user_id=u.id ORDER BY role) FROM users u `

func scanUser(row pgx.Row) (auth.User, error) {
	var u auth.User
	err := row.Scan(&u.ID, &u.Name, &u.Login, &u.Active, &u.PINHash, &u.PasswordHash, &u.AuthVersion, &u.Version, &u.Roles)
	return u, storageError(err)
}
func (t *authTx) User(id string) (auth.User, error) {
	return scanUser(t.tx.QueryRow(t.ctx, userSelect+"WHERE u.id=$1", id))
}
func (t *authTx) UserByLogin(login string) (auth.User, error) {
	return scanUser(t.tx.QueryRow(t.ctx, userSelect+"WHERE u.manager_login=$1", login))
}
func (t *authTx) Users() ([]auth.User, error) {
	list := []auth.User{}
	rows, err := t.tx.Query(t.ctx, userSelect+"ORDER BY u.display_name,u.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, rows.Err()
}
func (t *authTx) SaveUser(u *auth.User) error {
	var err error
	if u.ID == "" {
		err = t.tx.QueryRow(t.ctx, `INSERT INTO users(display_name,manager_login,active,pin_hash,password_hash,auth_version,version) VALUES($1,NULLIF($2,''),$3,$4,$5,$6,$7) RETURNING id::text`, u.Name, u.Login, u.Active, u.PINHash, u.PasswordHash, u.AuthVersion, u.Version).Scan(&u.ID)
	} else {
		_, err = t.tx.Exec(t.ctx, `UPDATE users SET display_name=$2,active=$3,pin_hash=$4,password_hash=$5,auth_version=$6,version=$7 WHERE id=$1`, u.ID, u.Name, u.Active, u.PINHash, u.PasswordHash, u.AuthVersion, u.Version)
	}
	if err != nil {
		return storageError(err)
	}
	// Roles of existing users are intentionally immutable until an audited role-change scenario.
	for _, role := range u.Roles {
		if _, err := t.tx.Exec(t.ctx, "INSERT INTO user_roles(user_id,role) VALUES($1,$2) ON CONFLICT DO NOTHING", u.ID, role); err != nil {
			return err
		}
	}
	return nil
}
func (t *authTx) Session(hash string) (auth.Session, error) {
	var s auth.Session
	err := t.tx.QueryRow(t.ctx, `SELECT token_hash,user_id::text,COALESCE(device_id::text,''),csrf_hash,auth_version,idle_expires_at,absolute_expires_at,revoked_at IS NOT NULL FROM sessions WHERE token_hash=$1`, hash).Scan(&s.Hash, &s.UserID, &s.DeviceID, &s.CSRFHash, &s.AuthVersion, &s.Idle, &s.Absolute, &s.Revoked)
	return s, storageError(err)
}
func (t *authTx) SaveSession(s auth.Session) error {
	_, err := t.tx.Exec(t.ctx, `INSERT INTO sessions(token_hash,user_id,device_id,csrf_hash,auth_version,idle_expires_at,absolute_expires_at) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7) ON CONFLICT(token_hash) DO UPDATE SET idle_expires_at=excluded.idle_expires_at`, s.Hash, s.UserID, s.DeviceID, s.CSRFHash, s.AuthVersion, s.Idle, s.Absolute)
	return err
}
func (t *authTx) RevokeSession(hash string) error {
	_, err := t.tx.Exec(t.ctx, "UPDATE sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE token_hash=$1", hash)
	return err
}
func (t *authTx) RevokeUserSessions(id string) error {
	_, err := t.tx.Exec(t.ctx, "UPDATE sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1", id)
	return err
}
func (t *authTx) Device(hash string) (auth.Device, error) {
	var d auth.Device
	err := t.tx.QueryRow(t.ctx, `SELECT id::text,token_hash,label,expires_at,revoked_at IS NOT NULL,created_by::text FROM trusted_devices WHERE token_hash=$1`, hash).Scan(&d.ID, &d.Hash, &d.Label, &d.Expires, &d.Revoked, &d.CreatedBy)
	return d, storageError(err)
}
func (t *authTx) Devices() ([]auth.Device, error) {
	list := []auth.Device{}
	rows, err := t.tx.Query(t.ctx, `SELECT id::text,token_hash,label,expires_at,revoked_at IS NOT NULL,created_by::text FROM trusted_devices ORDER BY expires_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d auth.Device
		if err := rows.Scan(&d.ID, &d.Hash, &d.Label, &d.Expires, &d.Revoked, &d.CreatedBy); err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}
func (t *authTx) SaveDevice(d *auth.Device) error {
	if d.ID == "" {
		return t.tx.QueryRow(t.ctx, `INSERT INTO trusted_devices(token_hash,label,expires_at,created_by) VALUES($1,$2,$3,$4) RETURNING id::text`, d.Hash, d.Label, d.Expires, d.CreatedBy).Scan(&d.ID)
	}
	_, err := t.tx.Exec(t.ctx, `UPDATE trusted_devices SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1`, d.ID)
	return err
}
func (t *authTx) Enrollment(hash string) (auth.Enrollment, error) {
	var e auth.Enrollment
	err := t.tx.QueryRow(t.ctx, `SELECT token_hash,created_by::text,expires_at,used_at IS NOT NULL FROM device_enrollments WHERE token_hash=$1`, hash).Scan(&e.Hash, &e.CreatedBy, &e.Expires, &e.Used)
	return e, storageError(err)
}
func (t *authTx) SaveEnrollment(e auth.Enrollment) error {
	_, err := t.tx.Exec(t.ctx, `INSERT INTO device_enrollments(token_hash,created_by,expires_at,used_at) VALUES($1,$2,$3,CASE WHEN $4 THEN now() ELSE NULL END) ON CONFLICT(token_hash) DO UPDATE SET used_at=CASE WHEN $4 THEN now() ELSE device_enrollments.used_at END`, e.Hash, e.CreatedBy, e.Expires, e.Used)
	return err
}
func (t *authTx) Attempt(key string) (auth.Attempt, error) {
	var a auth.Attempt
	err := t.tx.QueryRow(t.ctx, `SELECT key,attempts,window_start,blocked_until FROM auth_attempts WHERE key=$1`, key).Scan(&a.Key, &a.Count, &a.Start, &a.Blocked)
	return a, storageError(err)
}
func (t *authTx) SaveAttempt(a auth.Attempt) error {
	_, err := t.tx.Exec(t.ctx, `INSERT INTO auth_attempts(key,attempts,window_start,blocked_until) VALUES($1,$2,$3,$4) ON CONFLICT(key) DO UPDATE SET attempts=$2,window_start=$3,blocked_until=$4`, a.Key, a.Count, a.Start, a.Blocked)
	return err
}
func (t *authTx) SaveProof(p auth.Proof) error {
	_, err := t.tx.Exec(t.ctx, `INSERT INTO reauth_proofs(token_hash,actor_id,action,target_id,revision,expires_at,session_hash) VALUES($1,$2,$3,$4,$5,$6,$7)`, p.Hash, p.ActorID, p.Action, p.TargetID, p.Revision, p.Expires, p.SessionHash)
	return err
}

// ConsumeProof is called inside the future operation's transaction; rollback also
// restores the proof. Session, actor, action, target and revision must all match.
func (t *authTx) ConsumeProof(p auth.Proof) (bool, error) {
	// now() is the transaction start, before a possible wait for the club lock.
	// Validate the proof and its session/device against the actual consumption time.
	tag, err := t.tx.Exec(t.ctx, `UPDATE reauth_proofs p SET used_at=clock_timestamp() WHERE token_hash=$1 AND actor_id=$2 AND action=$3 AND target_id=$4 AND revision=$5 AND session_hash=$6 AND used_at IS NULL AND expires_at>clock_timestamp() AND EXISTS(SELECT 1 FROM sessions s JOIN users u ON u.id=s.user_id JOIN trusted_devices d ON d.id=s.device_id WHERE s.token_hash=p.session_hash AND s.user_id=p.actor_id AND s.revoked_at IS NULL AND u.active AND u.auth_version=s.auth_version AND s.idle_expires_at>clock_timestamp() AND s.absolute_expires_at>clock_timestamp() AND d.revoked_at IS NULL AND d.expires_at>clock_timestamp())`, p.Hash, p.ActorID, p.Action, p.TargetID, p.Revision, p.SessionHash)
	return tag.RowsAffected() == 1, err
}
func (t *authTx) Audit(e auth.Event) error {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = t.tx.Exec(t.ctx, `INSERT INTO audit_events(actor_id,device_id,action,entity_id,request_id,success,reason,details) VALUES(NULLIF($1,'')::uuid,NULLIF($2,'')::uuid,$3,$4,$5,$6,$7,$8)`, e.ActorID, e.DeviceID, e.Action, e.EntityID, e.RequestID, e.Success, e.Reason, encoded)
	return err
}

var _ auth.Repository = AuthRepository{}

func (t *authTx) UserHasUnfinishedShift(userID string) (bool, error) {
	var exists bool
	err := t.tx.QueryRow(t.ctx, "SELECT EXISTS(SELECT 1 FROM shifts WHERE owner_id=$1 AND status IN ('opening','active','handover_pending'))", userID).Scan(&exists)
	return exists, err
}
