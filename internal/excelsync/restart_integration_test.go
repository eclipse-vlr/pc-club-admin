//go:build integration

package excelsync_test

import (
	"bytes"
	"context"
	"crypto/md5" // Fake provider checksum, not authentication.
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/excelsync"
	"pc-club-admin/internal/shifts"
	"pc-club-admin/internal/storage/postgres"
)

const restartFileID = "synthetic-restart-file"

// A separate database isolates the database-wide advisory lock from concurrent
// HTTP integration tests. Only the newly created *_test database is dropped.
func restartDatabase(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	source := os.Getenv("TEST_DATABASE_URL")
	u, err := url.Parse(source)
	if err != nil || source == "" || !strings.HasSuffix(u.Path, "_test") || os.Getenv("APP_ENV") == "production" {
		t.Fatal("dedicated TEST_DATABASE_URL (*_test) and test mode required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, source)
	if err != nil {
		t.Fatal("test admin connection configuration unavailable")
	}
	database := fmt.Sprintf("excel_restart_%d_test", time.Now().UnixNano())
	name := pgx.Identifier{database}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal("restart tests require CREATE DATABASE privilege on the isolated test server")
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error("could not remove isolated restart database")
		}
		admin.Close()
	})
	u.Path = "/" + database
	target := u.String()
	pool, err = pgxpool.New(ctx, target)
	if err != nil {
		t.Fatal("isolated database configuration unavailable")
	}
	if err := postgres.Migrate(ctx, pool, "Europe/Moscow"); err != nil {
		t.Fatal("isolated database migration failed")
	}
	return pool, target
}

type fakeWorkbook struct {
	mu                          sync.Mutex
	content                     []byte
	uploads, attempts, revision int
	before                      bool
	reached, release            chan struct{}
}

func (f *fakeWorkbook) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/drive/v2/files/"+restartFileID && r.URL.Path != "/upload/drive/v2/files/"+restartFileID {
		w.WriteHeader(404)
		return
	}
	f.mu.Lock()
	if r.Method == "GET" {
		defer f.mu.Unlock()
		if r.URL.Query().Get("alt") == "media" {
			_, _ = w.Write(f.content)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": restartFileID, "mimeType": excelsync.MIME, "etag": fmt.Sprintf(`"v%d"`, f.revision), "md5Checksum": fmt.Sprintf("%x", md5.Sum(f.content)), "capabilities": map[string]bool{"canEdit": true}})
		return
	}
	if r.Method != "PUT" {
		f.mu.Unlock()
		w.WriteHeader(405)
		return
	}
	if r.Header.Get("If-Match") != fmt.Sprintf(`"v%d"`, f.revision) {
		f.mu.Unlock()
		w.WriteHeader(412)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, excelsync.MaxFileSize+1))
	if err != nil || len(body) > excelsync.MaxFileSize {
		f.mu.Unlock()
		w.WriteHeader(400)
		return
	}
	f.attempts++
	first := f.attempts == 1
	if !(first && f.before) {
		f.content = body
		f.uploads++
		f.revision++
	}
	if first {
		close(f.reached)
	}
	digest := fmt.Sprintf("%x", md5.Sum(f.content))
	f.mu.Unlock()
	if first {
		// The parent kills the client while the result is unknown to the relay.
		select {
		case <-r.Context().Done():
			return
		case <-f.release:
			w.WriteHeader(503)
			return
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"id": restartFileID, "md5Checksum": digest})
}

