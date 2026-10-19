//go:build integration

package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/httpapi"
	"pc-club-admin/internal/shifts"
	"pc-club-admin/internal/storage/postgres"
)

func TestManagerAuditMetadataPagination(t *testing.T) {
	pool := authDB(t)
	ctx := context.Background()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	access := auth.New(postgres.AuthRepository{Pool: pool}, "synthetic-audit-pepper-at-least-32-characters")
	password := "synthetic-manager-password"
	check(access.Bootstrap(ctx, auth.UserInput{Name: "Audit manager", Login: "manager", Password: password, Roles: []string{"manager"}}))
	login, err := access.Login(ctx, auth.Request{Source: "192.0.2.150"}, "manager", password, false)
	check(err)
	work := shifts.New(postgres.ShiftRepository{Pool: pool}, access, "Europe/Moscow", false)
	c := config.Config{Environment: "test", Origin: "http://127.0.0.1:8080", Timezone: "Europe/Moscow", CashPolicyStatus: "unverified"}
	handler := httpapi.NewWithShifts(c, func(context.Context) error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)), access, work)
	remote := &browser{t: t, h: handler, cookies: map[string]*http.Cookie{"pcclub_session": {Name: "pcclub_session", Value: login.Token}}, source: "192.0.2.150"}
	const endpoint = "/api/v1/audit-events"
	(&browser{t: t, h: handler, cookies: map[string]*http.Cookie{}, source: "192.0.2.151"}).call("GET", endpoint, nil, 401)
	// Equal timestamps and a bigint beyond JavaScript's exact integer range.
	_, err = pool.Exec(ctx, `INSERT INTO audit_events(id,action,entity_id,request_id,success,details,created_at) OVERRIDING SYSTEM VALUE VALUES
 (9007199254740993,'synthetic_audit','fixture','request-one',true,'{"secret":"must-not-leak"}','2026-10-19T09:00:00Z'),
 (9007199254740994,'synthetic_audit','fixture','request-two',false,'{}','2026-10-19T09:00:00Z')`)
	check(err)
	read := func(path string) shifts.AuditPage {
		t.Helper()
		body, err := json.Marshal(remote.call("GET", path, nil, 200))
		check(err)
		if strings.Contains(string(body), "must-not-leak") || strings.Contains(string(body), "details") {
			t.Fatal("internal details exposed")
		}
		var page shifts.AuditPage
		check(json.Unmarshal(body, &page))
		return page
	}
	first := read(endpoint + "?action=synthetic_audit&limit=1")
	if len(first.Items) != 1 || first.Items[0].ID != "9007199254740994" || first.NextCursor != first.Items[0].ID || first.Items[0].ActorID != nil {
		t.Fatalf("bad first page: %+v", first)
	}
	second := read(endpoint + "?action=synthetic_audit&limit=1&cursor=" + first.NextCursor)
	if len(second.Items) != 1 || second.Items[0].ID != "9007199254740993" || second.NextCursor != "" {
		t.Fatalf("bad second page: %+v", second)
	}
	if len(read(endpoint+"?entity_id=absent").Items) != 0 {
		t.Fatal("entity filter ignored")
	}
	if len(read(endpoint+"?action=synthetic_audit&from=2026-10-19T09:00:00Z&to=2026-10-19T09:00:01Z").Items) != 2 {
		t.Fatal("period filter lost boundary events")
	}
	if len(read(endpoint+"?action=synthetic_audit&to=2026-10-19T09:00:00Z").Items) != 0 {
		t.Fatal("upper bound is not exclusive")
	}
	for _, query := range []string{"?cursor=01", "?actor_id=bad", "?from=bad", "?limit=0", "?limit=1&limit=2", "?unknown=1"} {
		remote.call("GET", endpoint+query, nil, 400)
	}
	// Role revocation is checked server-side even for an existing valid session.
	_, err = pool.Exec(ctx, `UPDATE user_roles SET role='admin' WHERE user_id=$1 AND role='manager'`, login.Identity.User.ID)
	check(err)
	remote.call("GET", endpoint, nil, 403)
}
