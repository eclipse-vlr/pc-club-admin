package excelsync

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/checklists"
)

const maxRows = 20000

type column struct{ code, label, phase string }

var columns = []column{
	{"accept_d", "Написал(а) в чат, что в пути", "acceptance"},
	{"accept_e", "На месте вовремя, в форме", "acceptance"},
	{"accept_f", "Место/зал в чистоте и порядке", "acceptance"},
	{"accept_g", "Сверены наличные в кассе", "acceptance"},
	{"accept_h", "Проверен холодильник/витрины", "acceptance"},
	{"accept_i", "Проверено неисправное оборудование", "acceptance"},
	{"accept_j", "Написано в чат «принял смену»", "acceptance"},
	{"accept_k", "Смена открыта в системе (CLS)", "acceptance"},
	{"accept_l", "Товары проверены", "acceptance"},
	{"accept_m", "Эвакуационные выходы проверены", "acceptance"},
	{"accept_n", "Температура в зале настроена (23°C)", "acceptance"},
	{"accept_o", "Проверка Faceit", "acceptance"},
	{"close_p", "Отчёт по выручке заполнен и сверен", "closing"},
	{"close_q", "Наличные в конверте (имя+дата), в сейфе", "closing"},
	{"close_r", "Чековая лента проверена", "closing"},
	{"close_s", "Холодильник/витрины заполнены", "closing"},
	{"close_t", "Фото витрин отправлено в чат", "closing"},
	{"close_u", "Заканчивающиеся товары переданы", "closing"},
	{"close_v", "ПК и консоли в порядке", "closing"},
	{"close_w", "Периферия и кресла исправны", "closing"},
	{"close_x", "Неисправности устранены/зафиксированы", "closing"},
	{"close_y", "Видео ПК отправлено в чат", "closing"},
	{"close_z", "Периферия протёрта, мусор вынесен", "closing"},
	{"close_aa", "Стол/ресепшн/зал убраны", "closing"},
	{"close_ab", "Рабочее место готово для след. смены", "closing"},
	{"close_ac", "Сессии гостей завершены/переданы", "closing"},
	{"conflicts_recorded", "Конфликтные ситуации зафиксированы", "closing"},
	{"close_ae", "Отчёт и фото отправлены в чат", "closing"},
	{"close_af", "Сообщение о передаче смены отправлено", "closing"},
	{"close_ag", "Следующий админ проинформирован", "closing"},
	{"close_ah", "На телевизорах и PS отсутствует пыль", "closing"},
}

var markerHeaders = []string{"PCClubAdmin: shift_id", "PCClubAdmin: version", "Состояние PC Club Admin", "PCClubAdmin: digest"}

