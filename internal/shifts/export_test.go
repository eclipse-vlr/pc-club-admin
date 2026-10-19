package shifts

import "testing"

func TestCSVFormulaProtection(t *testing.T) {
	for _, value := range []string{"=1+1", "+SUM(A1)", "-cmd", "@SUM(A1)", "  =1", "\t=1", "\r@a", "\n+1", "\u00a0=1"} {
		if got := CSVText(value); got != "'"+value {
			t.Errorf("unsafe %q: %q", value, got)
		}
	}
	for _, value := range []string{"", "Обычный текст", "first;second", "a\nb", "'safe"} {
		if got := CSVText(value); got != value {
			t.Errorf("changed safe text %q", value)
		}
	}
}
func TestCSVExactAmounts(t *testing.T) {
	for input, want := range map[string]string{"": "", "0": "0.00", "1": "0.01", "-1": "-0.01", "100": "1.00", "9007199254740993": "90071992547409.93", "-9223372036854775808": "-92233720368547758.08", "18446744073709551614": "184467440737095516.14"} {
		if got := CSVAmount(input); got != want {
			t.Errorf("%s: got %s want %s", input, got, want)
		}
	}
}
