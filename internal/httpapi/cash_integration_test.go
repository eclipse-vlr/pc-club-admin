//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
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
	"pc-club-admin/migrations"
)

func TestCashTestModeBoundary(t *testing.T) {
	pool := authDB(t)
	ctx := context.Background()
	access := auth.New(postgres.AuthRepository{Pool: pool}, "synthetic-cash-pepper-at-least-32-characters")
	password := "synthetic-manager-password"
	if err := access.Bootstrap(ctx, auth.UserInput{Name: "Тестовый управляющий", Login: "manager", Password: password, Roles: []string{"manager"}}); err != nil {
		t.Fatal(err)
	}
	c := config.Config{Environment: "test", Origin: "http://127.0.0.1:8080", Timezone: "Europe/Moscow", CashPolicyStatus: "unverified"}
	work := shifts.New(postgres.ShiftRepository{Pool: pool}, access, c.Timezone, false)
	handler := httpapi.NewWithShifts(c, func(ctx context.Context) error { return postgres.CheckSchema(ctx, pool, c.Timezone) }, slog.New(slog.NewTextHandler(io.Discard, nil)), access, work)
	browserFor := func(source string) *browser {
		return &browser{t: t, h: handler, cookies: map[string]*http.Cookie{}, source: source}
	}
	manager := browserFor("192.0.2.80")
	manager.call("GET", "/api/v1/auth/csrf", nil, 200)
	manager.call("POST", "/api/v1/auth/manager-login", map[string]any{"login": "manager", "password": password}, 200)
	owner := manager.call("POST", "/api/v1/users", map[string]any{"display_name": "Тестовый администратор", "roles": []string{"admin"}, "pin": "123456"}, 201)
	manager.key = "cash-bootstrap-key-001"
	draft := manager.call("POST", "/api/v1/shifts/bootstrap", map[string]any{"owner_id": owner["id"], "business_date": "2026-10-19", "kind": "day", "opening_cash_kopecks": 1000000, "reason": "Синтетический первый остаток"}, 201)
	manager.key = ""
	shiftID := draft["id"].(string)
	path := "/api/v1/shifts/" + shiftID
	code := manager.call("POST", "/api/v1/devices/enrollment", map[string]any{"password": password}, 201)["code"].(string)
	club := browserFor("192.0.2.81")
	club.call("GET", "/api/v1/auth/csrf", nil, 200)
	club.call("POST", "/api/v1/devices/register", map[string]any{"code": code, "label": "Кассовый тест"}, 204)
	club.call("POST", "/api/v1/auth/pin-login", map[string]any{"user_id": owner["id"], "pin": "123456"}, 200)
	club.key = "activate-incomplete-key"
	club.call("POST", path+"/activate", map[string]any{"counted_opening_kopecks": 1000000, "version": 1, "reauth_proof": ""}, 422)
	for _, v := range draft["checklist"].([]any) {
		entry := v.(map[string]any)
		if entry["phase"] == "acceptance" {
			draft = club.call("PATCH", path+"/checklist/"+entry["code"].(string), map[string]any{"answer": "yes", "version": 1}, 200)
		}
	}
	version := draft["version"]
	club.key = "activate-no-count-key"
	club.call("POST", path+"/activate", map[string]any{"version": version}, 422)
	proof := func(action, target string, revision any) string {
		return club.call("POST", "/api/v1/auth/reauth", map[string]any{"pin": "123456", "action": action, "target_id": target, "revision": revision}, 201)["proof"].(string)
	}
	activationProof := proof("activate_first_shift", shiftID, version)
	activation := map[string]any{"counted_opening_kopecks": 999999, "version": version, "reauth_proof": activationProof}
	club.key = "activate-mismatch-key"
	club.call("POST", path+"/activate", activation, 422)
	activation["counted_opening_kopecks"] = 1000000
	club.key = "activate-correct-key"
	active := club.call("POST", path+"/activate", activation, 200)
	if active["status"] != "active" || active["actual_start_at"] == nil {
		t.Fatal("first personal count did not activate shift")
	}
	club.call("POST", path+"/activate", activation, 200)
	var activations int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='first_shift_activated'").Scan(&activations); err != nil {
		t.Fatal(err)
	}
	if activations != 1 {
		t.Fatal("duplicate first activation")
	}
	cashVersion := active["cash"].(map[string]any)["session"].(map[string]any)["version"]
	input := map[string]any{"langame_cash_amount_kopecks": 500000, "extra_cash_refunds_kopecks": 0, "extra_cash_expenses_kopecks": 0, "counted_closing_kopecks": 700000, "version": cashVersion}
	club.key = "cash-patch-initial-key"
	view := club.call("PATCH", path+"/cash", input, 200)
	if view["summary"].(map[string]any)["difference_kopecks"] != float64(-800000) {
		t.Fatal("cash mismatch not calculated on server")
	}
	cashVersion = view["session"].(map[string]any)["version"]
	collectionProof := proof("collection", shiftID, cashVersion)
	collection := map[string]any{"amount_kopecks": 800000, "reason": "Синтетическая инкассация", "recipient": "Тестовый получатель", "occurred_at": time.Date(2026, 10, 19, 18, 0, 0, 0, time.UTC).Format(time.RFC3339), "reauth_proof": collectionProof, "version": cashVersion}
	club.key = "cash-collection-first-key"
	// Two concurrent identical HTTP requests share one key and one proof.
	raw, err := json.Marshal(collection)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() {
			<-start
			req := httptest.NewRequest("POST", path+"/cash/collections", bytes.NewReader(raw))
			req.RemoteAddr = "192.0.2.81:1234"
			req.Header.Set("Origin", c.Origin)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", club.csrf)
			req.Header.Set("X-Actor-ID", club.actor)
			req.Header.Set("Idempotency-Key", club.key)
			for _, cookie := range club.cookies {
				req.AddCookie(cookie)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			responses <- response
		}()
	}
	close(start)
	for range 2 {
		response := <-responses
		if response.Code != 201 {
			t.Fatalf("concurrent collection: %d %s", response.Code, response.Body.String())
		}
	}
	view = club.call("POST", path+"/cash/collections", collection, 201)
	summary := view["summary"].(map[string]any)
	session := view["session"].(map[string]any)
	if summary["expected_closing_kopecks"] != float64(700000) || summary["difference_kopecks"] != float64(0) || summary["verified"] != false {
		t.Fatalf("T09: %+v", summary)
	}
	replay := club.call("POST", path+"/cash/collections", collection, 201)
	if len(replay["session"].(map[string]any)["movements"].([]any)) != 1 {
		t.Fatal("T19: duplicate collection")
	}
	collection["amount_kopecks"] = 800001
	club.call("POST", path+"/cash/collections", collection, 409)
	collection["amount_kopecks"] = 800000
	movements := session["movements"].([]any)
	movementID := movements[0].(map[string]any)["id"].(string)
	cashVersion = session["version"]
	manager.key = "manager-cash-bypass-key"
	manager.call("PATCH", path+"/cash", input, 403)
	// Wrong action/revision cannot spend a proof for another monetary operation.
	wrong := proof("collection", shiftID, cashVersion)
	club.key = "wrong-proof-reverse-key"
	club.call("POST", path+"/cash/collections/"+movementID+"/reverse", map[string]any{"reason": "Тест", "reauth_proof": wrong, "version": cashVersion}, 403)
	reversal := map[string]any{"reason": "Исправление тестовой записи", "reauth_proof": proof("reverse_collection", movementID, cashVersion), "version": cashVersion}
	club.key = "reverse-collection-key"
	view = club.call("POST", path+"/cash/collections/"+movementID+"/reverse", reversal, 201)
	if view["summary"].(map[string]any)["collection_total_kopecks"] != float64(0) || len(view["session"].(map[string]any)["movements"].([]any)) != 2 {
		t.Fatal("reversal deleted history or incorrect total")
	}
	club.call("POST", path+"/cash/collections/"+movementID+"/reverse", reversal, 201)
	cashVersion = view["session"].(map[string]any)["version"]
	reversal["version"] = cashVersion
	club.key = "reverse-again-new-key"
	club.call("POST", path+"/cash/collections/"+movementID+"/reverse", reversal, 409)
	input["version"] = cashVersion
	input["langame_cash_amount_kopecks"] = int64(math.MaxInt64)
	club.key = "cash-overflow-input-key"
	club.call("PATCH", path+"/cash", input, 400)
	input["langame_cash_amount_kopecks"] = 500000
	input["counted_closing_kopecks"] = 1499999
	club.key = "cash-one-kopeck-key"
	view = club.call("PATCH", path+"/cash", input, 200)
	if view["summary"].(map[string]any)["difference_kopecks"] != float64(-1) {
		t.Fatal("T10: one kopeck disappeared")
	}
	cashVersion = view["session"].(map[string]any)["version"]
	club.key = "cash-missing-fields-key"
	view = club.call("PATCH", path+"/cash", map[string]any{"version": cashVersion}, 200)
	if view["summary"].(map[string]any)["expected_closing_kopecks"] != nil {
		t.Fatal("T08: blank became zero")
	}
	cashVersion = view["session"].(map[string]any)["version"]
	collection["version"] = cashVersion
	collection["reauth_proof"] = proof("collection", shiftID, cashVersion)
	collection["amount_kopecks"] = 1
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_events ADD CONSTRAINT fail_cash_audit CHECK(false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	club.key = "cash-audit-rollback-key"
	club.call("POST", path+"/cash/collections", collection, 503)
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_events DROP CONSTRAINT fail_cash_audit"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM cash_movements").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal("cash movement survived audit failure")
	}
	var used bool
	if err := pool.QueryRow(ctx, "SELECT used_at IS NOT NULL FROM reauth_proofs WHERE token_hash=$1", auth.Digest(collection["reauth_proof"].(string))).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used {
		t.Fatal("proof spent despite rollback")
	}
	club.call("POST", path+"/cash/collections", collection, 201) // same key works: failed attempt did not reserve it
	if _, err := pool.Exec(ctx, "DELETE FROM cash_movements WHERE id=$1", movementID); err == nil {
		t.Fatal("append-only ledger allowed delete")
	}
	if _, err := pool.Exec(ctx, "UPDATE shifts SET status='closed',actual_end_at=clock_timestamp() WHERE id=$1", shiftID); err != nil {
		t.Fatal(err)
	}
	club.key = "closed-cash-update-key"
	input["version"] = cashVersion
	club.call("PATCH", path+"/cash", input, 409)
	production := shifts.New(postgres.ShiftRepository{Pool: pool}, access, c.Timezone, true)
	club.h = httpapi.NewWithShifts(c, func(context.Context) error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)), access, production)
	club.key = "production-cash-update-key"
	club.call("PATCH", path+"/cash", input, 503)
	var auditText string
	if err := pool.QueryRow(ctx, "SELECT string_agg(row_to_json(audit_events)::text,',') FROM audit_events").Scan(&auditText); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{activationProof, collectionProof, wrong} {
		if strings.Contains(auditText, secret) {
			t.Fatal("proof leaked into audit")
		}
	}
}

