package excelsync

import (
	"bytes"
	"context"

	"github.com/xuri/excelize/v2"
)

// Check reads metadata and the workbook without uploads, queue changes or DB
// access. A successful read does not prove Google's conditional writes work.
func (d *DriveClient) Check(ctx context.Context) error {
	metadata, etag, err := d.metadata(ctx)
	if err != nil {
		return err
	}
	if metadata.ID != d.fileID || metadata.MIME != MIME || metadata.Trashed || !metadata.Capabilities.CanEdit {
		return fail("EXCEL_FILE_ACCESS")
	}
	if !strongETag(etag) {
		return fail("EXCEL_ETAG_REQUIRED")
	}
	input, _, err := d.request(ctx, "GET", "/drive/v2/files/"+d.fileID+"?alt=media&supportsAllDrives=true", nil, nil, MaxFileSize)
	if err != nil {
		return err
	}
	if metadata.MD5 == "" || digestMD5(input) != metadata.MD5 {
		return fail("EXCEL_REMOTE_CONFLICT")
	}
	if err := checkArchive(input); err != nil {
		return err
	}
	workbook, err := excelize.OpenReader(bytes.NewReader(input), excelize.Options{UnzipSizeLimit: 64 << 20, UnzipXMLSizeLimit: 8 << 20})
	if err != nil {
		return fail("EXCEL_INVALID_WORKBOOK")
	}
	defer func() { _ = workbook.Close() }()
	if err := checkHeaders(workbook); err != nil {
		return err
	}
	rows, err := workbook.GetRows(Sheet, excelize.Options{RawCellValue: true})
	if err != nil || len(rows) > maxRows {
		return fail("EXCEL_ROW_LIMIT")
	}
	return nil
}
