// Package cash isolates draft-v1 test policy; no LANGAME API assumptions are made.
package cash

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

const PolicyVersion = "draft-v1"

var ErrMoney = errors.New("invalid money amount")

// ParseRubles is exact; blank, exponent notation and >2 decimals are rejected.
func ParseRubles(text string) (int64, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, ErrMoney
	}
	negative := false
	switch text[0] {
	case '-':
		negative = true
		text = text[1:]
	case '+':
		return 0, ErrMoney
	}
	text = strings.ReplaceAll(text, ",", ".")
	parts := strings.Split(text, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, ErrMoney
	}
	for _, part := range parts {
		if part == "" {
			return 0, ErrMoney
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, ErrMoney
			}
		}
	}
	decimals := "00"
	if len(parts) == 2 {
		if len(parts[1]) > 2 {
			return 0, ErrMoney
		}
		decimals = parts[1]
		if len(decimals) == 1 {
			decimals += "0"
		}
	}
	digits := strings.TrimLeft(parts[0]+decimals, "0")
	if digits == "" {
		digits = "0"
	}
	if negative {
		digits = "-" + digits
	}
	value, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, ErrMoney
	}
	return value, nil
}
func Add(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b || b < 0 && a < math.MinInt64-b {
		return 0, ErrMoney
	}
	return a + b, nil
}
func Sub(a, b int64) (int64, error) {
	if b > 0 && a < math.MinInt64+b || b < 0 && a > math.MaxInt64+b {
		return 0, ErrMoney
	}
	return a - b, nil
}

type Movement struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Amount     int64     `json:"amount_kopecks"`
	ReversalOf *string   `json:"reversal_of"`
	Reason     string    `json:"reason"`
	Recipient  string    `json:"recipient"`
	OccurredAt time.Time `json:"occurred_at"`
	CreatedAt  time.Time `json:"created_at"`
	ActorID    string    `json:"actor_id"`
}
type Session struct {
	ID                 string     `json:"id"`
	ShiftID            string     `json:"shift_id"`
	Version            int64      `json:"version"`
	Opening            int64      `json:"opening_cash_kopecks"`
	CountedOpening     *int64     `json:"counted_opening_kopecks"`
	OpeningConfirmedBy *string    `json:"opening_confirmed_by"`
	OpeningConfirmedAt *time.Time `json:"opening_confirmed_at"`
	Langame            *int64     `json:"langame_cash_amount_kopecks"`
	Refunds            *int64     `json:"extra_cash_refunds_kopecks"`
	Expenses           *int64     `json:"extra_cash_expenses_kopecks"`
	Counted            *int64     `json:"counted_closing_kopecks"`
	Policy             string     `json:"policy_version"`
	Movements          []Movement `json:"movements"`
}
type Blocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Summary struct {
	Expected        *int64    `json:"expected_closing_kopecks"`
	Difference      *int64    `json:"difference_kopecks"`
	CollectionTotal *int64    `json:"collection_total_kopecks"`
	Policy          string    `json:"policy_version"`
	Verified        bool      `json:"verified"`
	Blockers        []Blocker `json:"blockers"`
}
type View struct {
	Session Session `json:"session"`
	Summary Summary `json:"summary"`
}

// CollectionTotal sums uncancelled collections, never subtracting previous shifts.
func CollectionTotal(movements []Movement) (int64, error) {
	reversed := map[string]bool{}
	for _, m := range movements {
		if m.Kind == "reversal" {
			if m.ReversalOf == nil || reversed[*m.ReversalOf] {
				return 0, ErrMoney
			}
			reversed[*m.ReversalOf] = true
		}
	}
	total := int64(0)
	found := map[string]bool{}
	for _, m := range movements {
		if m.Amount < 0 {
			return 0, ErrMoney
		}
		if m.Kind == "collection" {
			found[m.ID] = true
			if !reversed[m.ID] {
				var err error
				total, err = Add(total, m.Amount)
				if err != nil {
					return 0, err
				}
			}
		} else if m.Kind != "reversal" {
			return 0, ErrMoney
		}
	}
	for id := range reversed {
		if !found[id] {
			return 0, ErrMoney
		}
	}
	return total, nil
}
func Evaluate(s Session) Summary {
	result := Summary{Policy: s.Policy, Blockers: []Blocker{}}
	block := func(code, message string) { result.Blockers = append(result.Blockers, Blocker{code, message}) }
	if s.Policy != PolicyVersion {
		block("CASH_POLICY_UNVERIFIED", "Неизвестная версия тестовой денежной политики")
		return result
	}
	total, err := CollectionTotal(s.Movements)
	if err != nil {
		block("CASH_OVERFLOW", "Недопустимая сумма или переполнение журнала инкассаций")
		return result
	}
	result.CollectionTotal = &total
	if s.Langame == nil || s.Refunds == nil || s.Expenses == nil || s.Counted == nil {
		block("CASH_INPUT_MISSING", "Заполните отчёт, вычеты и пересчёт; пустое поле не равно нулю")
	}
	if s.Opening < 0 || s.Refunds != nil && *s.Refunds < 0 || s.Expenses != nil && *s.Expenses < 0 || s.Counted != nil && *s.Counted < 0 {
		block("CASH_INVALID_AMOUNT", "Пересчёт и дополнительные вычеты не могут быть отрицательными")
		return result
	}
	if s.Langame == nil || s.Refunds == nil || s.Expenses == nil {
		return result
	}
	expected, err := Add(s.Opening, *s.Langame)
	if err == nil {
		expected, err = Sub(expected, *s.Refunds)
	}
	if err == nil {
		expected, err = Sub(expected, *s.Expenses)
	}
	if err == nil {
		expected, err = Sub(expected, total)
	}
	if err != nil {
		block("CASH_OVERFLOW", "Переполнение int64 при расчёте кассы")
		return result
	}
	result.Expected = &expected
	if expected < 0 {
		block("CASH_NEGATIVE_EXPECTED", "Ожидаемый остаток отрицателен: исправьте исходные данные")
		return result
	}
	if s.Counted != nil {
		difference, err := Sub(*s.Counted, expected)
		if err != nil {
			block("CASH_OVERFLOW", "Переполнение разницы кассы")
			return result
		}
		result.Difference = &difference
		if difference != 0 {
			block("CASH_MISMATCH", "Касса не совпадает. Передача запрещена даже при расхождении в 1 копейку")
		}
	}
	return result
}
