//go:build integration

package excelsync

import (
	"net/http"
	"testing"
)

// DriveClientForTest exposes the transport seam only to this package's test
// binary. Production constructors still require the fixed Google endpoints.
func DriveClientForTest(client *http.Client, base, fileID string) *DriveClient {
	return &DriveClient{client: client, base: base, fileID: fileID}
}

func WorkbookForTest(t *testing.T) []byte {
	t.Helper()
	return template(t)
}