// Apply updates one application-owned row. Legacy rows and other worksheets are
// preserved. Equal revisions are a verified no-op, including after a lost upload
// response. Manual changes to an owned row stop delivery rather than being lost.
func Apply(input []byte, r Record) ([]byte, bool, error) {
	values, err := project(r)
	if err != nil {
		return nil, false, err
	}
	if err := checkArchive(input); err != nil {
		return nil, false, err
	}
	f, err := excelize.OpenReader(bytes.NewReader(input), excelize.Options{UnzipSizeLimit: 64 << 20, UnzipXMLSizeLimit: 8 << 20})
	if err != nil {
		return nil, false, fail("EXCEL_INVALID_WORKBOOK")
	}
	defer func() { _ = f.Close() }() // best-effort cleanup of reader-owned temporary files
	if err := checkHeaders(f); err != nil {
		return nil, false, err
	}
	rows, err := f.GetRows(Sheet, excelize.Options{RawCellValue: true})
	if err != nil || len(rows) > maxRows {
		return nil, false, fail("EXCEL_ROW_LIMIT")
	}
	get := func(row []string, i int) string {
		if i < len(row) {
			return row[i]
		}
		return ""
	}
	rowNumber, occupied := 0, 5
	markerHeader, _ := f.GetCellValue(Sheet, "AK5")
	for i, row := range rows {
		if i < 5 {
			continue
		}
		if markerHeader == "" {
			for j := 36; j < 40; j++ {
				if get(row, j) != "" {
					return nil, false, fail("EXCEL_TEMPLATE_MISMATCH")
				}
			}
		}
		if get(row, 36) == r.ID {
			if rowNumber != 0 {
				return nil, false, fail("EXCEL_DUPLICATE_SHIFT")
			}
			rowNumber = i + 1
		}
		for j, v := range row {
			// Pre-filled percentage formulas do not make a blank legacy row data.
			if j != 34 && strings.TrimSpace(v) != "" {
				occupied = i + 1
				break
			}
		}
	}
	digest := rowDigest(values)
	newRow := rowNumber == 0
	if rowNumber != 0 {
		row := rows[rowNumber-1]
		version, err := strconv.ParseInt(get(row, 37), 10, 64)
		if err != nil || version < 1 {
			return nil, false, fail("EXCEL_ROW_EDITED")
		}
		current, err := readOwned(f, rowNumber)
		if err != nil {
			return nil, false, err
		}
		if rowDigest(current) != get(row, 39) {
			return nil, false, fail("EXCEL_ROW_EDITED")
		}
		if version > r.Version {
			return input, false, nil
		}
		if version == r.Version {
			if digest != get(row, 39) {
				return nil, false, fail("EXCEL_VERSION_CONFLICT")
			}
			return input, false, nil
		}
	} else {
		rowNumber = occupied + 1
		if rowNumber > maxRows {
			return nil, false, fail("EXCEL_ROW_LIMIT")
		}
		for c := 1; c <= 36; c++ {
			style, err := f.GetCellStyle(Sheet, cell(c, 6))
			if err != nil {
				return nil, false, err
			}
			if err := f.SetCellStyle(Sheet, cell(c, rowNumber), cell(c, rowNumber), style); err != nil {
				return nil, false, err
			}
		}
		height, err := f.GetRowHeight(Sheet, 6)
		if err != nil {
			return nil, false, err
		}
		if err := f.SetRowHeight(Sheet, rowNumber, height); err != nil {
			return nil, false, err
		}
	}
	for i, v := range values[:36] {
		if i == 34 {
			percentage, err := strconv.ParseFloat(v, 64) // percentage only, never money
			if err != nil {
				return nil, false, err
			}
			if err := f.SetCellFloat(Sheet, cell(i+1, rowNumber), percentage, 6, 64); err != nil {
				return nil, false, err
			}
		} else if err := f.SetCellStr(Sheet, cell(i+1, rowNumber), v); err != nil {
			return nil, false, err
		}
	}
	for i, value := range []string{r.ID, strconv.FormatInt(r.Version, 10), values[36], digest} {
		if err := f.SetCellStr(Sheet, cell(37+i, rowNumber), value); err != nil {
			return nil, false, err
		}
		if err := f.SetCellStr(Sheet, cell(37+i, 5), markerHeaders[i]); err != nil {
			return nil, false, err
		}
	}
	if err := f.SetColVisible(Sheet, "AK:AL", false); err != nil {
		return nil, false, err
	}
	if err := f.SetColVisible(Sheet, "AN", false); err != nil {
		return nil, false, err
	}
	if err := f.SetColWidth(Sheet, "AM", "AM", 40); err != nil {
		return nil, false, err
	}
	// Newly appended rows must retain the answer dropdown too.
	if newRow {
		if err := ensureValidation(f, rowNumber); err != nil {
			return nil, false, err
		}
	}
	buffer, err := f.WriteToBuffer()
	if err != nil {
		return nil, false, fail("EXCEL_WRITE_FAILED")
	}
	if buffer.Len() > MaxFileSize {
		return nil, false, fail("EXCEL_FILE_TOO_LARGE")
	}
	return buffer.Bytes(), true, nil
}

