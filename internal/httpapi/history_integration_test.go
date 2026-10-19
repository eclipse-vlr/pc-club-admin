//go:build integration

package httpapi_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"
	"time"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/httpapi"
	"pc-club-admin/internal/shifts"
	"pc-club-admin/internal/storage/postgres"
)

func TestHistoryScopePaginationAndCancelledSnapshot(t *testing.T) {
	pool := authDB(t)
	ctx := context.Background()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	access := auth.New(postgres.AuthRepository{Pool: pool}, "synthetic-history-pepper-at-least-32-characters")
	password := "synthetic-manager-password"
	check(access.Bootstrap(ctx, auth.UserInput{Name: "История: управляющий", Login: "manager", Password: password, Roles: []string{"manager"}}))
	managerLogin, err := access.Login(ctx, auth.Request{Source: "192.0.2.130"}, "manager", password, false)
	check(err)
	manager := auth.Request{SessionToken: managerLogin.Token, CSRF: managerLogin.CSRF, ActorID: managerLogin.Identity.User.ID, Source: "192.0.2.130"}
	owner, err := access.CreateUser(ctx, manager, auth.UserInput{Name: "История: сдающий", Roles: []string{"admin"}, PIN: "123456"})
	check(err)
	receiver, err := access.CreateUser(ctx, manager, auth.UserInput{Name: "История: принимающий", Roles: []string{"admin"}, PIN: "654321"})
	check(err)
	former, err := access.CreateUser(ctx, manager, auth.UserInput{Name: "История: прежний получатель", Roles: []string{"admin"}, PIN: "112233"})
	check(err)
	code, err := access.Enroll(ctx, manager, password)
	check(err)
	device, err := access.RegisterDevice(ctx, auth.Request{Source: "192.0.2.131"}, code, "История: синтетический браузер")
	check(err)
	work := shifts.New(postgres.ShiftRepository{Pool: pool}, access, "Europe/Moscow", false)
	amount, zero := int64(1000000), int64(0)
	source, _, err := work.Bootstrap(ctx, manager, shifts.BootstrapInput{OwnerID: owner.ID, BusinessDate: "2026-10-19", Kind: "day", InitialCash: &amount, Reason: "Синтетическая история"}, "history-bootstrap-key")
	check(err)
	login := func(user, pin string) (auth.Request, auth.LoginResult) {
		t.Helper()
		result, err := access.Login(ctx, auth.Request{DeviceToken: device, Source: "192.0.2.131"}, user, pin, true)
		check(err)
		return auth.Request{SessionToken: result.Token, DeviceToken: device, CSRF: result.CSRF, ActorID: user, Source: "192.0.2.131"}, result
	}
	admin, _ := login(owner.ID, "123456")
	answer := func(r auth.Request, shift shifts.Shift, phase string) shifts.Shift {
		t.Helper()
		for _, entry := range shift.Entries {
			if entry.Phase != phase {
				continue
			}
			value := "yes"
			if entry.Code == "conflicts_recorded" {
				value = "no"
			}
			shift, err = work.Answer(ctx, r, shift.ID, entry.Code, shifts.AnswerInput{Answer: value, Version: entry.Version})
			check(err)
		}
		return shift
	}
	proof := func(r auth.Request, pin, action, target string, revision int64) string {
		t.Helper()
		token, err := access.Reauth(ctx, r, pin, action, target, revision)
		check(err)
		return token
	}
	source = answer(admin, source, "acceptance")
	source, _, err = work.Activate(ctx, admin, source.ID, "history-first-activation", shifts.ActivateInput{Counted: &amount, Version: source.Version, Proof: proof(admin, "123456", "activate_first_shift", source.ID, source.Version)})
	check(err)
	_, _, err = work.PatchCash(ctx, admin, source.ID, "history-cash-save-key", shifts.CashInput{Langame: &zero, Refunds: &zero, Expenses: &zero, Counted: &amount, Version: source.Cash.Session.Version})
	check(err)
	source = answer(admin, source, "closing")
	note, err := work.Note(ctx, admin, source.ID, "", shifts.NoteInput{Category: "general", Text: "Первоначальная заметка"})
	check(err)
	source, err = work.Get(ctx, admin, source.ID)
	check(err)
	cancelled, _, err := work.Prepare(ctx, admin, source.ID, "history-first-preparation", shifts.PrepareInput{ReceiverID: former.ID, Version: source.Version, Proof: proof(admin, "123456", "prepare_handover", source.ID, source.Version)})
	check(err)
	admin, _ = login(owner.ID, "123456")
	cancelled, _, err = work.Cancel(ctx, admin, cancelled.ID, "history-cancel-key", shifts.CancelInput{Version: cancelled.Version, Reason: "Изменился получатель"})
	check(err)
	_, err = work.Note(ctx, admin, source.ID, note.ID, shifts.NoteInput{Category: "general", Text: "Новая заметка после отзыва", Version: note.Version})
	check(err)
	source, err = work.Get(ctx, admin, source.ID)
	check(err)
	accepted, _, err := work.Prepare(ctx, admin, source.ID, "history-second-preparation", shifts.PrepareInput{ReceiverID: receiver.ID, Version: source.Version, Proof: proof(admin, "123456", "prepare_handover", source.ID, source.Version)})
	check(err)
	incoming, receiverLogin := login(receiver.ID, "654321")
	next, err := work.Get(ctx, incoming, accepted.ToShiftID)
	check(err)
	next = answer(incoming, next, "acceptance")
	accepted, _, err = work.Receipt(ctx, incoming, accepted.ID, "history-receipt-key", shifts.ReceiptInput{Revision: accepted.Revision, Version: accepted.Version, Counted: &amount})
	check(err)
	accepted, _, err = work.Acknowledge(ctx, incoming, accepted.ID, "history-acknowledgement", shifts.AcknowledgeInput{Revision: accepted.Revision, Version: accepted.Version, NoteIDs: []string{note.ID}})
	check(err)
	next, err = work.Get(ctx, incoming, next.ID)
	check(err)
	accepted, _, err = work.Accept(ctx, incoming, accepted.ID, "history-acceptance-key", shifts.AcceptInput{Revision: accepted.Revision, Version: accepted.Version, ShiftVersion: next.Version, Proof: proof(incoming, "654321", "accept_handover", accepted.ID, next.Version)})
	check(err)
	// Equal dates and creation times require UUID as a deterministic final sort key.
	_, err = pool.Exec(ctx, `UPDATE shifts SET created_at='2026-10-19T06:00:00Z'`)
	check(err)
	c := config.Config{Environment: "test", Origin: "http://127.0.0.1:8080", Timezone: "Europe/Moscow", CashPolicyStatus: "unverified"}
	handler := httpapi.NewWithShifts(c, func(context.Context) error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)), access, work)
	browserFrom := func(result auth.LoginResult, trusted bool) *browser {
		cookies := map[string]*http.Cookie{"pcclub_session": {Name: "pcclub_session", Value: result.Token}, "pcclub_csrf": {Name: "pcclub_csrf", Value: result.CSRF}}
		if trusted {
			cookies["pcclub_device"] = &http.Cookie{Name: "pcclub_device", Value: device}
		}
		return &browser{t: t, h: handler, cookies: cookies, source: "192.0.2.131", actor: result.Identity.User.ID, csrf: result.CSRF}
	}
	managerBrowser := browserFrom(managerLogin, false)
	receiverBrowser := browserFrom(receiverLogin, true)
	_, ownerLogin := login(owner.ID, "123456")
	ownerBrowser := browserFrom(ownerLogin, true)
	_, formerLogin := login(former.ID, "112233")
	formerBrowser := browserFrom(formerLogin, true)
	// Logging into one physical browser revokes only the passed prior session token;
	// these isolated request fixtures retain personal sessions to test each scope.
	gather := func(b *browser, query string) map[string]bool {
		t.Helper()
		ids := map[string]bool{}
		cursor := ""
		for page := 0; page < 10; page++ {
			q, err := url.ParseQuery(query)
			check(err)
			q.Set("limit", "1")
			if cursor != "" {
				q.Set("cursor", cursor)
			}
			result := b.call("GET", "/api/v1/shifts?"+q.Encode(), nil, 200)
			for _, value := range result["items"].([]any) {
				id := value.(map[string]any)["id"].(string)
				if ids[id] {
					t.Fatal("duplicate across history pages")
				}
				ids[id] = true
			}
			cursor = result["next_cursor"].(string)
			if cursor == "" {
				return ids
			}
		}
		t.Fatal("pagination never ended")
		return nil
	}
	all := gather(managerBrowser, "")
	if len(all) != 3 {
		t.Fatal("manager history incomplete")
	}
	mine := gather(ownerBrowser, "")
	if len(mine) != 1 || !mine[source.ID] {
		t.Fatal("sender saw receiver's live or cancelled draft")
	}
	mine = gather(receiverBrowser, "")
	if len(mine) != 2 || !mine[source.ID] || !mine[next.ID] {
		t.Fatal("receiver cannot see personally accepted history")
	}
	mine = gather(formerBrowser, "")
	if len(mine) != 1 || !mine[cancelled.ToShiftID] {
		t.Fatal("cancelled participant saw sender's new state")
	}
	if len(gather(managerBrowser, "date_from=2026-10-20")) != 0 || len(gather(managerBrowser, "date_to=2026-10-18")) != 0 || len(gather(managerBrowser, "kind=day&status=closed")) != 1 || len(gather(managerBrowser, "owner_id="+former.ID)) != 1 {
		t.Fatal("filters ignored")
	}
	receiverBrowser.call("GET", "/api/v1/shifts/"+source.ID, nil, 200)
	formerBrowser.call("GET", "/api/v1/shifts/"+source.ID, nil, 404)
	formerBrowser.call("GET", "/api/v1/shifts/"+source.ID+"/handovers", nil, 404)
	old := formerBrowser.call("GET", "/api/v1/handovers/"+cancelled.ID, nil, 200)
	if old["snapshot"].(map[string]any)["notes"].([]any)[0].(map[string]any)["text"] != "Первоначальная заметка" {
		t.Fatal("cancelled snapshot leaked later notes")
	}
	recipientTransfers := receiverBrowser.call("GET", "/api/v1/shifts/"+source.ID+"/handovers", nil, 200)["items"].([]any)
	if len(recipientTransfers) != 1 || recipientTransfers[0].(map[string]any)["id"] != accepted.ID {
		t.Fatal("receiver saw an unrelated cancelled transfer")
	}
	transferPage := managerBrowser.call("GET", "/api/v1/shifts/"+source.ID+"/handovers?limit=1", nil, 200)
	cursor := transferPage["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("transfer page missing cursor")
	}
	second := managerBrowser.call("GET", "/api/v1/shifts/"+source.ID+"/handovers?limit=1&cursor="+url.QueryEscape(cursor), nil, 200)
	if second["next_cursor"] != "" || len(second["items"].([]any)) != 1 || transferPage["items"].([]any)[0].(map[string]any)["id"] == second["items"].([]any)[0].(map[string]any)["id"] {
		t.Fatal("transfer pagination repeated/skipped")
	}
	for _, query := range []string{"limit=0", "limit=-1", "limit=101", "limit=abc", "date_from=2026-02-30", "date_from=0000-01-01", "date_from=2026-10-20&date_to=2026-10-19", "owner_id=not-a-uuid", "status=forged", "kind=other", "cursor=invalid", "limit=1&limit=2", "cursor=%zz"} {
		managerBrowser.call("GET", "/api/v1/shifts?"+query, nil, 400)
	}
	encoded, err := json.Marshal(shifts.PageCursor{Date: "9999-12-31", At: time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), ID: source.ID})
	check(err)
	forged := formerBrowser.call("GET", "/api/v1/shifts?cursor="+base64.RawURLEncoding.EncodeToString(encoded), nil, 200)
	if len(forged["items"].([]any)) != 1 || forged["items"].([]any)[0].(map[string]any)["id"] != cancelled.ToShiftID {
		t.Fatal("cursor bypassed ownership")
	}
	browserFrom(receiverLogin, false).call("GET", "/api/v1/shifts", nil, 403)
	var before, after time.Time
	check(pool.QueryRow(ctx, `SELECT idle_expires_at FROM sessions WHERE token_hash=$1`, auth.Digest(receiverLogin.Token)).Scan(&before))
	receiverBrowser.call("GET", "/api/v1/shifts", nil, 200)
	receiverBrowser.call("GET", "/api/v1/shifts/"+source.ID, nil, 200)
	receiverBrowser.call("GET", "/api/v1/handovers/"+accepted.ID, nil, 200)
	check(pool.QueryRow(ctx, `SELECT idle_expires_at FROM sessions WHERE token_hash=$1`, auth.Digest(receiverLogin.Token)).Scan(&after))
	if !before.Equal(after) {
		t.Fatal("history polling extended personal session")
	}
	t.Logf("history scopes verified for %d synthetic shifts", len(all))
}
