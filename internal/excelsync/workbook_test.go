package excelsync

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
	"pc-club-admin/internal/checklists"
)

func template(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	t.Cleanup(func() { _ = f.Close() })
	if err := f.SetSheetName("Sheet1", Sheet); err != nil {
		t.Fatal(err)
	}
	headers := []string{"Дата", "Админ", "Смена"}
	for _, c := range columns {
		headers = append(headers, c.label)
	}
	headers = append(headers, "Выполнено, %", "Комментарий")
	for i, h := range headers {
		if err := f.SetCellStr(Sheet, cell(i+1, 5), h); err != nil {
			t.Fatal(err)
		}
	}
	for c := 1; c <= 36; c++ {
		style, err := f.NewStyle(&excelize.Style{Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFF2CC"}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetCellStyle(Sheet, cell(c, 6), cell(c, 6), style); err != nil {
			t.Fatal(err)
		}
	}
	for address, value := range map[string]string{"A6": "2026-10-18", "B6": "Историческая строка", "D6": "Да", "AJ6": "Сохранить историю"} {
		if err := f.SetCellStr(Sheet, address, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.SetCellFormula(Sheet, "AI6", `IFERROR(COUNTIF(D6:AH6,"Да")/COUNTIF(D6:AH6,"<>"),"")`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Остаток наличных в кассе", "Реестр проблем"} {
		if _, err := f.NewSheet(name); err != nil {
			t.Fatal(err)
		}
		if err := f.SetCellStr(name, "A1", "Не изменять другие листы"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.SetCellFormula("Остаток наличных в кассе", "G5", "D5-F5"); err != nil {
		t.Fatal(err)
	}
	dv := excelize.NewDataValidation(true)
	dv.Sqref = "D6:AH96"
	if err := dv.SetDropList([]string{"Да", "Нет", "—"}); err != nil {
		t.Fatal(err)
	}
	if err := f.AddDataValidation(Sheet, dv); err != nil {
		t.Fatal(err)
	}
	buffer, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func snapshot() Record {
	r := Record{ID: "11111111-1111-1111-1111-111111111111", OwnerName: "Синтетический сотрудник", BusinessDate: "2026-10-19", Kind: "night", Status: "opening", Version: 1}
	for i, c := range columns {
		r.Entries = append(r.Entries, checklists.Entry{Code: c.code, Phase: c.phase, Label: c.label, Required: true, Allowed: []string{"yes"}, Answer: "unanswered", Order: i + 1})
	}
	r.Entries[26].Allowed = []string{"yes", "no"}
	return r
}

func openWorkbook(t *testing.T, body []byte) *excelize.File {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}
func value(t *testing.T, f *excelize.File, sheet, address string) string {
	t.Helper()
	v, err := f.GetCellValue(sheet, address, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestWorkbookPreservesHistoryAndMirrorsOneRevision(t *testing.T) {
	input := template(t)
	r := snapshot()
	r.Entries[0].Answer = "yes"
	r.Entries[1].Comment = "=HYPERLINK(\"https://example.invalid\",\"text\")"
	body, changed, err := Apply(input, r)
	if err != nil || !changed {
		t.Fatalf("first projection: changed=%v err=%v", changed, err)
	}
	f := openWorkbook(t, body)
	for address, want := range map[string]string{"A7": "2026-10-19", "B7": r.OwnerName, "C7": "Ночная 21:00–09:00", "D7": "Да", "E7": "", "AK7": r.ID, "AL7": "1", "AM7": "ТЕСТ — Черновик приёма"} {
		if got := value(t, f, Sheet, address); got != want {
			t.Fatalf("%s: got %q want %q", address, got, want)
		}
	}
	if value(t, f, Sheet, "B6") != "Историческая строка" || value(t, f, "Реестр проблем", "A1") != "Не изменять другие листы" {
		t.Fatal("history or other sheet changed")
	}
	formula, err := f.GetCellFormula("Остаток наличных в кассе", "G5")
	if err != nil || formula != "D5-F5" {
		t.Fatal("cash formula changed")
	}
	formula, err = f.GetCellFormula(Sheet, "AJ7")
	if err != nil || formula != "" {
		t.Fatal("untrusted text became a formula")
	}
	formula, err = f.GetCellFormula(Sheet, "AI7")
	if err != nil || formula != "" {
		t.Fatal("old Excel progress formula used")
	}
	if !strings.Contains(value(t, f, Sheet, "AJ7"), "=HYPERLINK") {
		t.Fatal("comment was lost")
	}
	one := value(t, f, Sheet, "AI7")
	legacyFormula, err := f.GetCellFormula(Sheet, "AI6")
	if one == "1" || one == "1.000000" || err != nil || legacyFormula == "" {
		t.Fatalf("incorrect progress or damaged original formula: %q", one)
	}
	style6, err := f.GetCellStyle(Sheet, "D6")
	if err != nil {
		t.Fatal(err)
	}
	style7, err := f.GetCellStyle(Sheet, "D7")
	if err != nil || style6 != style7 {
		t.Fatal("template style not copied")
	}
	dv, err := f.GetDataValidations(Sheet)
	if err != nil || len(dv) != 1 {
		t.Fatal("overlapping validation rules added")
	}
	replay, replayed, err := Apply(body, r)
	if err != nil || replayed || !bytes.Equal(body, replay) {
		t.Fatalf("same revision not a no-op: %v", err)
	}
	r.Version++
	r.Entries[1].Answer = "no"
	newer, changed, err := Apply(body, r)
	if err != nil || !changed {
		t.Fatal(err)
	}
	f = openWorkbook(t, newer)
	if value(t, f, Sheet, "E7") != "Нет" || value(t, f, Sheet, "AK8") != "" || value(t, f, Sheet, "AL7") != "2" {
		t.Fatal("update duplicated a shift")
	}
	r.Version = 1
	_, changed, err = Apply(newer, r)
	if err != nil || changed {
		t.Fatal("older delivery overwrote a newer revision")
	}
}

func TestWorkbookUsesServerCompletionAndDetectsManualChanges(t *testing.T) {
	r := snapshot()
	for i := range r.Entries {
		r.Entries[i].Answer = "yes"
	}
	r.Entries[26].Answer = "no" // the sole permitted negative answer
	r.Entries[5].EquipmentCheck = true
	r.Entries[5].EquipmentIssue = true
	body, _, err := Apply(template(t), r)
	if err != nil {
		t.Fatal(err)
	}
	f := openWorkbook(t, body)
	if value(t, f, Sheet, "AI7") == "1.000000" {
		t.Fatal("missing equipment note became 100%")
	}
	r.Version++
	r.Notes = []checklists.Note{{Code: "accept_i", Category: "equipment", Object: "ПК-01", Text: "Поломка записана, ремонта пока нет"}}
	body, _, err = Apply(body, r)
	if err != nil {
		t.Fatal(err)
	}
	f = openWorkbook(t, body)
	if value(t, f, Sheet, "AI7") != "1.000000" {
		t.Fatal("recorded unrepaired issue should allow completion")
	}
	if err := f.SetCellStr(Sheet, "AJ7", "Ручная правка"); err != nil {
		t.Fatal(err)
	}
	b, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	r.Version++
	if _, _, err := Apply(b.Bytes(), r); Code(err) != "EXCEL_ROW_EDITED" {
		t.Fatalf("manual edits lost: %v", err)
	}
}

func TestWorkbookRejectsIncompatibleOrOversizedData(t *testing.T) {
	r := snapshot()
	r.Entries[0].Comment = strings.Repeat("x", 32768)
	if _, _, err := Apply(template(t), r); Code(err) != "EXCEL_TEXT_TOO_LONG" {
		t.Fatalf("silent truncation: %v", err)
	}
	r = snapshot()
	r.Entries = r.Entries[:30]
	if _, _, err := Apply(template(t), r); Code(err) != "EXCEL_SNAPSHOT_INVALID" {
		t.Fatal("missing checklist item accepted")
	}
	r = snapshot()
	f := openWorkbook(t, template(t))
	if err := f.SetCellStr(Sheet, "D5", "Другой шаблон"); err != nil {
		t.Fatal(err)
	}
	b, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Apply(b.Bytes(), r); Code(err) != "EXCEL_TEMPLATE_MISMATCH" {
		t.Fatal("wrong headers accepted")
	}
	if _, _, err := Apply([]byte("not a workbook"), r); Code(err) != "EXCEL_INVALID_WORKBOOK" {
		t.Fatal("invalid ZIP accepted")
	}
	if _, _, err := Apply(make([]byte, MaxFileSize+1), r); Code(err) != "EXCEL_FILE_TOO_LARGE" {
		t.Fatal("size limit ignored")
	}
}

func TestServerCredentialRejectsForeignTokenEndpoint(t *testing.T) {
	file := t.TempDir() + "/service-account.json"
	if err := os.WriteFile(file, []byte(`{"type":"service_account","token_uri":"https://example.invalid/token","client_email":"app@club.iam.gserviceaccount.com","private_key":"synthetic"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := NewDriveClient(context.Background(), file, "synthetic-file-id")
	if Code(err) != "EXCEL_CONFIG_INVALID" {
		t.Fatal("arbitrary token endpoint accepted")
	}
	for _, attempt := range []int{0, 1, 6, 10000} {
		if delay := RetryDelay(&Failure{Temporary: true}, attempt); delay <= 0 || delay > 5*60*1e9 {
			t.Fatal("unbounded backoff")
		}
	}
	if Code(fmt.Errorf("wrapped: %w", &Failure{Code: "SAFE"})) != "SAFE" || Code(errors.New("private provider payload")) != "EXCEL_SYNC_UNAVAILABLE" {
		t.Fatal("unsafe error projection")
	}
}

// Optional local check: the private workbook is never a repository fixture.
func TestProvidedTemplatePreservation(t *testing.T) {
	path := os.Getenv("PC_CLUB_EXCEL_TEMPLATE_TEST")
	if path == "" {
		t.Skip("private template not configured")
	}
	input, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before := openWorkbook(t, input)
	output, changed, err := Apply(input, snapshot())
	if err != nil || !changed {
		t.Fatalf("projection: changed=%v err=%v", changed, err)
	}
	after := openWorkbook(t, output)
	for sheetIndex, sheet := range before.GetSheetList() {
		rows, err := before.GetRows(sheet, excelize.Options{RawCellValue: true})
		if err != nil {
			t.Fatal(err)
		}
		for row, values := range rows {
			if sheet == Sheet && value(t, after, sheet, cell(37, row+1)) == snapshot().ID {
				continue
			}
			for col := range values {
				if sheet == Sheet && col >= 36 {
					continue
				}
				address := cell(col+1, row+1)
				oldFormula, err := before.GetCellFormula(sheet, address)
				if err != nil {
					t.Fatal(err)
				}
				newFormula, err := after.GetCellFormula(sheet, address)
				if err != nil {
					t.Fatal(err)
				}
				if oldFormula != newFormula {
					t.Fatal("existing formula changed")
				}
				if oldFormula == "" && value(t, before, sheet, address) != value(t, after, sheet, address) {
					t.Fatalf("existing value changed: sheet index=%d cell=%s", sheetIndex, address)
				}
			}
		}
	}
	onDisk, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(input, onDisk) {
		t.Fatal("source file changed")
	}
}

func TestWorkbookRejectsDuplicateArchiveEntries(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for range 2 {
		entry, err := writer.Create("xl/workbook.xml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("<workbook/>")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := checkArchive(buffer.Bytes()); Code(err) != "EXCEL_INVALID_WORKBOOK" {
		t.Fatalf("unexpected result: %v", err)
	}
}

func TestArchiveLimitsImplicitCoordinates(t *testing.T) {
	for _, tc := range []struct{ name, grid string }{
		{"implicit rows", strings.Repeat("<row><c><v>1</v></c></row>", maxRows+1)},
		{"implicit columns", "<row>" + strings.Repeat("<c><v>1</v></c>", 101) + "</row>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			for name, content := range map[string]string{
				"xl/workbook.xml":            `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="` + Sheet + `" r:id="rId1"/></sheets></workbook>`,
				"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
				"xl/worksheets/sheet1.xml":   "<worksheet><sheetData>" + tc.grid + "</sheetData></worksheet>",
			} {
				entry, err := writer.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write([]byte(content)); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			err := checkArchive(buffer.Bytes())
			if err == nil || Code(err) != "EXCEL_ROW_LIMIT" {
				t.Errorf("checkArchive(%s) = %v, want EXCEL_ROW_LIMIT", tc.name, err)
			}
		})
	}
}
