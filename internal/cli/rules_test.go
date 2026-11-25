package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

func TestMemberAddKeepsRoleForPeopleAndRightsForAgents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"rights on a user", []string{"member", "add", "user:5", "--rights", "work"}, "--rights is for agents; a person holds --role"},
		{"rights on a bare id", []string{"project", "member", "add", "5", "--rights", "work"}, "--rights is for agents; a person holds --role"},
		{"role on an agent", []string{"member", "add", "agent:5", "--role", "member"}, "--role is for people; an agent holds --rights (work, ready, triage, answer, decide)"},
		{"role on a project agent", []string{"project", "member", "add", "agent:5", "--role", "viewer"}, "--role is for people; an agent holds --rights (work, ready, triage, answer, decide)"},
		{"unknown right", []string{"member", "add", "agent:5", "--rights", "root"}, `--rights "root": want one of work, ready, triage, answer, decide`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, nil)
			err := h.run(t, append([]string{"-p", "web"}, tt.args...)...)
			if cmdutil.ExitCode(err) != cmdutil.ExitUsage || err.Error() != tt.want {
				t.Fatalf("err = %v, want usage error %q", err, tt.want)
			}
		})
	}
}

func TestRequiredRolesRejectEmptyAndUnknown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		allowed string
	}{
		{"member add", []string{"member", "add", "5", "--role"}, "viewer, member, reviewer, admin, owner"},
		{"member role", []string{"member", "role", "5"}, "viewer, member, reviewer, admin, owner"},
		{"project member add", []string{"project", "member", "add", "5", "--role"}, "viewer, member, reviewer"},
	}
	for _, tt := range tests {
		for _, value := range []string{"", "unknown"} {
			t.Run(tt.name+"/"+value, func(t *testing.T) {
				t.Parallel()
				h := newHarness(t, nil)
				err := h.run(t, append(tt.args, value)...)
				want := fmt.Sprintf("--role %q: want one of %s", value, tt.allowed)
				if cmdutil.ExitCode(err) != cmdutil.ExitUsage || err.Error() != want {
					t.Fatalf("err = %v, want usage error %q", err, want)
				}
				if h.out.String() != "" {
					t.Errorf("stdout = %q, want empty", h.out.String())
				}
			})
		}
	}
}

func TestLockListsHandleMissingEndpointAndOtherErrors(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"status", "locks"} {
		for _, status := range []int{200, 404, 403, 500} {
			for _, asJSON := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/json=%t", command, status, asJSON), func(t *testing.T) {
					t.Parallel()
					routes := map[string]http.HandlerFunc{
						"GET /api/v1/projects/demo":       reply(200, apitypes.Project{Slug: "demo"}),
						"GET /api/v1/projects/demo/cards": reply(200, apitypes.CardList{}),
						"GET /api/v1/locks":               reply(status, nil),
					}
					if status == 200 {
						routes["GET /api/v1/locks"] = reply(200, apitypes.LockList{})
					}
					h := newHarness(t, serve(t, routes))
					args := []string{"-p", "demo", command}
					if asJSON {
						args = append(args, "--json")
					}
					err := h.run(t, args...)
					if status == 403 || status == 500 {
						if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("api %d", status)) {
							t.Fatalf("err = %v, want API %d", err, status)
						}
						if h.out.String() != "" {
							t.Errorf("stdout = %q, want empty on error", h.out.String())
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					switch {
					case asJSON:
						var view map[string]json.RawMessage
						if err := json.Unmarshal(h.out.Bytes(), &view); err != nil {
							t.Fatal(err)
						}
						field := "items"
						if command == "status" {
							field = "locks"
						}
						if string(view[field]) != "[]" {
							t.Errorf("%s = %s, want []", field, view[field])
						}
					case command == "locks" && h.out.String() != "no locks\n":
						t.Errorf("stdout = %q, want no locks", h.out.String())
					case command == "status" && strings.Contains(h.out.String(), "locks:"):
						t.Errorf("stdout = %q, want no locks section", h.out.String())
					}
				})
			}
		}
	}
}