func TestCashUpgradeDoesNotAcceptManagerAmount(t *testing.T) {
	pool := authDB(t)
	ctx := context.Background()
	// The isolated schema is deliberately moved back to v3 to test v4 backfill.
	down, err := migrations.Files.ReadFile("000004_cash.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM schema_migrations WHERE version=4"); err != nil {
		t.Fatal(err)
	}
	var userID, shiftID string
	if err := pool.QueryRow(ctx, "INSERT INTO users(display_name) VALUES('Синтетический владелец') RETURNING id::text").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO shifts(owner_id,business_date,kind,status,planned_start_at,planned_end_at,template_version,initial_cash_kopecks,bootstrap_reason,created_by) VALUES($1,'2026-10-19','day','opening','2026-10-19T09:00:00+03:00','2026-10-19T21:00:00+03:00',1,1000000,'Синтетический остаток',$1) RETURNING id::text`, userID).Scan(&shiftID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, pool, "Europe/Moscow"); err != nil {
		t.Fatal(err)
	}
	var opening int64
	var confirmed *time.Time
	var counted *int64
	if err := pool.QueryRow(ctx, "SELECT opening_cash_kopecks,opening_confirmed_at,counted_opening_kopecks FROM cash_sessions WHERE shift_id=$1", shiftID).Scan(&opening, &confirmed, &counted); err != nil {
		t.Fatal(err)
	}
	if opening != 1000000 || confirmed != nil || counted != nil {
		t.Fatal("upgrade silently accepted manager amount")
	}
}
