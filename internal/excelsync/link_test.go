package excelsync

import "testing"

func TestFileURL(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"synthetic-test-file", "https://drive.google.com/file/d/synthetic-test-file/view"},
		{"", ""}, {"../private", ""}, {"javascript:alert(1)", ""}, {"synthetic?token=private", ""},
	} {
		if got := FileURL(tc.id); got != tc.want {
			t.Errorf("FileURL(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}