func TestBodilessCommandsPrintAResult(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		args  []string
		human string
		json  string
	}{
		{"agent revoke", []string{"agent", "revoke", "5"}, "revoked agent 5\n", `{"id":"5","revoked":true}`},
		{"token revoke", []string{"token", "revoke", "5"}, "revoked token 5\n", `{"id":"5","revoked":true}`},
		{"agent remove", []string{"agent", "remove", "5"}, "removed agent 5\n", `{"kind":"agent","id":"5","removed":true}`},
		{"agent unbind", []string{"agent", "unbind", "5"}, "agent 5 removed from demo\n", `{"kind":"agent","id":"5","project":"demo","removed":true}`},
		{"member remove", []string{"member", "remove", "5"}, "removed user 5\n", `{"kind":"user","id":"5","removed":true}`},
		{"project member remove", []string{"project", "member", "remove", "5"}, "removed user 5 from demo\n", `{"kind":"user","id":"5","project":"demo","removed":true}`},
		{"unlink", []string{"unlink", "5", "applies", "6"}, "unlinked #5 applies #6\n", `{"number":5,"kind":"applies","to":6,"unlinked":true}`},
		{"decision unlink", []string{"decisions", "unlink", "5", "6"}, "unlinked #5 applies #6\n", `{"number":5,"kind":"applies","to":6,"unlinked":true}`},
	}
	for _, tt := range tests {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tt.name, asJSON), func(t *testing.T) {
				t.Parallel()
				h := newHarness(t, serve(t, map[string]http.HandlerFunc{"DELETE /": reply(204, nil), "POST /": reply(204, nil)}))
				args := append([]string{"-p", "demo"}, tt.args...)
				if asJSON {
					args = append(args, "--json")
				}
				if err := h.run(t, args...); err != nil {
					t.Fatal(err)
				}
				if h.errOut.String() != "" {
					t.Errorf("stderr = %q, want empty", h.errOut.String())
				}
				if !asJSON {
					if h.out.String() != tt.human {
						t.Errorf("stdout = %q, want %q", h.out.String(), tt.human)
					}
					return
				}
				var got, want any
				if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
					t.Fatalf("stdout %q is not JSON: %v", h.out.String(), err)
				}
				if err := json.Unmarshal([]byte(tt.json), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("stdout = %s, want %s", h.out.String(), tt.json)
				}
			})
		}
	}
}

func TestHistoryPageAsksOneMoreToKnowIfOlderRowsExist(t *testing.T) {
	t.Parallel()
	rows := func(n int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = n - i
		}
		return out
	}
	tests := []struct {
		name     string
		limit    int
		served   int
		wantAsk  int
		wantRows int
		wantNext string
	}{
		{"short page is the last", 10, 3, 11, 3, ""},
		{"exactly limit is the last", 3, 3, 4, 3, ""},
		{"one extra row means older ones", 2, 3, 3, 2, "2"},
		{"a full page at the cap may have more", 500, 200, 200, 200, "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var asked client.HistoryParams
			got, next, err := historyPage(tt.limit, "77", strconv.Itoa,
				func(p client.HistoryParams) ([]int, error) { asked = p; return rows(tt.served), nil })
			if err != nil {
				t.Fatal(err)
			}
			if asked.Limit != tt.wantAsk || asked.Before != "77" {
				t.Errorf("asked %+v, want limit %d before 77", asked, tt.wantAsk)
			}
			if len(got) != tt.wantRows || next != tt.wantNext {
				t.Errorf("rows %d next %q, want %d %q", len(got), next, tt.wantRows, tt.wantNext)
			}
		})
	}
	if _, _, err := historyPage(0, "", func(int) string { return "" }, func(client.HistoryParams) ([]int, error) { return nil, nil }); cmdutil.ExitCode(err) != cmdutil.ExitUsage {
		t.Errorf("limit 0: err = %v, want a usage error", err)
	}
}
