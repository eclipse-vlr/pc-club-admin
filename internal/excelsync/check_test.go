package excelsync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConnectionCheckIsReadOnly(t *testing.T) {
	content := template(t)
	for _, test := range []struct {
		name, etag   string
		edit         bool
		digest, code string
	}{
		{"valid", `"v1"`, true, digestMD5(content), ""},
		{"read only", `"v1"`, false, digestMD5(content), "EXCEL_FILE_ACCESS"},
		{"no conditional token", "", true, digestMD5(content), "EXCEL_ETAG_REQUIRED"},
		{"weak token", `W/"v1"`, true, digestMD5(content), "EXCEL_ETAG_REQUIRED"},
		{"checksum mismatch", `"v1"`, true, "invalid", "EXCEL_REMOTE_CONFLICT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("check attempted a write")
					w.WriteHeader(405)
					return
				}
				if r.URL.Query().Get("alt") == "media" {
					_, _ = w.Write(content)
					return
				}
				w.Header().Set("ETag", test.etag)
				metadata := fileMetadata{ID: "synthetic-check-file", MIME: MIME, MD5: test.digest}
				metadata.Capabilities.CanEdit = test.edit
				_ = json.NewEncoder(w).Encode(metadata)
			}))
			defer server.Close()
			client := &DriveClient{client: server.Client(), base: server.URL, fileID: "synthetic-check-file"}
			err := client.Check(context.Background())
			got := ""
			if err != nil {
				got = Code(err)
			}
			if got != test.code {
				t.Fatalf("got %q want %q", got, test.code)
			}
		})
	}
}

func TestV2BodyETagAndTrashedFile(t *testing.T) {
	content := template(t)
	for _, test := range []struct {
		name, bodyTag, headerTag string
		trashed                  bool
		code                     string
	}{
		{"body only", `"body-v1"`, "", false, ""},
		{"matching header", `"body-v1"`, `"body-v1"`, false, ""},
		{"mismatched header", `"body-v1"`, `"other"`, false, "EXCEL_PROVIDER_RESPONSE"},
		{"trashed", `"body-v1"`, "", true, "EXCEL_FILE_ACCESS"},
		{"weak body", `W/"body-v1"`, "", false, "EXCEL_ETAG_REQUIRED"},
		{"wildcard", "*", "", false, "EXCEL_ETAG_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/drive/v2/files/synthetic-check-file" {
					t.Error("unexpected API path or write")
					w.WriteHeader(400)
					return
				}
				if r.URL.Query().Get("alt") == "media" {
					_, _ = w.Write(content)
					return
				}
				if r.URL.Query().Get("fields") != "id,mimeType,md5Checksum,fileSize,labels(trashed),capabilities(canEdit),etag" {
					t.Error("missing v2 fields")
				}
				if test.headerTag != "" {
					w.Header().Set("ETag", test.headerTag)
				}
				metadata := fileMetadata{ID: "synthetic-check-file", MIME: MIME, MD5: digestMD5(content), ETag: test.bodyTag}
				metadata.Capabilities.CanEdit = true
				metadata.Labels.Trashed = test.trashed
				_ = json.NewEncoder(w).Encode(metadata)
			}))
			defer server.Close()
			client := &DriveClient{client: server.Client(), base: server.URL, fileID: "synthetic-check-file"}
			err := client.Check(context.Background())
			got := ""
			if err != nil {
				got = Code(err)
			}
			if got != test.code {
				t.Fatalf("got %q want %q", got, test.code)
			}
		})
	}
}