func project(r Record) ([]string, error) {
	if !auth.ValidID(r.ID) || r.Version < 1 || len(r.Entries) != len(columns) {
		return nil, fail("EXCEL_SNAPSHOT_INVALID")
	}
	entries := map[string]checklists.Entry{}
	for _, entry := range r.Entries {
		if _, duplicate := entries[entry.Code]; duplicate {
			return nil, fail("EXCEL_SNAPSHOT_INVALID")
		}
		entries[entry.Code] = entry
	}
	status := map[string]string{"opening": "Черновик приёма", "active": "Смена принята", "handover_pending": "Ожидает приёма", "closed": "Закрыта", "cancelled": "Отменена"}[r.Status]
	kind := map[string]string{"day": "Дневная 09:00–21:00", "night": "Ночная 21:00–09:00"}[r.Kind]
	_, dateErr := time.Parse("2006-01-02", r.BusinessDate)
	if status == "" || kind == "" || dateErr != nil || strings.TrimSpace(r.OwnerName) == "" {
		return nil, fail("EXCEL_SNAPSHOT_INVALID")
	}
	values := []string{r.BusinessDate, r.OwnerName, kind}
	var comments []string
	for _, c := range columns {
		e, ok := entries[c.code]
		if !ok || e.Phase != c.phase || !e.Required || !checklists.ValidAnswer(e.Answer) {
			return nil, fail("EXCEL_SNAPSHOT_INVALID")
		}
		values = append(values, map[string]string{"yes": "Да", "no": "Нет", "unanswered": ""}[e.Answer])
		if e.Comment != "" {
			comments = append(comments, e.Label+": "+e.Comment)
		}
	}
	completed := 0
	for _, phase := range []string{"acceptance", "closing"} {
		progress := checklists.Evaluate(r.Entries, r.Notes, phase)
		completed += progress.Completed
		// A required note is part of completion, not a cosmetic warning.
		for _, b := range progress.Blockers {
			if b.Code == "NOTE_REQUIRED" {
				completed--
			}
		}
	}
	for _, n := range r.Notes {
		comments = append(comments, "["+n.Category+"] "+n.Object+": "+n.Text)
	}
	values = append(values, strconv.FormatFloat(float64(completed)/float64(len(columns)), 'f', 6, 64), strings.Join(comments, "\n"), "ТЕСТ — "+status)
	for i, value := range values {
		value = strings.ReplaceAll(value, "\r\n", "\n")
		if !utf8.ValidString(value) || len(utf16.Encode([]rune(value))) > 32767 {
			return nil, fail("EXCEL_TEXT_TOO_LONG")
		}
		for _, r := range value {
			if r < 32 && r != '\n' && r != '\r' && r != '\t' {
				return nil, fail("EXCEL_INVALID_TEXT")
			}
		}
		values[i] = value
	}
	return values, nil
}

func checkHeaders(f *excelize.File) error {
	if index, err := f.GetSheetIndex(Sheet); err != nil || index < 0 {
		return fail("EXCEL_TEMPLATE_MISMATCH")
	}
	headers := []string{"Дата", "Админ", "Смена"}
	for _, c := range columns {
		headers = append(headers, c.label)
	}
	headers = append(headers, "Выполнено, %", "Комментарий")
	for i, want := range headers {
		got, err := f.GetCellValue(Sheet, cell(i+1, 5))
		if err != nil || strings.TrimSpace(got) != want {
			return fail("EXCEL_TEMPLATE_MISMATCH")
		}
	}
	for i, want := range markerHeaders {
		got, err := f.GetCellValue(Sheet, cell(37+i, 5))
		if err != nil || got != "" && got != want {
			return fail("EXCEL_TEMPLATE_MISMATCH")
		}
	}
	return nil
}

func ensureValidation(f *excelize.File, row int) error {
	validations, err := f.GetDataValidations(Sheet)
	if err != nil {
		return err
	}
	for _, validation := range validations {
		for _, area := range strings.Fields(validation.Sqref) {
			bounds := strings.Split(area, ":")
			if len(bounds) != 2 {
				continue
			}
			left, top, e1 := excelize.CellNameToCoordinates(bounds[0])
			right, bottom, e2 := excelize.CellNameToCoordinates(bounds[1])
			if e1 == nil && e2 == nil && left <= 4 && right >= 34 && top <= row && bottom >= row {
				return nil
			}
		}
	}
	dv := excelize.NewDataValidation(true)
	dv.Sqref = fmt.Sprintf("D%d:AH%d", row, row)
	if err := dv.SetDropList([]string{"Да", "Нет"}); err != nil {
		return err
	}
	return f.AddDataValidation(Sheet, dv)
}

func readOwned(f *excelize.File, row int) ([]string, error) {
	values := []string{}
	for c := 1; c <= 39; c++ {
		if c == 37 || c == 38 {
			continue
		}
		formula, err := f.GetCellFormula(Sheet, cell(c, row))
		if err != nil || formula != "" {
			return nil, fail("EXCEL_ROW_EDITED")
		}
		value, err := f.GetCellValue(Sheet, cell(c, row), excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, err
		}
		if c == 35 {
			n, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return nil, fail("EXCEL_ROW_EDITED")
			}
			value = strconv.FormatFloat(n, 'f', 6, 64)
		}
		values = append(values, value)
	}
	return values, nil
}

