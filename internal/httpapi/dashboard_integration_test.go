//go:build integration

package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/httpapi"
	"pc-club-admin/internal/shifts"
	"pc-club-admin/internal/storage/postgres"
)

func TestManagerDashboardRemoteEvidenceAndIdleTTL(t *testing.T) {
	pool := authDB(t)
	ctx := context.Background()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	access := auth.New(postgres.AuthRepository{Pool: pool}, "synthetic-dashboard-pepper-at-least-32-characters")
	password := "synthetic-manager-password"
	check(access.Bootstrap(ctx, auth.UserInput{Name: "Панель: управляющий", Login: "manager", Password: password, Roles: []string{"manager"}}))
	managerLogin, err := access.Login(ctx, auth.Request{Source: "192.0.2.140"}, "manager", password, false)
	check(err)
	manager := auth.Request{SessionToken: managerLogin.Token, CSRF: managerLogin.CSRF, ActorID: managerLogin.Identity.User.ID, Source: "192.0.2.140"}
	owner, err := access.CreateUser(ctx, manager, auth.UserInput{Name: "Панель: сдающий", Roles: []string{"admin"}, PIN: "123456"})
	check(err)
	receiver, err := access.CreateUser(ctx, manager, auth.UserInput{Name: "Панель: принимающий", Roles: []string{"admin"}, PIN: "654321"})
	check(err)
	work := shifts.New(postgres.ShiftRepository{Pool: pool}, access, "Europe/Moscow", false)
	c := config.Config{Environment: "test", Origin: "http://127.0.0.1:8080", Timezone: "Europe/Moscow", CashPolicyStatus: "unverified"}
	handler := httpapi.NewWithShifts(c, func(context.Context) error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)), access, work)
	remote := &browser{t: t, h: handler, cookies: map[string]*http.Cookie{"pcclub_session": {Name: "pcclub_session", Value: managerLogin.Token}}, source: "192.0.2.140"}
	const endpoint = "/api/v1/manager/dashboard"
	var before, after time.Time
	check(pool.QueryRow(ctx, `SELECT idle_expires_at FROM sessions WHERE token_hash=$1`, auth.Digest(managerLogin.Token)).Scan(&before))
	read := func() shifts.Dashboard {
		t.Helper()
		raw, err := json.Marshal(remote.call("GET", endpoint, nil, 200))
		check(err)
		var result shifts.Dashboard
		check(json.Unmarshal(raw, &result))
		return result
	}
	d := read()
	if d.State != "uninitialized" || d.Responsible != nil || d.InitialOpening != nil || d.ObservedAt.IsZero() {
		t.Fatal("empty club presented as active")
	}
	(&browser{t: t, h: handler, cookies: map[string]*http.Cookie{}, source: "192.0.2.141"}).call("GET", endpoint, nil, 401)
	amount, zero := int64(1000000), int64(0)
	source, _, err := work.Bootstrap(ctx, manager, shifts.BootstrapInput{OwnerID: owner.ID, BusinessDate: "2026-10-19", Kind: "day", InitialCash: &amount, Reason: "Синтетический остаток для панели"}, "dashboard-bootstrap-key")
	check(err)
	d = read()
	if d.State != "opening" || d.Responsible != nil || d.InitialOpening == nil || d.InitialOpening.Cash == nil || d.InitialOpening.Cash.State != "opening_unconfirmed" || d.InitialOpening.Cash.CountedOpening != nil || d.InitialOpening.Cash.LastSavedAt != nil {
		t.Fatal("manager amount became accepted evidence")
	}
	_, err = pool.Exec(ctx, `UPDATE shifts SET status='cancelled' WHERE id=$1`, source.ID)
	check(err)
	d = read()
	if d.State != "no_responsible" || d.Responsible != nil {
		t.Fatal("history without responsibility was presented as an empty healthy club")
	}
	_, err = pool.Exec(ctx, `UPDATE shifts SET status='opening' WHERE id=$1`, source.ID)
	check(err)
	code, err := access.Enroll(ctx, manager, password)
	check(err)
	device, err := access.RegisterDevice(ctx, auth.Request{Source: "192.0.2.141"}, code, "Синтетический ПК для панели")
	check(err)
	personal, err := access.Login(ctx, auth.Request{DeviceToken: device, Source: "192.0.2.141"}, owner.ID, "123456", true)
	check(err)
	admin := auth.Request{SessionToken: personal.Token, DeviceToken: device, CSRF: personal.CSRF, ActorID: owner.ID, Source: "192.0.2.141"}
	(&browser{t: t, h: handler, cookies: map[string]*http.Cookie{"pcclub_session": {Name: "pcclub_session", Value: personal.Token}, "pcclub_device": {Name: "pcclub_device", Value: device}}, source: "192.0.2.141"}).call("GET", endpoint, nil, 403)
	proof := func(r auth.Request, pin, action, target string, version int64) string {
		t.Helper()
		token, err := access.Reauth(ctx, r, pin, action, target, version)
		check(err)
		return token
	}
	for _, entry := range source.Entries {
		if entry.Phase == "acceptance" {
			source, err = work.Answer(ctx, admin, source.ID, entry.Code, shifts.AnswerInput{Answer: "yes", Version: entry.Version})
			check(err)
		}
	}
	source, _, err = work.Activate(ctx, admin, source.ID, "dashboard-first-acceptance", shifts.ActivateInput{Version: source.Version, Counted: &amount, Proof: proof(admin, "123456", "activate_first_shift", source.ID, source.Version)})
	check(err)
	d = read()
	if d.State != "active" || d.Responsible == nil || d.Responsible.Cash.State != "incomplete" || d.Responsible.Cash.LastSavedAt != nil {
		t.Fatal("unsaved cash shown as reconciled")
	}
	// Moving the plan into the past must not end actual responsibility.
	_, err = pool.Exec(ctx, `UPDATE shifts SET planned_start_at=clock_timestamp()-interval '13 hours',planned_end_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, source.ID)
	check(err)
	d = read()
	if !d.Responsible.Overdue || d.Responsible.DelaySeconds < 3599 || d.Responsible.DelaySeconds > 3610 || d.Responsible.OwnerID != owner.ID || d.Responsible.ActualEnd != nil {
		t.Fatal("overdue responsibility disappeared")
	}
	mismatch := amount - 1
	view, _, err := work.PatchCash(ctx, admin, source.ID, "dashboard-mismatch-key", shifts.CashInput{Langame: &zero, Refunds: &zero, Expenses: &zero, Counted: &mismatch, Version: source.Cash.Session.Version})
	check(err)
	d = read()
	if d.Responsible.Cash.State != "mismatch" || d.Responsible.Cash.LastSavedAt == nil || d.Responsible.Cash.Summary.Difference == nil || *d.Responsible.Cash.Summary.Difference != -1 {
		t.Fatal("one kopeck mismatch hidden")
	}
	view, _, err = work.PatchCash(ctx, admin, source.ID, "dashboard-matched-key", shifts.CashInput{Langame: &zero, Refunds: &zero, Expenses: &zero, Counted: &amount, Version: view.Session.Version})
	check(err)
	d = read()
	if d.Responsible.Cash.State != "matched" || d.Responsible.Cash.Summary.Verified {
		t.Fatal("matching drafts certified production policy")
	}
	// Simulate clock skew: timestamps must not hide later journal versions or
	// make an older report outrank a newly committed report.
	_, err = pool.Exec(ctx, `UPDATE audit_events SET created_at=clock_timestamp()+interval '1 day' WHERE id=(SELECT max(id) FROM audit_events WHERE action='cash_saved' AND entity_id=$1)`, source.ID)
	check(err)
	one := int64(1)
	view, _, err = work.Collect(ctx, admin, source.ID, "dashboard-collection-key", shifts.CollectionInput{Amount: &one, Reason: "Синтетическое изъятие", OccurredAt: time.Now(), Version: view.Session.Version, Proof: proof(admin, "123456", "collection", source.ID, view.Session.Version)})
	check(err)
	d = read()
	if !d.Responsible.Cash.ChangedAfterSave || d.Responsible.Cash.State != "mismatch" {
		t.Fatal("later journal operation presented as fresh saved reconciliation")
	}
	movementID := view.Session.Movements[0].ID
	view, _, err = work.ReverseCollection(ctx, admin, source.ID, movementID, "dashboard-reversal-key", shifts.ReverseInput{Reason: "Синтетическая отмена", Version: view.Session.Version, Proof: proof(admin, "123456", "reverse_collection", movementID, view.Session.Version)})
	check(err)
	d = read()
	if !d.Responsible.Cash.ChangedAfterSave || d.Responsible.Cash.State != "matched" {
		t.Fatal("net-zero journal change lost its evidence warning")
	}
	_, _, err = work.PatchCash(ctx, admin, source.ID, "dashboard-refresh-cash", shifts.CashInput{Langame: &zero, Refunds: &zero, Expenses: &zero, Counted: &amount, Version: view.Session.Version})
	check(err)
	d = read()
	if d.Responsible.Cash.ChangedAfterSave {
		t.Fatal("saving report did not refresh timestamp evidence")
	}
	source, err = work.Get(ctx, admin, source.ID)
	check(err)
	for _, entry := range source.Entries {
		if entry.Phase == "closing" {
			answer := "yes"
			if entry.Code == "conflicts_recorded" {
				answer = "no"
			}
			source, err = work.Answer(ctx, admin, source.ID, entry.Code, shifts.AnswerInput{Answer: answer, Version: entry.Version})
			check(err)
		}
	}
	pending, _, err := work.Prepare(ctx, admin, source.ID, "dashboard-prepare-key", shifts.PrepareInput{ReceiverID: receiver.ID, Version: source.Version, Proof: proof(admin, "123456", "prepare_handover", source.ID, source.Version)})
	check(err)
	d = read()
	if d.State != "handover_pending" || d.Pending == nil || d.Pending.ReceiverID != receiver.ID || d.Pending.Counted != nil || d.Pending.ReceiptState != "missing" || d.Pending.Acceptance.Completed != 0 || d.Responsible.OwnerID != owner.ID {
		t.Fatal("pending transfer prematurely reassigned responsibility")
	}
	personal, err = access.Login(ctx, auth.Request{DeviceToken: device, Source: "192.0.2.141"}, receiver.ID, "654321", true)
	check(err)
	incoming := auth.Request{SessionToken: personal.Token, CSRF: personal.CSRF, DeviceToken: device, ActorID: receiver.ID, Source: "192.0.2.141"}
	_, _, err = work.Receipt(ctx, incoming, pending.ID, "dashboard-recipient-count", shifts.ReceiptInput{Revision: pending.Revision, Version: pending.Version, Counted: &mismatch})
	check(err)
	d = read()
	if d.Pending.Counted == nil || *d.Pending.Counted != mismatch || d.Pending.ReceiptState != "mismatch" || d.Responsible.OwnerID != owner.ID {
		t.Fatal("receiver mismatch hidden")
	}
	check(pool.QueryRow(ctx, `SELECT idle_expires_at FROM sessions WHERE token_hash=$1`, auth.Digest(managerLogin.Token)).Scan(&after))
	if !before.Equal(after) {
		t.Fatal("passive dashboard reads extended session")
	}
	production := shifts.New(postgres.ShiftRepository{Pool: pool}, access, "Europe/Moscow", true)
	blocked, err := production.Dashboard(ctx, manager)
	check(err)
	if !blocked.ProductionBlocked || blocked.CashPolicy != "unverified" {
		t.Fatal("read-only dashboard bypassed production policy warning")
	}
	// SQL failure is a 503 error, never a successful empty/healthy dashboard.
	func() {
		_, err := pool.Exec(ctx, `ALTER TABLE audit_events RENAME TO dashboard_unavailable_audit`)
		check(err)
		defer func() {
			_, err := pool.Exec(ctx, `ALTER TABLE dashboard_unavailable_audit RENAME TO audit_events`)
			check(err)
		}()
		failure := remote.call("GET", endpoint, nil, 503)
		if _, ok := failure["observed_at"]; ok {
			t.Fatal("failed query returned a success snapshot")
		}
	}()
	d = read()
	if d.Responsible == nil || d.Pending == nil {
		t.Fatal("dashboard did not recover after database error")
	}
	_, err = pool.Exec(ctx, `UPDATE sessions SET idle_expires_at=clock_timestamp()-interval '1 second' WHERE token_hash=$1`, auth.Digest(managerLogin.Token))
	check(err)
	remote.call("GET", endpoint, nil, 401)
}
