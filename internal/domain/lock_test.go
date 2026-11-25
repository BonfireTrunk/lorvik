package domain

import (
	"testing"
	"time"
)

func TestParseLockName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"plain", "db", true},
		{"slash and hyphen and digits", "web/build-2", true},
		{"forty characters", "a123456789a123456789a123456789a123456789", true},
		{"empty", "", false},
		{"forty one characters", "a123456789a123456789a123456789a1234567890", false},
		{"uppercase", "Db", false},
		{"underscore", "a_b", false},
		{"space", "a b", false},
		{"comma", "a,b", false},
	}
	for _, tt := range tests {
		got, err := ParseLockName(tt.in)
		if (err == nil) != tt.ok || (tt.ok && got != tt.in) {
			t.Errorf("%s: ParseLockName(%q) = %q, %v; ok want %v", tt.name, tt.in, got, err, tt.ok)
		}
	}
}

func TestLockTTL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		secs int
		want time.Duration
		ok   bool
	}{
		{0, DefaultLockTTL, true},
		{60, time.Minute, true},
		{3600, time.Hour, true},
		{59, 0, false},
		{3601, 0, false},
		{-5, 0, false},
	}
	for _, tt := range tests {
		got, err := LockTTL(tt.secs)
		if got != tt.want || (err == nil) != tt.ok {
			t.Errorf("LockTTL(%d) = %v, %v; want %v, ok %v", tt.secs, got, err, tt.want, tt.ok)
		}
	}
}

func TestLockExpiredAtTheBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	l := Lock{ExpiresAt: now}
	if !l.Expired(now) || l.Expired(now.Add(-time.Nanosecond)) {
		t.Errorf("a lock must be expired exactly at its expiry and live before it")
	}
}
