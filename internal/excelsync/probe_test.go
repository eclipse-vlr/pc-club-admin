package excelsync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeUpload(t *testing.T) {
	content := template(t)
	for _, tc := range []struct {
		name, title string
		reject      int
		want        string
		puts        int
	}{
		{"conditional", "PC Club Admin — проверка Drive.xlsx", 412, "", 2},
		{"ordinary file", "Рабочий.xlsx", 412, "EXCEL_PROBE_COPY_REQUIRED", 0},
		{"ignores condition", "PC Club Admin — проверка Drive.xlsx", 200, "EXCEL_PRECONDITION_UNCONFIRMED", 1},
		{"conflict is not proof", "PC Club Admin — проверка Drive.xlsx", 409, "EXCEL_PRECONDITION_UNCONFIRMED", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			puts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" {
					puts++
					data, _ := io.ReadAll(r.Body)
					if string(data) != string(content) {
						t.Error("probe changed workbook bytes")
					}
					if puts == 1 {
						w.WriteHeader(tc.reject)
						return
					}
					if r.Header.Get("If-Match") != `"v1"` {
						t.Error("positive write did not preserve original ETag")
					}
					_ = json.NewEncoder(w).Encode(fileMetadata{ID: "probe-file-id", MD5: digestMD5(content)})
					return
				}
				if r.URL.Query().Get("alt") == "media" {
					_, _ = w.Write(content)
					return
				}
				if r.URL.Query().Get("fields") == "id,title" {
					_ = json.NewEncoder(w).Encode(map[string]string{"id": "probe-file-id", "title": tc.title})
					return
				}
				meta := fileMetadata{ID: "probe-file-id", MIME: MIME, ETag: `"v1"`, MD5: digestMD5(content)}
				meta.Capabilities.CanEdit = true
				_ = json.NewEncoder(w).Encode(meta)
			}))
			defer server.Close()
			client := &DriveClient{client: server.Client(), base: server.URL, fileID: "probe-file-id"}
			err := client.ProbeUpload(context.Background())
			got := ""
			if err != nil {
				got = Code(err)
			}
			if got != tc.want || puts != tc.puts {
				t.Fatalf("code=%q puts=%d; want %q %d", got, puts, tc.want, tc.puts)
			}
		})
	}
}
