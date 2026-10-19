package excelsync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestConditionalUploadRecoversUnknownOutcomeWithoutDuplicate(t *testing.T) {
	var mu sync.Mutex
	content := template(t)
	revision, uploads := 1, 0
	loseResponse := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !strings.Contains(r.URL.Path, "synthetic-file-id") {
			t.Error("unexpected destination")
			w.WriteHeader(404)
			return
		}
		if r.Method == "PUT" {
			if r.URL.Path != "/upload/drive/v2/files/synthetic-file-id" || r.URL.Query().Get("uploadType") != "media" || r.URL.Query().Get("newRevision") != "true" {
				t.Error("wrong v2 conditional upload endpoint")
				w.WriteHeader(400)
				return
			}
			uploads++
			if r.Header.Get("If-Match") != `"v`+strconv.Itoa(revision)+`"` {
				w.WriteHeader(412)
				return
			}
			if r.Header.Get("Content-Type") != MIME {
				t.Error("incorrect file MIME")
			}
			var err error
			content, err = io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			revision++
			if loseResponse {
				loseResponse = false
				w.WriteHeader(503)
				return
			}
			_ = json.NewEncoder(w).Encode(fileMetadata{ID: "synthetic-file-id", MD5: digestMD5(content)})
			return
		}
		if r.URL.Query().Get("alt") == "media" {
			_, _ = w.Write(content)
			return
		}
		if r.Method != "GET" || r.URL.Path != "/drive/v2/files/synthetic-file-id" {
			t.Error("wrong v2 read endpoint")
			w.WriteHeader(400)
			return
		}
		metadata := fileMetadata{ID: "synthetic-file-id", MIME: MIME, MD5: digestMD5(content), ETag: `"v` + strconv.Itoa(revision) + `"`}
		metadata.Capabilities.CanEdit = true
		_ = json.NewEncoder(w).Encode(metadata)
	}))
	defer server.Close()
	client := &DriveClient{client: server.Client(), base: server.URL, fileID: "synthetic-file-id"}
	r := snapshot()
	if err := client.Deliver(context.Background(), r); Code(err) != "EXCEL_PROVIDER_UNAVAILABLE" {
		t.Fatalf("ambiguous upload not surfaced: %v", err)
	}
	if err := client.Deliver(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if uploads != 1 {
		t.Fatal("reconciliation wrote a duplicate after a lost response")
	}
	f := openWorkbook(t, content)
	if value(t, f, Sheet, "AK7") != r.ID || value(t, f, Sheet, "AK8") != "" {
		t.Fatal("duplicate row or lost snapshot")
	}
}

func TestDriveRejectsConcurrentEditAndMissingToken(t *testing.T) {
	for _, mode := range []string{"conflict", "no-etag", "wrong-mime", "too-large", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			content := template(t)
			uploads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" {
					uploads++
					w.WriteHeader(412)
					return
				}
				if r.URL.Query().Get("alt") == "media" {
					if mode == "too-large" {
						_, _ = w.Write(make([]byte, MaxFileSize+1))
						return
					}
					_, _ = w.Write(content)
					return
				}
				if mode == "redirect" {
					w.Header().Set("Location", "https://example.invalid/private")
					w.WriteHeader(302)
					return
				}
				if mode != "no-etag" {
					w.Header().Set("ETag", `"v1"`)
				}
				metadata := fileMetadata{ID: "synthetic-file-id", MIME: MIME, MD5: digestMD5(content)}
				metadata.Capabilities.CanEdit = true
				if mode == "wrong-mime" {
					metadata.MIME = "application/vnd.google-apps.spreadsheet"
				}
				_ = json.NewEncoder(w).Encode(metadata)
			}))
			defer server.Close()
			httpClient := server.Client()
			httpClient.CheckRedirect = noRedirect
			client := &DriveClient{client: httpClient, base: server.URL, fileID: "synthetic-file-id"}
			err := client.Deliver(context.Background(), snapshot())
			want := map[string]string{"conflict": "EXCEL_REMOTE_CONFLICT", "no-etag": "EXCEL_ETAG_REQUIRED", "wrong-mime": "EXCEL_FILE_ACCESS", "too-large": "EXCEL_FILE_TOO_LARGE", "redirect": "EXCEL_PROVIDER_RESPONSE"}[mode]
			if Code(err) != want {
				t.Fatalf("got %v want %s", err, want)
			}
			if mode != "conflict" && uploads != 0 {
				t.Fatal("unsafe upload attempted")
			}
		})
	}
}
