//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/httpapi"
	"pc-club-admin/internal/shifts"
	"pc-club-admin/internal/storage/postgres"
)

func TestHandoverTwoPersonalLoginsAtomicBoundary(t *testing.T) {
	pool := authDB(t)
	ctx := context.Background()
	access := auth.New(postgres.AuthRepository{Pool: pool}, "synthetic-handover-pepper-at-least-32-characters")
	password := "synthetic-manager-password"
	if err := access.Bootstrap(ctx, auth.UserInput{Name: "Тестовый управляющий", Login: "manager", Password: password, Roles: []string{"manager"}}); err != nil {
		t.Fatal(err)
	}
	c := config.Config{Environment: "test", Origin: "http://127.0.0.1:8080", Timezone: "Europe/Moscow", CashPolicyStatus: "unverified"}
	work := shifts.New(postgres.ShiftRepository{Pool: pool}, access, c.Timezone, false)
	handler := httpapi.NewWithShifts(c, func(ctx context.Context) error { return postgres.CheckSchema(ctx, pool, c.Timezone) }, slog.New(slog.NewTextHandler(io.Discard, nil)), access, work)
	manager := &browser{t: t, h: handler, cookies: map[string]*http.Cookie{}, source: "192.0.2.100"}
	manager.call("GET", "/api/v1/auth/csrf", nil, 200)
	manager.call("POST", "/api/v1/auth/manager-login", map[string]any{"login": "manager", "password": password}, 200)
	owner := manager.call("POST", "/api/v1/users", map[string]any{"display_name": "Сдающий", "roles": []string{"admin"}, "pin": "123456"}, 201)["id"].(string)
	receiver := manager.call("POST", "/api/v1/users", map[string]any{"display_name": "Принимающий", "roles": []string{"admin"}, "pin": "654321"}, 201)["id"].(string)
	stranger := manager.call("POST", "/api/v1/users", map[string]any{"display_name": "Другой сотрудник", "roles": []string{"admin"}, "pin": "112233"}, 201)["id"].(string)
	manager.key = "handover-bootstrap-key"
	draft := manager.call("POST", "/api/v1/shifts/bootstrap", map[string]any{"owner_id": owner, "business_date": "2026-10-19", "kind": "day", "opening_cash_kopecks": 1000000, "reason": "Тестовый остаток"}, 201)
	id := draft["id"].(string)
	path := "/api/v1/shifts/" + id
	club := &browser{t: t, h: handler, cookies: map[string]*http.Cookie{}, source: "192.0.2.101"}
	register := func(b *browser) {
		code := manager.call("POST", "/api/v1/devices/enrollment", map[string]any{"password": password}, 201)["code"]
		b.call("GET", "/api/v1/auth/csrf", nil, 200)
		b.call("POST", "/api/v1/devices/register", map[string]any{"code": code, "label": "Тест передачи"}, 204)
	}
	register(club)
	login := func(user, pin string) {
		club.call("POST", "/api/v1/auth/pin-login", map[string]any{"user_id": user, "pin": pin}, 200)
	}
	proof := func(pin, action, target string, version any) string {
		return club.call("POST", "/api/v1/auth/reauth", map[string]any{"pin": pin, "action": action, "target_id": target, "revision": version}, 201)["proof"].(string)
	}
	answer := func(shift map[string]any, phase string) map[string]any {
		for _, v := range shift["checklist"].([]any) {
			e := v.(map[string]any)
			if e["phase"] != phase {
				continue
			}
			a := "yes"
			if e["code"] == "conflicts_recorded" {
				a = "no"
			}
			shift = club.call("PATCH", "/api/v1/shifts/"+shift["id"].(string)+"/checklist/"+e["code"].(string), map[string]any{"answer": a, "version": e["version"]}, 200)
		}
		return shift
	}
	login(owner, "123456")
	draft = answer(draft, "acceptance")
	club.key = "handover-first-activate"
	draft = club.call("POST", path+"/activate", map[string]any{"version": draft["version"], "counted_opening_kopecks": 1000000, "reauth_proof": proof("123456", "activate_first_shift", id, draft["version"])}, 200)
	setCash := func(count int) {
		draft = club.call("GET", path, nil, 200)
		cv := draft["cash"].(map[string]any)["session"].(map[string]any)["version"]
		club.key = fmt.Sprintf("handover-cash-%v-%v", cv, count)
		club.call("PATCH", path+"/cash", map[string]any{"version": cv, "langame_cash_amount_kopecks": 500000, "extra_cash_refunds_kopecks": 0, "extra_cash_expenses_kopecks": 0, "counted_closing_kopecks": count}, 200)
		draft = club.call("GET", path, nil, 200)
	}
	setCash(1499999)
	draft = answer(draft, "closing")
	prepare := func(want int) map[string]any {
		club.key = fmt.Sprintf("handover-prepare-%v", draft["version"])
		return club.call("POST", path+"/handovers", map[string]any{"receiver_id": receiver, "version": draft["version"], "reauth_proof": proof("123456", "prepare_handover", id, draft["version"])}, want)
	}
	prepare(422) // One kopeck blocks surrender; owner remains active.
	setCash(1500000)
	// A manager or self-selected receiver cannot bypass personal surrender.
	manager.key = "handover-manager-prepare"
	manager.call("POST", path+"/handovers", map[string]any{"receiver_id": receiver, "version": draft["version"]}, 403)
	club.key = "handover-self-prepare"
	club.call("POST", path+"/handovers", map[string]any{"receiver_id": owner, "version": draft["version"]}, 422)
	// An ordinary no still blocks a complete closing checklist.
	draft = club.call("PATCH", path+"/checklist/close_p", map[string]any{"answer": "no", "version": 2}, 200)
	prepare(422)
	draft = club.call("PATCH", path+"/checklist/close_p", map[string]any{"answer": "yes", "version": 3}, 200)
	// A recorded fault, without a repair workflow, is sufficient.
	note := club.call("POST", path+"/notes", map[string]any{"category": "equipment", "checklist_code": "close_x", "object_label": "Тестовый ПК", "text": "Поломка записана, ремонт не завершён"}, 201)
	draft = club.call("GET", path, nil, 200)
	h := prepare(201)
	hID := h["id"].(string)
	hp := "/api/v1/handovers/" + hID
	nextID := h["to_shift_id"].(string)
	club.call("GET", "/api/v1/auth/me", nil, 401)
	manager.key = "handover-manager-accept"
	manager.call("POST", hp+"/accept", map[string]any{"revision": h["revision"], "version": h["version"], "shift_version": 1}, 403)
	if h["status"] != "pending" {
		t.Fatal("preparation completed surrender")
	}
	login(stranger, "112233")
	club.call("GET", hp, nil, 404)
	login(receiver, "654321")
	// Intended recipient is protected against being disabled while pending.
	manager.call("PATCH", "/api/v1/users/"+receiver, map[string]any{"display_name": "Принимающий", "active": false, "version": 1}, 422)
	// A different trusted browser cannot sign the receipt.
	other := &browser{t: t, h: handler, cookies: map[string]*http.Cookie{}, source: "192.0.2.102"}
	register(other)
	other.call("POST", "/api/v1/auth/pin-login", map[string]any{"user_id": receiver, "pin": "654321"}, 200)
	other.key = "handover-other-device"
	other.call("PATCH", hp+"/receipt", map[string]any{"version": h["version"], "revision": h["revision"], "counted_cash_kopecks": 1500000}, 403)
	next := club.call("GET", "/api/v1/shifts/current", nil, 200)["shift"].(map[string]any)
	if next["id"] != nextID || next["handover_parent_id"] != id || next["cash"] != nil {
		t.Fatal("receiver draft not found")
	}
	club.key = "handover-no-first-activate"
	club.call("POST", "/api/v1/shifts/"+nextID+"/activate", map[string]any{"version": next["version"], "counted_opening_kopecks": 0}, 409)
	other.call("PATCH", "/api/v1/shifts/"+nextID+"/checklist/accept_d", map[string]any{"answer": "yes", "version": 1}, 403)
	_ = answer(next, "acceptance")
	club.key = "handover-wrong-count"
	h = club.call("PATCH", hp+"/receipt", map[string]any{"version": h["version"], "revision": h["revision"], "counted_cash_kopecks": 1499999}, 200)
	next = club.call("GET", "/api/v1/shifts/"+nextID, nil, 200)
	accept := func(token string) map[string]any {
		return map[string]any{"revision": h["revision"], "version": h["version"], "shift_version": next["version"], "reauth_proof": token}
	}
	club.key = "handover-accept-mismatch"
	club.call("POST", hp+"/accept", accept(proof("654321", "accept_handover", hID, next["version"])), 422)
	// Cancel after receiver work; a new attempt must not reuse old count/checklist/acks.
	login(owner, "123456")
	club.key = "handover-cancel-first"
	cancelled := club.call("POST", hp+"/cancel", map[string]any{"version": h["version"], "reason": "Перепроверить отчёт"}, 200)
	if cancelled["status"] != "cancelled" {
		t.Fatal("cancel failed")
	}
	club.call("POST", hp+"/cancel", map[string]any{"version": h["version"], "reason": "Перепроверить отчёт"}, 200)
	draft = club.call("GET", path, nil, 200)
	h = prepare(201)
	hID = h["id"].(string)
	hp = "/api/v1/handovers/" + hID
	nextID = h["to_shift_id"].(string)
	if h["revision"] != float64(2) || h["counted_cash_kopecks"] != nil {
		t.Fatal("old receipt carried over")
	}
	login(receiver, "654321")
	club.key = "handover-old-accept-key"
	club.call("POST", "/api/v1/handovers/"+cancelled["id"].(string)+"/accept", map[string]any{"revision": 1, "version": 1, "shift_version": 1}, 409)
	next = club.call("GET", "/api/v1/shifts/"+nextID, nil, 200)
	if next["checklist"].([]any)[0].(map[string]any)["answer"] != "unanswered" {
		t.Fatal("old checklist carried over")
	}
	next = answer(next, "acceptance")
	club.key = "handover-correct-count"
	h = club.call("PATCH", hp+"/receipt", map[string]any{"version": h["version"], "revision": h["revision"], "counted_cash_kopecks": 1500000}, 200)
	next = club.call("GET", "/api/v1/shifts/"+nextID, nil, 200)
	club.key = "handover-no-note-ack"
	club.call("POST", hp+"/accept", accept(""), 422)
	club.key = "handover-note-ack-key"
	h = club.call("POST", hp+"/acknowledgements", map[string]any{"version": h["version"], "revision": h["revision"], "note_ids": []string{note["id"].(string)}}, 200)
	next = club.call("GET", "/api/v1/shifts/"+nextID, nil, 200)
	staleToken := proof("654321", "accept_handover", hID, next["version"])
	next = club.call("PATCH", "/api/v1/shifts/"+nextID+"/checklist/accept_d", map[string]any{"answer": "yes", "version": 2}, 200)
	club.key = "handover-stale-proof-key"
	club.call("POST", hp+"/accept", accept(staleToken), 403)
	token := proof("654321", "accept_handover", hID, next["version"])
	input := accept(token)
	// A failure after business writes must roll back proof, statuses, money and receipt.
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_events ADD CONSTRAINT fail_handover_audit CHECK(false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	club.key = "handover-atomic-accept-key"
	club.call("POST", hp+"/accept", input, 503)
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_events DROP CONSTRAINT fail_handover_audit`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM shifts WHERE id=$1 AND status='handover_pending' AND actual_end_at IS NULL`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("outgoing changed after rollback", err)
	}
	// Different-key concurrent accepts have one success; replay of the winner is safe.
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	for index := range 2 {
		go func() {
			<-start
			req := httptest.NewRequest("POST", hp+"/accept", bytes.NewReader(raw))
			req.RemoteAddr = "192.0.2.101:1234"
			req.Header.Set("Origin", c.Origin)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", club.csrf)
			req.Header.Set("X-Actor-ID", club.actor)
			req.Header.Set("Idempotency-Key", fmt.Sprintf("handover-concurrent-key-%d", index))
			responseKey := fmt.Sprintf("handover-concurrent-key-%d", index)
			for _, cookie := range club.cookies {
				req.AddCookie(cookie)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			response.Header().Set("Test-Request-Key", responseKey)
			responses <- response
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		response := <-responses
		switch response.Code {
		case 200:
			successes++
			club.key = response.Header().Get("Test-Request-Key")
		case 409:
			conflicts++
		default:
			t.Fatalf("concurrent accept: %d %s", response.Code, response.Body.String())
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("different-key accept requests did not serialize")
	}
	h = club.call("POST", hp+"/accept", input, 200)
	if h["status"] != "accepted" {
		t.Fatal("receipt not accepted")
	}
	var same bool
	if err := pool.QueryRow(ctx, `SELECT a.status='closed' AND b.status='active' AND a.actual_end_at=b.actual_start_at AND b.previous_shift_id=a.id AND c.opening_cash_kopecks=1500000 AND c.counted_opening_kopecks=1500000 FROM shifts a JOIN shifts b ON b.id=$2 JOIN cash_sessions c ON c.shift_id=b.id WHERE a.id=$1`, id, nextID).Scan(&same); err != nil || !same {
		t.Fatal("boundary or transferred amount incorrect", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='handover_accepted' AND success`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate acceptance audit", err)
	}
	club.key = "handover-repeat-other-key"
	club.call("POST", hp+"/accept", input, 409)
	club.call("PATCH", path+"/checklist/close_p", map[string]any{"answer": "yes", "version": 2}, 403)
	club.call("PATCH", "/api/v1/shifts/"+nextID+"/checklist/accept_d", map[string]any{"answer": "no", "version": 3}, 409)
	login(owner, "123456")
	club.call("PATCH", path+"/checklist/close_p", map[string]any{"answer": "yes", "version": 4}, 409)
	if _, err := pool.Exec(ctx, `UPDATE handovers SET snapshot='{}'::jsonb WHERE id=$1`, hID); err == nil {
		t.Fatal("final snapshot is mutable")
	}

}
