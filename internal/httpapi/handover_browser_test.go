//go:build integration

package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/httpapi"
	"pc-club-admin/internal/shifts"
	"pc-club-admin/internal/storage/postgres"
)

// TestHandoverBrowser uses the same isolated *_test database guard as HTTP tests.
// Node drives a real Chromium; no Google credentials or private data are used.
func TestHandoverBrowser(t *testing.T) {
	if os.Getenv("PC_CLUB_BROWSER_TEST") != "1" {
		t.Skip("optional Chromium test")
	}
	pool := authDB(t)
	ctx := context.Background()
	access := auth.New(postgres.AuthRepository{Pool: pool}, "synthetic-browser-pepper-at-least-32-characters")
	password := "synthetic-manager-password"
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check(access.Bootstrap(ctx, auth.UserInput{Name: "Управляющий E2E", Login: "manager", Password: password, Roles: []string{"manager"}}))
	login, err := access.Login(ctx, auth.Request{Source: "192.0.2.120"}, "manager", password, false)
	check(err)
	manager := auth.Request{SessionToken: login.Token, CSRF: login.CSRF, ActorID: login.Identity.User.ID, Source: "192.0.2.120"}
	owner, err := access.CreateUser(ctx, manager, auth.UserInput{Name: "Сдающий E2E", Roles: []string{"admin"}, PIN: "123456"})
	check(err)
	receiver, err := access.CreateUser(ctx, manager, auth.UserInput{Name: "Принимающий E2E", Roles: []string{"admin"}, PIN: "654321"})
	check(err)
	code, err := access.Enroll(ctx, manager, password)
	check(err)
	device, err := access.RegisterDevice(ctx, auth.Request{Source: "192.0.2.121"}, code, "Chromium E2E")
	check(err)
	work := shifts.New(postgres.ShiftRepository{Pool: pool}, access, "Europe/Moscow", false)
	amount := int64(1000000)
	shift, _, err := work.Bootstrap(ctx, manager, shifts.BootstrapInput{OwnerID: owner.ID, BusinessDate: "2026-10-19", Kind: "day", InitialCash: &amount, Reason: "Синтетический E2E"}, "browser-bootstrap-key")
	check(err)
	login, err = access.Login(ctx, auth.Request{DeviceToken: device, Source: "192.0.2.121"}, owner.ID, "123456", true)
	check(err)
	admin := auth.Request{SessionToken: login.Token, CSRF: login.CSRF, DeviceToken: device, ActorID: owner.ID, Source: "192.0.2.121"}
	for _, entry := range shift.Entries {
		if entry.Phase == "acceptance" {
			shift, err = work.Answer(ctx, admin, shift.ID, entry.Code, shifts.AnswerInput{Answer: "yes", Version: entry.Version})
			check(err)
		}
	}
	token, err := access.Reauth(ctx, admin, "123456", "activate_first_shift", shift.ID, shift.Version)
	check(err)
	shift, _, err = work.Activate(ctx, admin, shift.ID, "browser-activate-key", shifts.ActivateInput{Counted: &amount, Version: shift.Version, Proof: token})
	check(err)
	zero, delta, end := int64(0), int64(500000), int64(1500000)
	_, _, err = work.PatchCash(ctx, admin, shift.ID, "browser-cash-save-key", shifts.CashInput{Langame: &delta, Refunds: &zero, Expenses: &zero, Counted: &end, Version: shift.Cash.Session.Version})
	check(err)
	for _, entry := range shift.Entries {
		if entry.Phase == "closing" {
			answer := "yes"
			if entry.Code == "conflicts_recorded" {
				answer = "no"
			}
			_, err = work.Answer(ctx, admin, shift.ID, entry.Code, shifts.AnswerInput{Answer: answer, Version: entry.Version})
			check(err)
		}
	}
	_, err = work.Note(ctx, admin, shift.ID, "", shifts.NoteInput{Code: "close_x", Category: "equipment", Object: "ПК E2E", Text: "Поломка зафиксирована, ремонт не завершён"})
	check(err)
	check(access.Logout(ctx, admin))
	dist, err := filepath.Abs("../../web/dist")
	check(err)
	if _, err := os.Stat(filepath.Join(dist, "index.html")); err != nil {
		t.Fatal("build frontend before browser test")
	}
	server := httptest.NewUnstartedServer(nil)
	c := config.Config{Environment: "test", Origin: "http://" + server.Listener.Addr().String(), Timezone: "Europe/Moscow", CashPolicyStatus: "unverified", WebDistDir: dist}
	server.Config.Handler = httpapi.NewWithShifts(c, func(ctx context.Context) error { return postgres.CheckSchema(ctx, pool, c.Timezone) }, slog.New(slog.NewTextHandler(io.Discard, nil)), access, work)
	server.Start()
	defer server.Close()
	browserContext, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	command := exec.CommandContext(browserContext, "node", "../../web/e2e/handover.mjs")
	command.Env = append(os.Environ(), "PC_CLUB_E2E_URL="+server.URL, "PC_CLUB_E2E_DEVICE="+device, "PC_CLUB_E2E_OWNER="+owner.ID, "PC_CLUB_E2E_RECEIVER="+receiver.ID)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser scenario failed: %v\n%s", err, output)
	}
	var completed bool
	check(pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM handovers h JOIN shifts a ON a.id=h.from_shift_id JOIN shifts b ON b.id=h.to_shift_id WHERE h.status='accepted' AND a.actual_end_at=b.actual_start_at AND b.owner_id=$1)`, receiver.ID).Scan(&completed))
	if !completed {
		t.Fatal("browser did not complete an atomic handover")
	}
}
