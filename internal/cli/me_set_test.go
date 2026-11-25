package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func TestMeSetSendsOnlyGivenFlagsAndEmptyClears(t *testing.T) {
	t.Parallel()
	var got apitypes.UpdateUser
	srv := serve(t, map[string]http.HandlerFunc{
		"PATCH /api/v1/me": func(w http.ResponseWriter, r *http.Request) {
			got = readJSON[apitypes.UpdateUser](t, r)
			reply(200, apitypes.User{ID: "1", Login: "ana", Timezone: "Asia/Kolkata"})(w, r)
		},
	})
	h := newHarness(t, srv)
	if err := h.run(t, "me", "set", "--timezone", "Asia/Kolkata", "--locale", ""); err != nil {
		t.Fatal(err)
	}
	if got.Name != nil {
		t.Errorf("name = %q, want unset", *got.Name)
	}
	if got.Timezone == nil || *got.Timezone != "Asia/Kolkata" {
		t.Errorf("timezone = %v, want Asia/Kolkata", got.Timezone)
	}
	if got.Locale == nil || *got.Locale != "" {
		t.Errorf("locale = %v, want pointer to empty string to clear", got.Locale)
	}
}

func TestMeSetValidatesBeforeSending(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"PATCH /api/v1/me": func(http.ResponseWriter, *http.Request) {
			t.Error("request sent despite invalid input")
		},
	})
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"bad zone", []string{"me", "set", "--timezone", "Mars/Base"}, "IANA"},
		{"no flags", []string{"me", "set"}, "nothing to change"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := newHarness(t, srv).run(t, tt.args...)
			if cmdutil.ExitCode(err) != 2 || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v (exit %d), want usage error containing %q", err, cmdutil.ExitCode(err), tt.want)
			}
		})
	}
}
