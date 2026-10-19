//go:build integration

package postgres

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrationLifecycle(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	u, err := url.Parse(raw)
	if err != nil || raw == "" || !strings.HasSuffix(strings.TrimPrefix(u.Path, "/"), "_test") {
		t.Fatal("TEST_DATABASE_URL must explicitly name a dedicated *_test database")
	}
	if os.Getenv("APP_ENV") == "production" {
		t.Fatal("integration tests refuse APP_ENV=production")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("foundation_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if CheckSchema(ctx, pool, "Europe/Moscow") == nil {
		t.Fatal("unmigrated schema accepted")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- Migrate(ctx, pool, "Europe/Moscow") }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := CheckSchema(ctx, pool, "Europe/Moscow"); err != nil {
		t.Fatal(err)
	}
	if err := CheckSchema(ctx, pool, "Asia/Tokyo"); err == nil {
		t.Fatal("timezone drift accepted")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO club_settings(id,timezone) VALUES(2,'UTC')"); err == nil {
		t.Fatal("second club accepted")
	}
	if _, err := pool.Exec(ctx, "UPDATE schema_migrations SET checksum='modified'"); err != nil {
		t.Fatal(err)
	}
	if CheckSchema(ctx, pool, "Europe/Moscow") == nil {
		t.Fatal("changed migration accepted")
	}
	if Migrate(ctx, pool, "Europe/Moscow") == nil {
		t.Fatal("changed migration reapplied")
	}
}
