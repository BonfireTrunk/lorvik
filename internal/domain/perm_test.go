package domain

import (
	"slices"
	"strings"
	"testing"
)

// TestPermissionTable pins every cell of the authorisation table from the
// schema design. A change here is a product decision.
func TestPermissionTable(t *testing.T) {
	t.Parallel()
	all := []Role{RoleViewer, RoleMember, RoleReviewer, RoleAdmin, RoleOwner}
	first := map[Permission]Role{
		PermRead:            RoleViewer,
		PermCardWrite:       RoleMember,
		PermQuestionAsk:     RoleMember,
		PermDecisionPropose: RoleMember,
		PermNoteWrite:       RoleMember,
		PermCommentWrite:    RoleMember,
		PermAgentRegister:   RoleMember,
		PermCardTriage:      RoleReviewer,
		PermQuestionAnswer:  RoleReviewer,
		PermDecisionAccept:  RoleReviewer,
		PermAgentManage:     RoleAdmin,
		PermProjectManage:   RoleAdmin,
		PermMemberManage:    RoleAdmin,
		PermOrgManage:       RoleOwner,
		PermCardReady:       RoleReviewer,
	}
	if len(first) != len(minRole) {
		t.Fatalf("test covers %d permissions, table has %d", len(first), len(minRole))
	}
	for perm, from := range first {
		for _, r := range all {
			if got, want := r.Can(perm), r >= from; got != want {
				t.Errorf("%s.Can(%d) = %v, want %v", r, perm, got, want)
			}
		}
	}
	if Role(0).Can(PermRead) || RoleOwner.Can(Permission(99)) {
		t.Error("no role and unknown permission must both be refused")
	}
}

func TestCanGrant(t *testing.T) {
	t.Parallel()
	tests := []struct {
		by, target Role
		want       bool
	}{
		{RoleOwner, RoleOwner, true},
		{RoleOwner, RoleAdmin, true},
		{RoleAdmin, RoleReviewer, true},
		{RoleAdmin, RoleAdmin, false},
		{RoleReviewer, RoleViewer, false},
		{RoleAdmin, Role(99), false},
	}
	for _, tt := range tests {
		if got := tt.by.CanGrant(tt.target); got != tt.want {
			t.Errorf("%s.CanGrant(%s) = %v, want %v", tt.by, tt.target, got, tt.want)
		}
	}
}

// Only reading is safe for a read-only token; a permission added later is
// mutating unless it is PermRead, so forgetting to classify it fails closed.
func TestOnlyReadIsNotMutating(t *testing.T) {
	t.Parallel()
	for p := PermRead; p <= PermCardReady; p++ {
		if got, want := p.Mutating(), p != PermRead; got != want {
			t.Errorf("Permission(%d).Mutating() = %v, want %v", p, got, want)
		}
	}
	if Permission(99).Mutating() != true {
		t.Error("an unknown permission must count as mutating")
	}
}

func TestOnlyAdminsAndOwnersSeeEveryProject(t *testing.T) {
	t.Parallel()
	for _, r := range []Role{RoleViewer, RoleMember, RoleReviewer, RoleAdmin, RoleOwner} {
		if got, want := r.SeesEveryProject(), r == RoleAdmin || r == RoleOwner; got != want {
			t.Errorf("%s.SeesEveryProject() = %v, want %v", r, got, want)
		}
	}
}

func TestParseRightsAndNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      []string
		want    Rights
		names   []string
		wantErr string
	}{
		{in: nil, want: 0, names: []string{}},
		{in: []string{"answer", "work", "work"}, want: RightWork | RightAnswer, names: []string{"work", "answer"}},
		{in: []string{"work", "ready", "triage", "answer", "decide"}, want: AllRights, names: []string{"work", "ready", "triage", "answer", "decide"}},
		{in: []string{"decide"}, want: 16, names: []string{"decide"}},
		{in: []string{"work", "admin"}, wantErr: "admin"},
	}
	for _, tt := range tests {
		got, err := ParseRights(tt.in)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ParseRights(%v) err = %v, want one naming %q", tt.in, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("ParseRights(%v) = %d, %v; want %d", tt.in, got, err, tt.want)
		}
		if names := got.Names(); names == nil || !slices.Equal(names, tt.names) {
			t.Errorf("Names(%d) = %#v, want %#v", got, names, tt.names)
		}
	}
	if Rights(32).Valid() || !AllRights.Valid() || !Rights(0).Valid() {
		t.Error("Valid must reject bits outside AllRights only")
	}
	if !AllRights.Has(RightWork|RightAnswer) || RightWork.Has(RightWork|RightReady) {
		t.Error("Has must require every bit of its argument")
	}
}

// Pins which verbs each right opens; agents never get registration, the
// decision-accept permission or the admin verbs, whatever rights they hold.
// The decide right opens no permission: a verdict checks it by name.
func TestRightsGrants(t *testing.T) {
	t.Parallel()
	want := map[Permission]Rights{
		PermCardWrite:       RightWork,
		PermQuestionAsk:     RightWork,
		PermDecisionPropose: RightWork,
		PermNoteWrite:       RightWork,
		PermCommentWrite:    RightWork,
		PermCardReady:       RightReady,
		PermCardTriage:      RightTriage,
		PermQuestionAnswer:  RightAnswer,
		PermRead:            0,
		PermAgentRegister:   0,
		PermDecisionAccept:  0,
		PermAgentManage:     0,
		PermProjectManage:   0,
		PermMemberManage:    0,
		PermOrgManage:       0,
	}
	if len(want) != len(minRole) {
		t.Fatalf("test covers %d permissions, table has %d", len(want), len(minRole))
	}
	for perm, need := range want {
		for _, r := range []Rights{RightWork, RightReady, RightTriage, RightAnswer, RightDecide, AllRights, 0} {
			if got, exp := r.Grants(perm), need != 0 && r.Has(need); got != exp {
				t.Errorf("Rights(%d).Grants(%s) = %v, want %v", r, perm, got, exp)
			}
		}
	}
}

func TestRightsHeldBy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		role Role
		want Rights
	}{
		{RoleViewer, 0},
		{RoleMember, RightWork},
		{RoleReviewer, AllRights},
		{RoleAdmin, AllRights},
		{RoleOwner, AllRights},
		{Role(0), 0},
	}
	for _, tt := range tests {
		if got := RightsHeldBy(tt.role); got != tt.want {
			t.Errorf("RightsHeldBy(%s) = %d, want %d", tt.role, got, tt.want)
		}
	}
}

// RightFor names the one right a permission needs; permissions no right
// grants name none, so an agent can never hold them.
func TestRightFor(t *testing.T) {
	t.Parallel()
	for p := PermRead; p <= PermCardReady; p++ {
		r := RightFor(p)
		if r != 0 && r&(r-1) != 0 {
			t.Errorf("RightFor(%s) = %d, want a single right", p, r)
		}
		if got := AllRights.Grants(p); got != (r != 0) {
			t.Errorf("AllRights.Grants(%s) = %v, want %v", p, got, r != 0)
		}
	}
}
