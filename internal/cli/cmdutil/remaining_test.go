package cmdutil

import "testing"

func TestRemainingText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		count int
		want  string
	}{
		{"empty", 0, "0"},
		{"below cap", 99, "99"},
		{"at cap", 100, "100+"},
		{"above cap", 101, "100+"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := RemainingText(tt.count); got != tt.want {
				t.Errorf("RemainingText(%d) = %q, want %q", tt.count, got, tt.want)
			}
		})
	}
}
