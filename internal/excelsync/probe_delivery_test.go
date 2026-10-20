package excelsync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestProbeDeliveryReconcilesCommittedUpload(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprintf("ambiguous=%t", ambiguous), func(t *testing.T) {
			t.Parallel()
			content := template(t)
			var mu sync.Mutex
			uploads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method == "PUT" {
					if r.Header.Get("If-Match") != fmt.Sprintf(`"v%d"`, uploads) {
						w.WriteHeader(412)
						return
					}
					content, _ = io.ReadAll(r.Body)
					uploads++
					if ambiguous && uploads == 1 {
						_, _ = w.Write([]byte(`{`))
						return
					}
					_ = json.NewEncoder(w).Encode(fileMetadata{ID: "probe-file-id", MD5: digestMD5(content)})
					return
				}
				if r.URL.Query().Get("alt") == "media" {
					_, _ = w.Write(content)
					return
				}
				switch r.URL.Query().Get("fields") {
				case "id,title":
					_ = json.NewEncoder(w).Encode(map[string]string{"id": "probe-file-id", "title": "PC Club Admin — проверка Drive.xlsx"})
				case "id,headRevisionId,md5Checksum":
					_ = json.NewEncoder(w).Encode(map[string]string{"id": "probe-file-id", "headRevisionId": fmt.Sprint(uploads), "md5Checksum": digestMD5(content)})
				default:
					meta := fileMetadata{ID: "probe-file-id", MIME: MIME, ETag: fmt.Sprintf(`"v%d"`, uploads), MD5: digestMD5(content)}
					meta.Capabilities.CanEdit = true
					_ = json.NewEncoder(w).Encode(meta)
				}
			}))
			defer server.Close()
			client := &DriveClient{client: server.Client(), base: server.URL, fileID: "probe-file-id"}
			err := client.ProbeDelivery(context.Background())
			if ambiguous {
				if err == nil || Code(err) != "EXCEL_UPLOAD_UNCONFIRMED" {
					t.Fatalf("ProbeDelivery(ambiguous upload) = %v, want EXCEL_UPLOAD_UNCONFIRMED", err)
				}
			} else if err != nil {
				t.Fatalf("ProbeDelivery(first delivery) = %v, want nil", err)
			}
			// A restarted CLI uses the same UUID and must reconcile an already committed upload.
			if err := client.ProbeDelivery(context.Background()); err != nil {
				t.Fatalf("ProbeDelivery(retry) = %v, want nil", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if uploads != 1 {
				t.Errorf("ProbeDelivery(retry) uploads = %d, want 1", uploads)
			}
		})
	}
}