// TestExcelProcessHelper is re-executed as an actual child process, not a mocked
// callback. It deliberately runs only against our new DB and loopback provider.
func TestExcelProcessHelper(t *testing.T) {
	if os.Getenv("PC_CLUB_RESTART_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	dsn := os.Getenv("PC_CLUB_RESTART_DATABASE")
	parsed, err := url.Parse(dsn)
	if err != nil || !strings.HasPrefix(parsed.Path, "/excel_restart_") || !strings.HasSuffix(parsed.Path, "_test") || os.Getenv("APP_ENV") == "production" {
		t.Fatal("isolated helper database required")
	}
	endpoint := os.Getenv("PC_CLUB_RESTART_ENDPOINT")
	remote, err := url.Parse(endpoint)
	if err != nil || remote.Scheme != "http" || remote.Hostname() != "127.0.0.1" || remote.Port() == "" || remote.User != nil || remote.Path != "" || remote.RawQuery != "" || remote.Fragment != "" {
		t.Fatal("loopback-only provider required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("helper DB configuration unavailable")
	}
	defer pool.Close()
	repo := postgres.ExcelRepository{Pool: pool}
	target := excelsync.TargetHash(restartFileID)
	if err := repo.PrepareTarget(ctx, target); err != nil {
		t.Fatal("helper queue preparation failed")
	}
	client := excelsync.DriveClientForTest(&http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, endpoint, restartFileID)
	defer client.Close()
	excelsync.Run(ctx, repo, target, client.Deliver, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func startRelay(t *testing.T, ctx context.Context, database, endpoint string) (func(), <-chan error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, executable, "-test.run=^TestExcelProcessHelper$", "-test.timeout=35s")
	// No real Drive credentials or application environment is needed in the child.
	child.Env = []string{"APP_ENV=test", "PC_CLUB_RESTART_HELPER=1", "PC_CLUB_RESTART_DATABASE=" + database, "PC_CLUB_RESTART_ENDPOINT=" + endpoint}
	child.WaitDelay = 2 * time.Second
	var output bytes.Buffer
	child.Stdout, child.Stderr = &output, &output
	if err := child.Start(); err != nil {
		t.Fatal("could not start isolated relay process")
	}
	done := make(chan error, 1)
	go func() {
		done <- child.Wait()
		close(done)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = child.Process.Kill()
			select {
			case <-done:
				if bytes.Contains(output.Bytes(), []byte("WARNING: DATA RACE")) {
					t.Errorf("race detected in relay process:\n%s", output.String())
				}
			case <-time.After(5 * time.Second):
				t.Error("relay process did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop, done
}

func TestExcelRelayRecoversAfterProcessCrash(t *testing.T) {
	for _, tc := range []struct {
		name            string
		before, newEdit bool
		uploads         int
	}{
		{"before remote commit", true, false, 1},
		{"after remote commit", false, false, 1},
		{"new edit while relay stopped", false, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, database := restartDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			access := auth.New(postgres.AuthRepository{Pool: pool}, "synthetic-restart-pepper-at-least-32-characters")
			password := "synthetic-restart-manager-password"
			if err := access.Bootstrap(ctx, auth.UserInput{Name: "Синтетический управляющий", Login: "manager", Password: password, Roles: []string{"manager"}}); err != nil {
				t.Fatal(err)
			}
			login, err := access.Login(ctx, auth.Request{Source: "192.0.2.230"}, "manager", password, false)
			if err != nil {
				t.Fatal(err)
			}
			manager := auth.Request{SessionToken: login.Token, CSRF: login.CSRF, ActorID: login.Identity.User.ID, Source: "192.0.2.230"}
			owner, err := access.CreateUser(ctx, manager, auth.UserInput{Name: "Синтетический администратор", Roles: []string{"admin"}, PIN: "123456"})
			if err != nil {
				t.Fatal(err)
			}
			work := shifts.New(postgres.ShiftRepository{Pool: pool}, access, "Europe/Moscow", false)
			zero := int64(0)
			shift, _, err := work.Bootstrap(ctx, manager, shifts.BootstrapInput{OwnerID: owner.ID, BusinessDate: "2026-10-20", Kind: "day", InitialCash: &zero, Reason: "Синтетический crash-тест"}, "synthetic-restart-bootstrap-key")
			if err != nil {
				t.Fatal(err)
			}
			remote := &fakeWorkbook{content: excelsync.WorkbookForTest(t), revision: 1, before: tc.before, reached: make(chan struct{}), release: make(chan struct{})}
			server := httptest.NewServer(http.HandlerFunc(remote.serve))
			t.Cleanup(func() { close(remote.release); server.Close() })
			stop, done := startRelay(t, ctx, database, server.URL)
			select {
			case <-remote.reached:
			case <-done:
				t.Fatal("relay exited before upload boundary")
			case <-ctx.Done():
				t.Fatal("relay did not reach upload boundary")
			}
			stop()
			var synced int64
			var success *time.Time
			if err := pool.QueryRow(ctx, "SELECT synced_version,last_success_at FROM excel_sync_queue WHERE shift_id=$1", shift.ID).Scan(&synced, &success); err != nil {
				t.Fatal(err)
			}
			if synced != 0 || success != nil {
				t.Fatal("killed relay falsely confirmed delivery")
			}
			expected := int64(1)
			if tc.newEdit {
				code, err := access.Enroll(ctx, manager, password)
				if err != nil {
					t.Fatal(err)
				}
				device, err := access.RegisterDevice(ctx, auth.Request{Source: "192.0.2.231"}, code, "Синтетический браузер")
				if err != nil {
					t.Fatal(err)
				}
				pin, err := access.Login(ctx, auth.Request{Source: "192.0.2.231", DeviceToken: device}, owner.ID, "123456", true)
				if err != nil {
					t.Fatal(err)
				}
				actor := auth.Request{Source: "192.0.2.231", DeviceToken: device, SessionToken: pin.Token, CSRF: pin.CSRF, ActorID: owner.ID}
				if _, err := work.Answer(ctx, actor, shift.ID, "accept_d", shifts.AnswerInput{Answer: "yes", Version: 1}); err != nil {
					t.Fatal(err)
				}
				expected = 2
			}
			stopAgain, doneAgain := startRelay(t, ctx, database, server.URL)
			ticker := time.NewTicker(25 * time.Millisecond)
			defer ticker.Stop()
			for synced != expected {
				select {
				case <-ticker.C:
				case <-doneAgain:
					t.Fatal("restarted relay exited before confirmation")
				case <-ctx.Done():
					t.Fatal("relay did not recover queue/advisory lock after crash")
				}
				if err := pool.QueryRow(ctx, "SELECT synced_version,last_success_at FROM excel_sync_queue WHERE shift_id=$1", shift.ID).Scan(&synced, &success); err != nil {
					t.Fatal(err)
				}
			}
			stopAgain()
			if success == nil {
				t.Fatal("delivery confirmation has no success time")
			}
			remote.mu.Lock()
			content, uploads := bytes.Clone(remote.content), remote.uploads
			remote.mu.Unlock()
			if uploads != tc.uploads {
				t.Errorf("remote uploads after restart = %d, want %d", uploads, tc.uploads)
			}
			book, err := excelize.OpenReader(bytes.NewReader(content))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = book.Close() }()
			rows, err := book.GetRows(excelsync.Sheet)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, row := range rows {
				if len(row) > 37 && row[36] == shift.ID {
					count++
					if row[37] != fmt.Sprint(expected) {
						t.Errorf("remote row revision = %s, want %d", row[37], expected)
					}
					if tc.newEdit && row[3] != "Да" {
						t.Error("new committed answer was lost after crash")
					}
				}
			}
			if count != 1 {
				t.Errorf("remote rows for shift = %d, want 1", count)
			}
			var audits int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='excel_synchronization' AND entity_id=$1 AND success", shift.ID).Scan(&audits); err != nil {
				t.Fatal(err)
			}
			if audits != 1 {
				t.Errorf("committed success audits = %d, want 1", audits)
			}
		})
	}
}
