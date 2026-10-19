package excelsync

import (
	"bytes"
	"context"
	"crypto/md5" // Drive's binary-file integrity field; never used for authentication.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"
)

var fileIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{10,200}$`)

type DriveClient struct {
	client       *http.Client
	transport    *http.Transport
	base, fileID string
}

// NewDriveClient uses only a configured service account and fixed Google hosts.
// The ChatGPT Google connection is not an application's server credential.
func NewDriveClient(ctx context.Context, credentialsPath, fileID string) (*DriveClient, error) {
	if !fileIDPattern.MatchString(fileID) {
		return nil, fail("EXCEL_CONFIG_INVALID")
	}
	f, err := os.Open(credentialsPath)
	if err != nil {
		return nil, fail("EXCEL_CONFIG_INVALID")
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return nil, fail("EXCEL_CONFIG_INVALID")
	}
	var identity struct {
		Type       string `json:"type"`
		TokenURL   string `json:"token_uri"`
		Email      string `json:"client_email"`
		PrivateKey string `json:"private_key"`
		KeyID      string `json:"private_key_id"`
	}
	if json.Unmarshal(data, &identity) != nil || identity.Type != "service_account" || identity.TokenURL != "https://oauth2.googleapis.com/token" || identity.PrivateKey == "" || !strings.HasSuffix(identity.Email, ".iam.gserviceaccount.com") {
		return nil, fail("EXCEL_CONFIG_INVALID")
	}
	config := jwt.Config{Email: identity.Email, PrivateKey: []byte(identity.PrivateKey), PrivateKeyID: identity.KeyID, TokenURL: identity.TokenURL, Scopes: []string{"https://www.googleapis.com/auth/drive"}}
	clear(data)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 10 * time.Second
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.MaxResponseHeaderBytes = 32 << 10
	base := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: noRedirect}
	tokenContext := context.WithValue(ctx, oauth2.HTTPClient, base)
	client := config.Client(tokenContext)
	client.Timeout = 15 * time.Second
	client.CheckRedirect = noRedirect
	return &DriveClient{client: client, transport: transport, base: "https://www.googleapis.com", fileID: fileID}, nil
}

// Close releases idle connections after the worker has stopped.
func (d *DriveClient) Close() {
	if d.transport != nil {
		d.transport.CloseIdleConnections()
	}
}

func noRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

func TargetHash(fileID string) string {
	sum := sha256.Sum256([]byte(fileID))
	return hex.EncodeToString(sum[:])
}

type fileMetadata struct {
	ID           string `json:"id"`
	MIME         string `json:"mimeType"`
	MD5          string `json:"md5Checksum"`
	Size         string `json:"size"`
	Trashed      bool   `json:"trashed"`
	Capabilities struct {
		CanEdit bool `json:"canEdit"`
	} `json:"capabilities"`
}

// Deliver performs one read/merge/conditional upload. Retry belongs to the
// durable queue. An ambiguous upload is reconciled by the next download, UUID,
// revision and digest, never by appending blindly or creating another file.
func (d *DriveClient) Deliver(ctx context.Context, r Record) error {
	metadata, etag, err := d.metadata(ctx)
	if err != nil {
		return err
	}
	if metadata.ID != d.fileID || metadata.MIME != MIME || metadata.Trashed || !metadata.Capabilities.CanEdit {
		return fail("EXCEL_FILE_ACCESS")
	}
	// Do not replace a shared workbook without a provider concurrency token.
	if etag == "" || strings.HasPrefix(etag, "W/") {
		return fail("EXCEL_ETAG_REQUIRED")
	}
	input, _, err := d.request(ctx, "GET", "/drive/v3/files/"+d.fileID+"?alt=media&supportsAllDrives=true", nil, nil, MaxFileSize)
	if err != nil {
		return err
	}
	if metadata.MD5 == "" || digestMD5(input) != metadata.MD5 {
		return &Failure{Code: "EXCEL_REMOTE_CONFLICT", Temporary: true}
	}
	output, changed, err := Apply(input, r)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	body, _, err := d.request(ctx, "PATCH", "/upload/drive/v3/files/"+d.fileID+"?uploadType=media&supportsAllDrives=true&fields=id,md5Checksum", output, map[string]string{"If-Match": etag, "Content-Type": MIME}, 64<<10)
	if err != nil {
		return err
	}
	var updated fileMetadata
	if json.Unmarshal(body, &updated) != nil || updated.ID != d.fileID || updated.MD5 != digestMD5(output) {
		return &Failure{Code: "EXCEL_UPLOAD_UNCONFIRMED", Temporary: true}
	}
	return nil
}

func (d *DriveClient) metadata(ctx context.Context) (fileMetadata, string, error) {
	body, headers, err := d.request(ctx, "GET", "/drive/v3/files/"+d.fileID+"?supportsAllDrives=true&fields=id,mimeType,md5Checksum,size,trashed,capabilities(canEdit)", nil, nil, 64<<10)
	if err != nil {
		return fileMetadata{}, "", err
	}
	var metadata fileMetadata
	if json.Unmarshal(body, &metadata) != nil {
		return metadata, "", fail("EXCEL_PROVIDER_RESPONSE")
	}
	return metadata, headers.Get("ETag"), nil
}

func (d *DriveClient) request(ctx context.Context, method, path string, body []byte, headers map[string]string, limit int64) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, d.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fail("EXCEL_CONFIG_INVALID")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	response, err := d.client.Do(req)
	if err != nil {
		var tokenError *oauth2.RetrieveError
		if errors.As(err, &tokenError) {
			return nil, nil, fail("EXCEL_AUTH_REQUIRED")
		}
		return nil, nil, &Failure{Code: "EXCEL_NETWORK_UNAVAILABLE", Temporary: true}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		code, temporary := "EXCEL_PROVIDER_RESPONSE", false
		switch response.StatusCode {
		case 401:
			code = "EXCEL_AUTH_REQUIRED"
		case 403, 404:
			code = "EXCEL_FILE_ACCESS"
		case 409, 412:
			code, temporary = "EXCEL_REMOTE_CONFLICT", true
		case 429:
			code, temporary = "EXCEL_RATE_LIMIT", true
		default:
			if response.StatusCode >= 500 {
				code, temporary = "EXCEL_PROVIDER_UNAVAILABLE", true
			}
		}
		// Error bodies can contain provider identities and URLs. Never propagate them.
		return nil, nil, &Failure{Code: code, Temporary: temporary}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, nil, &Failure{Code: "EXCEL_NETWORK_UNAVAILABLE", Temporary: true}
	}
	if int64(len(data)) > limit {
		return nil, nil, fail("EXCEL_FILE_TOO_LARGE")
	}
	return data, response.Header, nil
}

func digestMD5(data []byte) string { sum := md5.Sum(data); return hex.EncodeToString(sum[:]) }
