//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/httpapi"
	"pc-club-admin/internal/shifts"
	"pc-club-admin/internal/storage/postgres"
)

// Starts from a migrated empty schema. The first manager is created by the
// actual interactive-CLI secret pipe; all further setup is done through the UI.
func TestSetupBrowser(t *testing.T) {
	if os.Getenv("PC_CLUB_BROWSER_TEST") != "1" {
		t.Skip("optional Chromium test")
	}
	pool := authDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	pepper := "synthetic-setup-pepper-at-least-32-characters"
	password := "synthetic-setup-manager-password"
	connection, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	query := connection.Query()
	query.Set("search_path", pool.Config().ConnConfig.RuntimeParams["search_path"])
	connection.RawQuery = query.Encode()
	bootstrap := exec.CommandContext(ctx, "go", "run", "./cmd/admin", "bootstrap-manager", "--login", "manager", "--name", "Управляющий настройки", "--password-stdin")
	bootstrap.Dir = "../.."
	bootstrap.Stdin = strings.NewReader(password + "\n")
	bootstrap.Env = append(os.Environ(), "APP_ENV=test", "APP_ORIGIN=http://127.0.0.1:8080", "PIN_PEPPER="+pepper, "DATABASE_URL="+connection.String(), "DRIVE_EXCEL_SYNC=disabled")
	if output, err := bootstrap.CombinedOutput(); err != nil {
		t.Fatalf("manager CLI: %v %s", err, output)
	}
	access := auth.New(postgres.AuthRepository{Pool: pool}, pepper)
	work := shifts.New(postgres.ShiftRepository{Pool: pool}, access, "Europe/Moscow", false)
	dist, err := filepath.Abs("../../web/dist")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	c := config.Config{Environment: "test", Origin: "http://" + server.Listener.Addr().String(), Timezone: "Europe/Moscow", CashPolicyStatus: "unverified", WebDistDir: dist}
	server.Config.Handler = httpapi.NewWithShifts(c, func(ctx context.Context) error { return postgres.CheckSchema(ctx, pool, c.Timezone) }, slog.New(slog.NewTextHandler(io.Discard, nil)), access, work)
	server.Start()
	defer server.Close()
	command := exec.CommandContext(ctx, "node", "../../web/e2e/setup.mjs")
	command.Env = append(os.Environ(), "PC_CLUB_E2E_URL="+server.URL, "PC_CLUB_E2E_MANAGER_PASSWORD="+password)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("setup browser: %v\n%s", err, output)
	}
	var active, confirmed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM shifts WHERE status='active' AND kind='night' AND business_date='2026-10-19' AND planned_end_at AT TIME ZONE 'Europe/Moscow'='2026-10-20 09:00:00'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM cash_sessions WHERE opening_cash_kopecks=0 AND counted_opening_kopecks=0 AND opening_confirmed_by IS NOT NULL`).Scan(&confirmed); err != nil {
		t.Fatal(err)
	}
	if active != 1 || confirmed != 1 {
		t.Fatal("first shift was not personally activated with explicit zero")
	}
	// Read a populated 31-item shift over HTTP from ten independent sessions.
	// Authentication setup is deliberately excluded from the measured interval.
	var shiftID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM shifts WHERE status='active'`).Scan(&shiftID); err != nil {
		t.Fatal(err)
	}
	tokens := []string{}
	var managerLogin auth.LoginResult
	for i := 0; i < 10; i++ {
		result, err := access.Login(ctx, auth.Request{Source: fmt.Sprintf("192.0.2.%d", 170+i)}, "manager", password, false)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, result.Token)
		managerLogin = result
	}
	var group sync.WaitGroup
	durations := make(chan time.Duration, 100)
	failures := make(chan error, 100)
	client := http.Client{Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	for _, token := range tokens {
		group.Add(1)
		go func(token string) {
			defer group.Done()
			for i := 0; i < 10; i++ {
				request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/shifts/"+shiftID, nil)
				if err != nil {
					failures <- err
					return
				}
				request.AddCookie(&http.Cookie{Name: "pcclub_session", Value: token})
				started := time.Now()
				response, err := client.Do(request)
				if err != nil {
					failures <- err
					return
				}
				_, readErr := io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				if response.StatusCode != 200 || readErr != nil {
					failures <- fmt.Errorf("read smoke status=%d error=%v", response.StatusCode, readErr)
					return
				}
				durations <- time.Since(started)
			}
		}(token)
	}
	group.Wait()
	close(durations)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	samples := []time.Duration{}
	for elapsed := range durations {
		samples = append(samples, elapsed)
	}
	if len(samples) != 100 {
		t.Fatalf("only %d successful load requests", len(samples))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	t.Logf("10 sessions, 100 populated-shift HTTP reads: p95=%s", samples[94])
	if samples[94] > 500*time.Millisecond {
		t.Errorf("read smoke p95 exceeds 500ms: %s", samples[94])
	}

	managerRequest := auth.Request{SessionToken: managerLogin.Token, CSRF: managerLogin.CSRF, ActorID: managerLogin.Identity.User.ID, Source: "192.0.2.179"}
	code, err := access.Enroll(ctx, managerRequest, password)
	if err != nil {
		t.Fatal(err)
	}
	device, err := access.RegisterDevice(ctx, auth.Request{Source: "192.0.2.180"}, code, "Synthetic load browser")
	if err != nil {
		t.Fatal(err)
	}
	var ownerID string
	if err := pool.QueryRow(ctx, `SELECT owner_id::text FROM shifts WHERE id=$1`, shiftID).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT code,version FROM shift_checklist_entries WHERE shift_id=$1 AND phase='closing' ORDER BY code LIMIT 10`, shiftID)
	if err != nil {
		t.Fatal(err)
	}
	type item struct {
		code    string
		version int64
	}
	items := []item{}
	for rows.Next() {
		var entry item
		if err := rows.Scan(&entry.code, &entry.version); err != nil {
			t.Fatal(err)
		}
		items = append(items, entry)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(items) != 10 {
		t.Fatal("ten independent closing items required")
	}
	sessions := []auth.LoginResult{}
	for i := 0; i < 10; i++ {
		login, err := access.Login(ctx, auth.Request{DeviceToken: device, Source: "192.0.2.180"}, ownerID, "222222", true)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, login)
	}
	durations = make(chan time.Duration, 100)
	failures = make(chan error, 100)
	for index, session := range sessions {
		group.Add(1)
		go func(entry item, session auth.LoginResult) {
			defer group.Done()
			for i := 0; i < 10; i++ {
				body := fmt.Sprintf(`{"answer":"yes","comment":"Synthetic load smoke","version":%d}`, entry.version)
				request, err := http.NewRequestWithContext(ctx, "PATCH", server.URL+"/api/v1/shifts/"+shiftID+"/checklist/"+entry.code, bytes.NewBufferString(body))
				if err != nil {
					failures <- err
					return
				}
				request.AddCookie(&http.Cookie{Name: "pcclub_session", Value: session.Token})
				request.AddCookie(&http.Cookie{Name: "pcclub_device", Value: device})
				request.AddCookie(&http.Cookie{Name: "pcclub_csrf", Value: session.CSRF})
				request.Header.Set("Origin", c.Origin)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-CSRF-Token", session.CSRF)
				request.Header.Set("X-Actor-ID", ownerID)
				started := time.Now()
				response, err := client.Do(request)
				if err != nil {
					failures <- err
					return
				}
				_, readErr := io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				if response.StatusCode != 200 || readErr != nil {
					failures <- fmt.Errorf("write smoke status=%d error=%v", response.StatusCode, readErr)
					return
				}
				durations <- time.Since(started)
				entry.version++
			}
		}(items[index], session)
	}
	group.Wait()
	close(durations)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	samples = nil
	for elapsed := range durations {
		samples = append(samples, elapsed)
	}
	if len(samples) != 100 {
		t.Fatalf("only %d successful write requests", len(samples))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	t.Logf("10 PIN sessions, 100 checklist HTTP writes: p95=%s", samples[94])
	if samples[94] > 500*time.Millisecond {
		t.Errorf("write smoke p95 exceeds 500ms: %s", samples[94])
	}

	var saved int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM shift_checklist_entries WHERE shift_id=$1 AND phase='closing' AND version=11 AND answer='yes'`, shiftID).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if saved != 10 {
		t.Fatalf("lost committed checklist writes: %d", saved)
	}

}
