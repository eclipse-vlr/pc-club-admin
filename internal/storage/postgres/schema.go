package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pc-club-admin/migrations"
)

const schemaVersion = 7

var migrationNames = []string{"000001_foundation.up.sql", "000002_auth.up.sql", "000003_checklists.up.sql", "000004_cash.up.sql", "000005_excel_sync.up.sql", "000006_handovers.up.sql", "000007_history_indexes.up.sql"}

func migration(name string) ([]byte, string, error) {
	sql, err := migrations.Files.ReadFile(name)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(sql)
	return sql, hex.EncodeToString(sum[:]), nil
}

// Migrate is an explicit release step, never called by the API process.
// A transaction-level advisory lock serializes concurrent migration commands.
func Migrate(ctx context.Context, pool *pgxpool.Pool, timezone string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(728391004)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version integer PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}

	rows, err := tx.Query(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return err
	}
	applied := map[int]string{}
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			return err
		}
		applied[version] = checksum
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(applied) > schemaVersion {
		return fmt.Errorf("incompatible schema")
	}
	for version := range applied {
		if version < 1 || version > schemaVersion {
			return fmt.Errorf("incompatible schema")
		}
	}
	for i, name := range migrationNames {
		version := i + 1
		sql, checksum, err := migration(name)
		if err != nil {
			return err
		}
		if actual, ok := applied[version]; ok {
			if actual != checksum {
				return fmt.Errorf("migration checksum mismatch")
			}
			continue
		}
		if version <= len(applied) {
			return fmt.Errorf("noncontiguous schema")
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)", version, checksum); err != nil {
			return err
		}
		if version == 1 {
			if _, err := tx.Exec(ctx, "INSERT INTO club_settings(id,timezone) VALUES(1,$1)", timezone); err != nil {
				return err
			}
		}
	}
	var storedTimezone string
	if err := tx.QueryRow(ctx, "SELECT timezone FROM club_settings WHERE id=1").Scan(&storedTimezone); err != nil {
		return err
	}
	if storedTimezone != timezone {
		return fmt.Errorf("club timezone differs from configuration")
	}
	return tx.Commit(ctx)
}

// CheckSchema rejects missing, older, newer or modified schemas and config drift.
func CheckSchema(ctx context.Context, pool *pgxpool.Pool, timezone string) error {

	rows, err := pool.Query(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var version int
		var actual string
		if err := rows.Scan(&version, &actual); err != nil {
			rows.Close()
			return err
		}
		if count >= len(migrationNames) || version != count+1 {
			rows.Close()
			return fmt.Errorf("incompatible schema")
		}
		_, checksum, err := migration(migrationNames[count])
		if err != nil {
			rows.Close()
			return err
		}
		if actual != checksum {
			rows.Close()
			return fmt.Errorf("migration checksum mismatch")
		}
		count++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if count != schemaVersion {
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
