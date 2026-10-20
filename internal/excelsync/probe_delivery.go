package excelsync

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"pc-club-admin/internal/checklists"
)

// ProbeDelivery delivers a stable synthetic snapshot twice to a disposable copy.
// This checks reconciliation after a committed upload, not a process/DB crash.
func (d *DriveClient) ProbeDelivery(ctx context.Context) error {
	if err := d.checkProbeCopy(ctx); err != nil {
		return err
	}
	record := probeRecord(d.fileID)
	if err := d.Deliver(ctx, record); err != nil {
		return err
	}
	before, err := d.probeRevision(ctx)
	if err != nil {
		return err
	}
	if err := d.verifyProbeRow(ctx, record); err != nil {
		return err
	}
	if err := d.Deliver(ctx, record); err != nil {
		return err
	}
	if err := d.verifyProbeRow(ctx, record); err != nil {
		return err
	}
	after, err := d.probeRevision(ctx)
	if err != nil {
		return err
	}
	if after != before {
		return fail("EXCEL_PROBE_REPEAT_CHANGED")
	}
	return nil
}

func probeRecord(fileID string) Record {
	hash := sha256.Sum256([]byte("pc-club-drive-probe-v1:" + fileID))
	// A stable version-4 shaped UUID prevents duplicates across CLI runs.
	hash[6] = hash[6]&0x0f | 0x40
	hash[8] = hash[8]&0x3f | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", hash[:4], hash[4:6], hash[6:8], hash[8:10], hash[10:16])
	r := Record{ID: id, OwnerName: "ТЕСТ подключения Drive — не рабочая смена", BusinessDate: "2000-01-01", Kind: "night", Status: "opening", Version: 1}
	for i, c := range columns {
		r.Entries = append(r.Entries, checklists.Entry{Code: c.code, Phase: c.phase, Label: c.label, Required: true, Allowed: []string{"yes"}, Answer: "unanswered", Order: i + 1})
	}
	return r
}

func (d *DriveClient) probeRevision(ctx context.Context) (string, error) {
	body, _, err := d.request(ctx, "GET", "/drive/v2/files/"+d.fileID+"?fields=id,headRevisionId,md5Checksum&supportsAllDrives=true", nil, nil, 64<<10)
	if err != nil {
		return "", err
	}
	var revision struct {
		ID   string `json:"id"`
		Head string `json:"headRevisionId"`
		MD5  string `json:"md5Checksum"`
	}
	if json.Unmarshal(body, &revision) != nil || revision.ID != d.fileID || revision.Head == "" || revision.MD5 == "" {
		return "", fail("EXCEL_PROBE_REVISION_REQUIRED")
	}
	return revision.Head + ":" + revision.MD5, nil
}

func (d *DriveClient) verifyProbeRow(ctx context.Context, record Record) error {
	meta, _, err := d.metadata(ctx)
	if err != nil {
		return err
	}
	data, _, err := d.request(ctx, "GET", "/drive/v2/files/"+d.fileID+"?alt=media&supportsAllDrives=true", nil, nil, MaxFileSize)
	if err != nil {
		return err
	}
	if meta.ID != d.fileID || meta.MD5 == "" || digestMD5(data) != meta.MD5 {
		return fail("EXCEL_REMOTE_CONFLICT")
	}
	// Apply rejects duplicate UUIDs and tampered rows; an exact existing row is a no-op.
	_, changed, err := Apply(data, record)
	if err != nil {
		return err
	}
	if changed {
		return fail("EXCEL_PROBE_ROW_UNCONFIRMED")
	}
	return nil
}
