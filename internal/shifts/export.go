package shifts

import (
	"bytes"
	"context"
	"encoding/csv"
	"strings"
	"unicode"

	"pc-club-admin/internal/auth"
)

const ExportLimit = 5000

type ExportFilter struct{ DateFrom, DateTo, OwnerID string }

// CSVText prevents formulas even when their first character follows whitespace.
func CSVText(value string) string {
	trimmed := strings.TrimLeftFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") || strings.HasPrefix(value, "\n") {
		return "'" + value
	}
	return value
}

// CSVAmount formats integer kopecks without a floating-point conversion. SQL
// numeric aggregates can exceed int64; decimal string formatting preserves them.
func CSVAmount(kopecks string) string {
	if kopecks == "" {
		return ""
	}
	sign := ""
	if strings.HasPrefix(kopecks, "-") {
		sign = "-"
		kopecks = kopecks[1:]
	}
	for len(kopecks) < 3 {
		kopecks = "0" + kopecks
	}
	return sign + kopecks[:len(kopecks)-2] + "." + kopecks[len(kopecks)-2:]
}

func (s *Service) ExportCSV(ctx context.Context, r auth.Request, f ExportFilter, cash bool) ([]byte, error) {
	if !validDate(f.DateFrom) || !validDate(f.DateTo) || f.DateFrom > f.DateTo || f.OwnerID != "" && !auth.ValidID(f.OwnerID) {
		return nil, auth.Fail("INVALID_INPUT", 400)
	}
	var result []byte
	err := s.within(ctx, r, false, func(tx Tx, id auth.Identity) error {
		if !id.User.Has("manager") {
			return auth.Fail("FORBIDDEN", 403)
		}
		rows, err := tx.ExportRows(f, cash)
		if err != nil {
			return err
		}
		if len(rows) > ExportLimit {
			return auth.Fail("EXPORT_TOO_LARGE", 422)
		}
		var buffer bytes.Buffer
		buffer.WriteString("\xef\xbb\xbf")
		writer := csv.NewWriter(&buffer)
		writer.Comma = ';'
		writer.UseCRLF = true
		header := []string{"Смена (ID)", "Дата начала смены", "Тип смены", "Статус", "Ответственный (ID)", "Ответственный"}
		if cash {
			header = append(header, "Начальный остаток, руб.", "Пересчёт при приёме, руб.", "Операции LANGAME, руб.", "Дополнительные возвраты, руб.", "Дополнительные расходы, руб.", "Пересчёт при сдаче, руб.", "Инкассации минус отмены, руб.", "Версия денежной политики")
		}
		if !cash {
			header = append(header, "Плановое начало", "Плановое окончание", "Фактическое начало", "Фактическое окончание")
		}
		header = append(header, "Статус денежной политики")
		if err := writer.Write(header); err != nil {
			return err
		}
		for _, row := range rows {
			for i := range row {
				if cash && i >= 6 && i <= 12 {
					row[i] = CSVAmount(row[i])
				} else {
					row[i] = CSVText(row[i])
				}
			}
			row = append(row, "unverified_test_only")
			if err := writer.Write(row); err != nil {
				return err
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return err
		}
		result = buffer.Bytes()
		return event(tx, r, id, "csv_exported", "", true, "", map[string]any{"cash": cash, "date_from": f.DateFrom, "date_to": f.DateTo, "owner_id": f.OwnerID, "row_count": len(rows)})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
