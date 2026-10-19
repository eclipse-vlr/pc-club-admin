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