func rowDigest(values []string) string {
	encoded, _ := json.Marshal(values)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
func cell(column, row int) string {
	name, _ := excelize.CoordinatesToCellName(column, row)
	return name
}

func checkArchive(input []byte) error {
	if len(input) > MaxFileSize {
		return fail("EXCEL_FILE_TOO_LARGE")
	}
	z, err := zip.NewReader(bytes.NewReader(input), int64(len(input)))
	if err != nil || len(z.File) > 2000 {
		return fail("EXCEL_INVALID_WORKBOOK")
	}
	var total uint64
	seen := make(map[string]bool, len(z.File))
	for _, f := range z.File {
		if seen[f.Name] {
			return fail("EXCEL_INVALID_WORKBOOK")
		}
		seen[f.Name] = true
		if f.UncompressedSize64 > 64<<20 || total > 64<<20-f.UncompressedSize64 {
			return fail("EXCEL_FILE_TOO_LARGE")
		}
		total += f.UncompressedSize64
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") || strings.Contains(strings.ToLower(f.Name), "vbaproject") {
			return fail("EXCEL_INVALID_WORKBOOK")
		}
	}
	// Reject sparse huge target grids before GetRows can allocate their gaps.
	read := func(name string) ([]byte, error) {
		for _, entry := range z.File {
			if entry.Name != name {
				continue
			}
			if entry.UncompressedSize64 > 8<<20 {
				return nil, fail("EXCEL_FILE_TOO_LARGE")
			}
			reader, err := entry.Open()
			if err != nil {
				return nil, err
			}
			defer func() { _ = reader.Close() }()
			return io.ReadAll(io.LimitReader(reader, 8<<20+1))
		}
		return nil, fail("EXCEL_INVALID_WORKBOOK")
	}
	data, err := read("xl/workbook.xml")
	if err != nil {
		return err
	}
	var workbook struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"id,attr"`
		} `xml:"sheets>sheet"`
	}
	if xml.Unmarshal(data, &workbook) != nil {
		return fail("EXCEL_INVALID_WORKBOOK")
	}
	var sheetID string
	for _, sheet := range workbook.Sheets {
		if sheet.Name == Sheet {
			sheetID = sheet.ID
		}
	}
	if sheetID == "" {
		return fail("EXCEL_TEMPLATE_MISMATCH")
	}
	data, err = read("xl/_rels/workbook.xml.rels")
	if err != nil {
		return err
	}
	var relationships struct {
		Items []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
			Mode   string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if xml.Unmarshal(data, &relationships) != nil {
		return fail("EXCEL_INVALID_WORKBOOK")
	}
	var sheetPath string
	for _, item := range relationships.Items {
		if item.ID == sheetID && item.Mode != "External" {
			if strings.HasPrefix(item.Target, "/") {
				sheetPath = strings.TrimPrefix(item.Target, "/")
			} else {
				sheetPath = path.Join("xl", item.Target)
			}
		}
	}
	if !strings.HasPrefix(sheetPath, "xl/worksheets/") {
		return fail("EXCEL_INVALID_WORKBOOK")
	}
	data, err = read(sheetPath)
	if err != nil {
		return err
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	rowCount, rowNumber, columnCount, columnNumber := 0, 0, 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fail("EXCEL_INVALID_WORKBOOK")
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		// OOXML permits omitted coordinates; counting only explicit r attributes
		// lets a small compressed file bypass the grid budget before GetRows.
		if start.Name.Local == "row" {
			rowCount++
			rowNumber++
			columnCount, columnNumber = 0, 0
		}
		if start.Name.Local == "c" {
			columnCount++
			columnNumber++
		}
		for _, attribute := range start.Attr {
			if attribute.Name.Local != "r" {
				continue
			}
			if start.Name.Local == "row" {
				row, err := strconv.Atoi(attribute.Value)
				rowNumber = row
				if err != nil || row < 1 || row > maxRows {
					return fail("EXCEL_ROW_LIMIT")
				}
			}
			if start.Name.Local == "c" {
				col, row, err := excelize.CellNameToCoordinates(attribute.Value)
				columnNumber = col
				if err != nil || col > 100 || row > maxRows {
					return fail("EXCEL_ROW_LIMIT")
				}
			}
		}
		if rowCount > maxRows || rowNumber > maxRows || columnCount > 100 || columnNumber > 100 {
			return fail("EXCEL_ROW_LIMIT")
		}
	}
}
