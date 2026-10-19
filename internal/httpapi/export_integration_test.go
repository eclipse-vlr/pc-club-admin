//go:build integration

package httpapi_test

import (
	"context"
	"encoding/csv"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/httpapi"
	"pc-club-admin/internal/shifts"
	"pc-club-admin/internal/storage/postgres"
)

func TestManagerCSVExports(t *testing.T) {
	pool := authDB(t)
	ctx := context.Background()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	access := auth.New(postgres.AuthRepository{Pool: pool}, "synthetic-export-pepper-at-least-32-characters")
	password := "synthetic-manager-password"
	check(access.Bootstrap(ctx, auth.UserInput{Name: "Export manager", Login: "manager", Password: password, Roles: []string{"manager"}}))
	login, err := access.Login(ctx, auth.Request{Source: "192.0.2.160"}, "manager", password, false)
	check(err)
	manager := auth.Request{SessionToken: login.Token, CSRF: login.CSRF, ActorID: login.Identity.User.ID, Source: "192.0.2.160"}
	owner, err := access.CreateUser(ctx, manager, auth.UserInput{Name: "  =SUM(A1)", PIN: "123456", Roles: []string{"admin"}})
	check(err)
	// A raw fixture name verifies whitespace-prefix handling independently of input trimming.
	_, err = pool.Exec(ctx, `UPDATE users SET display_name='  =SUM(A1)' WHERE id=$1`, owner.ID)
	check(err)
	work := shifts.New(postgres.ShiftRepository{Pool: pool}, access, "Europe/Moscow", false)
	amount := int64(9007199254740993)
	shift, _, err := work.Bootstrap(ctx, manager, shifts.BootstrapInput{OwnerID: owner.ID, BusinessDate: "2026-10-19", Kind: "day", InitialCash: &amount, Reason: "Synthetic CSV test"}, "synthetic-export-bootstrap")
	check(err)
	_, err = pool.Exec(ctx, `UPDATE cash_sessions SET langame_cash_amount_kopecks=-1,extra_cash_refunds_kopecks=0 WHERE shift_id=$1`, shift.ID)
	check(err)
	var cashID, movement string
	check(pool.QueryRow(ctx, `SELECT id::text FROM cash_sessions WHERE shift_id=$1`, shift.ID).Scan(&cashID))
	check(pool.QueryRow(ctx, `INSERT INTO cash_movements(cash_session_id,kind,amount_kopecks,reason,occurred_at,actor_id) VALUES($1,'collection',100,'synthetic',now(),$2) RETURNING id::text`, cashID, owner.ID).Scan(&movement))
	_, err = pool.Exec(ctx, `INSERT INTO cash_movements(cash_session_id,kind,amount_kopecks,reversal_of,reason,occurred_at,actor_id) VALUES($1,'reversal',100,$2,'synthetic reversal',now(),$3)`, cashID, movement, owner.ID)
	check(err)
	c := config.Config{Environment: "test", Origin: "http://127.0.0.1:8080", Timezone: "Europe/Moscow", CashPolicyStatus: "unverified"}
	handler := httpapi.NewWithShifts(c, func(context.Context) error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)), access, work)
	request := func(path, token string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "192.0.2.160:1000"
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "pcclub_session", Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: got %d want %d: %s", path, w.Code, want, w.Body.String())
		}
		return w
	}
	const query = "?date_from=2026-10-19&date_to=2026-10-19"
	var before, after time.Time
	check(pool.QueryRow(ctx, `SELECT idle_expires_at FROM sessions WHERE token_hash=$1`, auth.Digest(login.Token)).Scan(&before))
	for _, kind := range []string{"shifts", "cash"} {
		w := request("/api/v1/exports/"+kind+".csv"+query, login.Token, 200)
		if w.Header().Get("Content-Type") != "text/csv; charset=utf-8" || w.Header().Get("Content-Disposition") != `attachment; filename="`+kind+`.csv"` {
			t.Fatal("missing download headers")
		}
		reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\xef\xbb\xbf")))
		reader.Comma = ';'
		rows, err := reader.ReadAll()
		check(err)
		if len(rows) != 2 || rows[1][5] != "'  =SUM(A1)" || rows[1][len(rows[1])-1] != "unverified_test_only" {
			t.Fatalf("unsafe/incorrect export: %q", rows)
		}
		if kind == "cash" {
			if rows[1][6] != "90071992547409.93" || rows[1][7] != "" || rows[1][8] != "-0.01" || rows[1][9] != "0.00" || rows[1][10] != "" || rows[1][12] != "0.00" || rows[1][13] != "draft-v1" {
				t.Fatalf("incorrect cash: %q", rows[1])
			}
		}
	}
	check(pool.QueryRow(ctx, `SELECT idle_expires_at FROM sessions WHERE token_hash=$1`, auth.Digest(login.Token)).Scan(&after))
	if !before.Equal(after) {
		t.Fatal("exports extended session idle TTL")
	}
	var auditCount int
	check(pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='csv_exported' AND success`).Scan(&auditCount))
	if auditCount != 2 {
		t.Fatal("successful exports not audited")
	}
	request("/api/v1/exports/cash.csv"+query, "", 401)
	request("/api/v1/exports/cash.csv", login.Token, 400)
	request("/api/v1/exports/cash.csv"+query+"&date_from=2026-10-19", login.Token, 400)
	request("/api/v1/exports/cash.csv"+query+"&owner_id=bad", login.Token, 400)
	w := request("/api/v1/exports/cash.csv?date_from=2026-10-20&date_to=2026-10-20", login.Token, 200)
	if strings.Count(w.Body.String(), "\r\n") != 1 {
		t.Fatal("empty export must contain header only")
	}
	_, err = pool.Exec(ctx, `UPDATE user_roles SET role='admin' WHERE user_id=$1 AND role='manager'`, login.Identity.User.ID)
	check(err)
	request("/api/v1/exports/cash.csv"+query, login.Token, 403)
	// The limit returns an explicit error rather than a truncated CSV.
	_, err = pool.Exec(ctx, `UPDATE user_roles SET role='manager' WHERE user_id=$1`, login.Identity.User.ID)
	check(err)
	_, err = pool.Exec(ctx, `INSERT INTO shifts(owner_id,business_date,kind,status,planned_start_at,planned_end_at,template_version,created_by)
 SELECT $1,'2026-10-18','day','cancelled','2026-10-18T06:00:00Z','2026-10-18T18:00:00Z',1,$1 FROM generate_series(1,5001)`, owner.ID)
	check(err)
	w = request("/api/v1/exports/shifts.csv?date_from=2026-10-18&date_to=2026-10-18", login.Token, 422)
	if w.Header().Get("Content-Disposition") != "" || !strings.Contains(w.Body.String(), "EXPORT_TOO_LARGE") {
		t.Fatal("oversized export became a partial file")
	}
	// Database failure cannot produce a 200 CSV with partial rows.
	_, err = pool.Exec(ctx, `UPDATE user_roles SET role='manager' WHERE user_id=$1`, login.Identity.User.ID)
	check(err)
	_, err = pool.Exec(ctx, `ALTER TABLE cash_sessions RENAME TO unavailable_cash_sessions`)
	check(err)
	w = request("/api/v1/exports/cash.csv"+query, login.Token, 503)
	if strings.Contains(w.Header().Get("Content-Type"), "csv") || w.Header().Get("Content-Disposition") != "" {
		t.Fatal("database error presented as download")
	}
}
