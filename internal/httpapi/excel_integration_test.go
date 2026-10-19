//go:build integration

package httpapi_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/excelsync"
	"pc-club-admin/internal/shifts"
	"pc-club-admin/internal/storage/postgres"
)

func TestExcelOutboxSurvivesOutageAndConcurrentEdits(t *testing.T) {
	pool := authDB(t)
	ctx := context.Background()
	access := auth.New(postgres.AuthRepository{Pool: pool}, "synthetic-excel-pepper-at-least-32-characters")
	password := "synthetic-manager-password"
	if err := access.Bootstrap(ctx, auth.UserInput{Name: "Синтетический управляющий", Login: "manager", Password: password, Roles: []string{"manager"}}); err != nil {
		t.Fatal(err)
	}
	login, err := access.Login(ctx, auth.Request{Source: "192.0.2.90"}, "manager", password, false)
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.Request{SessionToken: login.Token, CSRF: login.CSRF, ActorID: login.Identity.User.ID, Source: "192.0.2.90"}
	owner, err := access.CreateUser(ctx, manager, auth.UserInput{Name: "Синтетический администратор", Roles: []string{"admin"}, PIN: "123456"})
	if err != nil {
		t.Fatal(err)
	}
	work := shifts.New(postgres.ShiftRepository{Pool: pool}, access, "Europe/Moscow", false)
	zero := int64(0)
	shift, _, err := work.Bootstrap(ctx, manager, shifts.BootstrapInput{OwnerID: owner.ID, BusinessDate: "2026-10-19", Kind: "day", InitialCash: &zero, Reason: "Синтетический тест"}, "synthetic-excel-bootstrap-key")
	if err != nil {
		t.Fatal(err)
	}
	repository := postgres.ExcelRepository{Pool: pool}
	target := excelsync.TargetHash("synthetic-file-id")
	worked, err := repository.DeliverOne(ctx, target, func(_ context.Context, r excelsync.Record) error {
		if r.Version != 1 || len(r.Entries) != 31 || r.OwnerName != owner.Name {
			t.Error("queue snapshot is not committed domain state")
		}
		return &excelsync.Failure{Code: "EXCEL_PROVIDER_UNAVAILABLE", Temporary: true}
	})
	if err != nil || !worked {
		t.Fatal(err)
	}
	read := func() (int64, int64, string) {
		t.Helper()
		var requested, synced int64
		var code string
		if err := pool.QueryRow(ctx, "SELECT requested_version,synced_version,last_error_code FROM excel_sync_queue WHERE shift_id=$1", shift.ID).Scan(&requested, &synced, &code); err != nil {
			t.Fatal(err)
		}
		return requested, synced, code
	}
	requested, synced, code := read()
	if requested != 1 || synced != 0 || code != "EXCEL_PROVIDER_UNAVAILABLE" {
		t.Fatal("outage erased or falsely confirmed data")
	}
	if worked, err := repository.DeliverOne(ctx, target, func(context.Context, excelsync.Record) error { t.Error("backoff bypassed"); return nil }); err != nil || worked {
		t.Fatal("retry not bounded")
	}
	if _, err := pool.Exec(ctx, "UPDATE excel_sync_queue SET next_attempt_at=clock_timestamp() WHERE shift_id=$1", shift.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DeliverOne(ctx, target, func(context.Context, excelsync.Record) error { return nil }); err != nil {
		t.Fatal(err)
	}
	_, synced, code = read()
	if synced != 1 || code != "" {
		t.Fatal("successful delivery not recorded")
	}
	codeToken, err := access.Enroll(ctx, manager, password)
	if err != nil {
		t.Fatal(err)
	}
	device, err := access.RegisterDevice(ctx, auth.Request{Source: "192.0.2.91"}, codeToken, "Синтетический Excel-тест")
	if err != nil {
		t.Fatal(err)
	}
	login, err = access.Login(ctx, auth.Request{DeviceToken: device, Source: "192.0.2.91"}, owner.ID, "123456", true)
	if err != nil {
		t.Fatal(err)
	}
	admin := auth.Request{SessionToken: login.Token, DeviceToken: device, CSRF: login.CSRF, ActorID: owner.ID, Source: "192.0.2.91"}
	if _, err := work.Answer(ctx, admin, shift.ID, "accept_d", shifts.AnswerInput{Answer: "yes", Version: 1}); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := repository.DeliverOne(ctx, target, func(_ context.Context, r excelsync.Record) error {
			if r.Version != 2 {
				return errors.New("wrong captured version")
			}
			close(started)
			<-release
			return nil
		})
		result <- err
	}()
	select {
	case <-started:
	case err := <-result:
		t.Fatalf("delivery did not reach the remote operation: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not start")
	}
	if worked, err := repository.DeliverOne(ctx, target, func(context.Context, excelsync.Record) error { return errors.New("second worker entered") }); err != nil || worked {
		close(release)
		t.Fatal("remote writers not serialized")
	}
	editContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	_, editErr := work.Answer(editContext, admin, shift.ID, "accept_e", shifts.AnswerInput{Answer: "yes", Version: 1})
	cancel()
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if editErr != nil {
		t.Fatalf("remote delivery blocked a business save: %v", editErr)
	}
	requested, synced, _ = read()
	if requested != 3 || synced != 2 {
		t.Fatal("old completion erased a newer pending revision")
	}
	// A successful remote write is not acknowledged if the local audit fails.
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_events ADD CONSTRAINT reject_excel_audit CHECK(false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DeliverOne(ctx, target, func(context.Context, excelsync.Record) error { return nil }); err == nil {
		t.Fatal("delivery acknowledged without audit")
	}
	if _, err := work.Answer(ctx, admin, shift.ID, "accept_f", shifts.AnswerInput{Answer: "yes", Version: 1}); err == nil {
		t.Fatal("business save accepted without audit")
	}
	requested, synced, _ = read()
	if requested != 3 || synced != 2 {
		t.Fatal("outbox survived a rolled-back business/audit transaction")
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_events DROP CONSTRAINT reject_excel_audit"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DeliverOne(ctx, target, func(context.Context, excelsync.Record) error { return nil }); err != nil {
		t.Fatal(err)
	}
	requested, synced, _ = read()
	if requested != synced || synced != 3 {
		t.Fatal("retry did not acknowledge the same revision")
	}
	if worked, err := repository.DeliverOne(ctx, target, func(context.Context, excelsync.Record) error { return nil }); err != nil || worked {
		t.Fatal("already delivered revision sent again")
	}
	if err := repository.PrepareTarget(ctx, excelsync.TargetHash("different-test-file-id")); err != nil {
		t.Fatal(err)
	}
	_, synced, _ = read()
	if synced != 0 {
		t.Fatal("old file receipt reused for a new destination")
	}
}
