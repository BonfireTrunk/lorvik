package domain

import (
	"strings"
	"testing"
)

func TestCleanSessionName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{"plain", "lorvik-main2", "lorvik-main2"},
		{"trimmed and single-spaced", "  release   notes ", "release notes"},
		{"control characters become a space", "a\x00b\tc\nd\x7fe", "a b c d e"},
		{"invalid UTF-8 becomes a space", "caf\xff\xfe bar", "caf bar"},
		{"cut by characters, not bytes", strings.Repeat("日", MaxSessionName+5), strings.Repeat("日", MaxSessionName)},
		{"no space left at the cut", strings.Repeat("a", MaxSessionName-1) + " b", strings.Repeat("a", MaxSessionName-1)},
		{"nothing left", " \t\n ", ""},
		{"format characters are dropped: direction overrides, zero-width", "a\u202eb\u200bc\u2066d\ufeff", "abcd"},
		{"only zero-width characters", "\u200b\u200d\u2060", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CleanSessionName(tt.in); got != tt.want {
				t.Errorf("CleanSessionName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
