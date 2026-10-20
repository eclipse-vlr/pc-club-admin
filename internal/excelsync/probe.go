package excelsync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
)

// ProbeUpload writes identical bytes as a new revision of a disposable copy.
// It proves conditional upload, not queue recovery or checklist delivery.
func (d *DriveClient) ProbeUpload(ctx context.Context) error {
	if err := d.checkProbeCopy(ctx); err != nil {
		return err
	}
	if err := d.Check(ctx); err != nil {
		return err
	}
	metadata, etag, err := d.metadata(ctx)
	if err != nil {
		return err
	}
	if metadata.ID != d.fileID || metadata.MIME != MIME || metadata.Trashed || !metadata.Capabilities.CanEdit || !strongETag(etag) {
		return fail("EXCEL_FILE_ACCESS")
	}
	input, _, err := d.request(ctx, "GET", "/drive/v2/files/"+d.fileID+"?alt=media&supportsAllDrives=true", nil, nil, MaxFileSize)
	if err != nil {
		return err
	}
	if metadata.MD5 == "" || digestMD5(input) != metadata.MD5 {
		return fail("EXCEL_REMOTE_CONFLICT")
	}
	path := "/upload/drive/v2/files/" + d.fileID + "?uploadType=media&newRevision=true&supportsAllDrives=true&fields=id,md5Checksum"
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return fail("EXCEL_PROBE_UNAVAILABLE")
	}
	_, _, err = d.request(ctx, "PUT", path, input, map[string]string{"If-Match": `"pcclub-probe-` + hex.EncodeToString(nonce) + `"`, "Content-Type": MIME}, 64<<10)
	var failure *Failure
	if !errors.As(err, &failure) || failure.HTTPStatus != http.StatusPreconditionFailed {
		return fail("EXCEL_PRECONDITION_UNCONFIRMED")
	}
	// Keep the original token: any intervening edit must reject the positive write.
	body, _, err := d.request(ctx, "PUT", path, input, map[string]string{"If-Match": etag, "Content-Type": MIME}, 64<<10)
	if err != nil {
		return err
	}
	var updated fileMetadata
	if json.Unmarshal(body, &updated) != nil || updated.ID != d.fileID || updated.MD5 != metadata.MD5 {
		return fail("EXCEL_UPLOAD_UNCONFIRMED")
	}
	if err := d.Check(ctx); err != nil {
		return err
	}
	after, _, err := d.metadata(ctx)
	if err != nil {
		return err
	}
	if after.MD5 != metadata.MD5 {
		return fail("EXCEL_REMOTE_CONFLICT")
	}
	return nil
}

func (d *DriveClient) checkProbeCopy(ctx context.Context) error {
	body, _, err := d.request(ctx, "GET", "/drive/v2/files/"+d.fileID+"?fields=id,title&supportsAllDrives=true", nil, nil, 64<<10)
	if err != nil {
		return err
	}
	var identity struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if json.Unmarshal(body, &identity) != nil || identity.ID != d.fileID || identity.Title != "PC Club Admin — проверка Drive.xlsx" {
		return fail("EXCEL_PROBE_COPY_REQUIRED")
	}
	return nil
}
