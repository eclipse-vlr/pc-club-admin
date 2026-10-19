package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"pc-club-admin/migrations"
)

const schemaVersion = 1
const migrationName = "000001_foundation.up.sql"

func migration() ([]byte, string, error) {
	sql, err := migrations.Files.ReadFile(migrationName)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(sql)
	return sql, hex.EncodeToString(sum[:]), nil
}

// Migrate is an explicit release step, never called by the API process.
// A transaction-level advisory lock serializes concurrent migration commands.
func Migrate(ctx context.Context, pool *pgxpool.Pool, timezone string) error {
	sql, checksum, err := migration()
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(728391004)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version integer PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	var count, maxVersion int
	if err = tx.QueryRow(ctx, "SELECT count(*), COALESCE(max(version),0) FROM schema_migrations").Scan(&count, &maxVersion); err != nil {
		return err
	}
	if count > 1 || maxVersion > schemaVersion {
		return fmt.Errorf("incompatible schema")
	}
	if count == 0 {
		if _, err = tx.Exec(ctx, string(sql)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES ($1,$2)", schemaVersion, checksum); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO club_settings(id,timezone) VALUES (1,$1)", timezone); err != nil {
			return err
		}
	} else {
		var actual string
		if err = tx.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE version=$1", schemaVersion).Scan(&actual); err != nil {
			return err
		}
		if actual != checksum {
			return fmt.Errorf("migration checksum mismatch")
		}
	}
	return tx.Commit(ctx)
}

// CheckSchema rejects missing, older, newer or modified schemas and config drift.
func CheckSchema(ctx context.Context, pool *pgxpool.Pool, timezone string) error {
	_, checksum, err := migration()
	if err != nil {
		return err
	}
	var count int
	var version int
	var actual string
	err = pool.QueryRow(ctx, `SELECT count(*) OVER (), version, checksum FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&count, &version, &actual)
	if err != nil {
		return err
	}
	if count != 1 || version != schemaVersion || actual != checksum {
		return fmt.Errorf("incompatible schema")
	}
	var configuredTimezone, status, policyVersion string
	err = pool.QueryRow(ctx, `SELECT timezone,cash_policy_status,cash_policy_version FROM club_settings WHERE id=1`).Scan(&configuredTimezone, &status, &policyVersion)
	if err != nil {
		return err
	}
	if configuredTimezone != timezone || status != "unverified" || policyVersion != "draft-v1" {
		return fmt.Errorf("club settings differ from supported configuration")
	}
	return nil
}
