package domain

import (
	"strings"
	"testing"
)

func TestPasswordHashVerifiesOnlyTheRightPassword(t *testing.T) {
	t.Parallel()
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	for pw, want := range map[string]bool{"correct horse battery": true, "Correct horse battery": false, "": false} {
		ok, err := VerifyPassword(h, pw)
		if err != nil || ok != want {
			t.Errorf("VerifyPassword(%q) = %v, %v; want %v", pw, ok, err, want)
		}
	}
	// A stored hash carries its parameters; these are the ones new hashes get.
	if want := "$argon2id$v=19$m=13312,t=3,p=1$"; !strings.HasPrefix(string(h), want) {
		t.Errorf("hash = %.40s..., want prefix %s", h, want)
	}
	// One made under earlier parameters still verifies: 64 MiB, 2 passes.
	const earlier = "$argon2id$v=19$m=65536,t=2,p=1$MDEyMzQ1Njc4OWFiY2RlZg$pVl2i9K1JXn27m2ddKuRO4wdyCELsLmBFheXW9sknCw"
	if ok, err := VerifyPassword([]byte(earlier), "correct horse battery"); err != nil || !ok {
		t.Errorf("VerifyPassword(earlier parameters) = %v, %v; want true", ok, err)
	}
	if _, err := HashPassword("short"); err == nil {
		t.Error("short password accepted")
	}
	if _, err := VerifyPassword([]byte("plain"), "x"); err == nil {
		t.Error("garbage hash accepted")
	}
}
