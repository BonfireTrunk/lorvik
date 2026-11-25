package server

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// DeleteMe deletes the calling person's account for good (#417 A): their
// place in every org ends, the agents they created are revoked, their ways
// to sign in and their tokens go, and the user row keeps only the login and
// name that past work shows. An org whose only person they are closes with
// them (#422 A); one where they are the only owner beside other people
// refuses, so no org is left without one. A sign-in token is taken too: a person a GitHub
// sign-in created by mistake deletes themself from the Create-an-org screen
// and so frees the GitHub account for the account they meant (#348).
func (s *Service) DeleteMe(ctx context.Context, in apitypes.DeleteMe) error {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return fmt.Errorf("%w: no credentials on request", domain.ErrUnauthorized)
	}
	if err := requireHuman(p); err != nil {
		return err
	}
	if err := p.requireWritable(); err != nil {
		return err
	}
	var closed []domain.ID
	err := s.store.WithTx(ctx, func(tx Store) error {
		closed = nil
		if p.Scope == domain.ScopeSignIn {
			if _, err := s.liveSignIn(ctx, tx, p, s.now()); err != nil {
				return err
			}
		}
		// The user row is locked first, as GitHub sign-in and link lock it, so
		// neither writes it back or hangs an identity on it mid-delete.
		u, err := tx.LockUser(ctx, p.Actor.ID)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if in.Confirm != u.Login {
			return withHint(invalidField("confirm", "does not match your login"), "type your login exactly to confirm")
		}
		ms, err := tx.ListOpenMembershipsForActor(ctx, u.Ref())
		if err != nil {
			return fmt.Errorf("list memberships: %w", err)
		}
		// Org order, so two people deleting at once lock owners in the same order.
		slices.SortFunc(ms, func(a, b domain.Membership) int { return cmp.Compare(a.OrgID, b.OrgID) })
		for _, m := range ms {
			c, err := s.leaveOrg(ctx, tx, p, u, m.OrgID)
			if err != nil {
				return err
			}
			if c {
				closed = append(closed, m.OrgID)
			}
		}
		ids, err := tx.ListIdentities(ctx, u.ID)
		if err != nil {
			return fmt.Errorf("load identities: %w", err)
		}
		for _, id := range ids {
			if err := tx.DeleteIdentity(ctx, u.ID, id.ID); err != nil {
				return fmt.Errorf("delete identity: %w", err)
			}
		}
		// Org tokens went with each membership; this takes the sign-in tokens.
		if err := tx.RevokeTokensForActor(ctx, 0, u.Ref()); err != nil {
			return fmt.Errorf("revoke sign-in tokens: %w", err)
		}
		u.Status, u.Email, u.AvatarURL, u.Locale, u.Timezone, u.UpdatedAt = domain.RecordDeleted, "", "", "", "", s.now()
		if _, err := tx.UpdateUser(ctx, u); err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "account deleted", "user_id", p.Actor.ID, "request_id", RequestIDFrom(ctx))
	for _, id := range closed {
		s.logger.InfoContext(ctx, "org closed", "org_id", id, "user_id", p.Actor.ID, "request_id", RequestIDFrom(ctx))
	}
	return nil
}

// DeletionPreview answers what DELETE /me would do now: the orgs it would
// close and the ones that refuse it.
func (s *Service) DeletionPreview(ctx context.Context) (apitypes.DeletionPreview, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.DeletionPreview{}, err
	}
	out := apitypes.DeletionPreview{Closes: []apitypes.ClosingOrg{}, BlockedBy: []string{}}
	err = s.store.WithTx(ctx, func(tx Store) error {
		ms, err := tx.ListMembershipsForActor(ctx, p.Actor)
		if err != nil {
			return fmt.Errorf("list memberships: %w", err)
		}
		slices.SortFunc(ms, func(a, b domain.Membership) int { return cmp.Compare(a.OrgID, b.OrgID) })
		for _, m := range ms {
			owners, err := tx.LockOrgOwners(ctx, m.OrgID)
			if err != nil {
				return err
			}
			exit, err := orgExitOf(ctx, tx, m, owners)
			if err != nil {
				return err
			}
			if exit == exitLeave {
				continue
			}
			org, err := tx.GetOrg(ctx, m.OrgID)
			if err != nil {
				return fmt.Errorf("load org: %w", err)
			}
			if exit == exitBlocked {
				out.BlockedBy = append(out.BlockedBy, org.Name)
				continue
			}
			projects, cards, err := tx.CountOrgContents(ctx, m.OrgID)
			if err != nil {
				return err
			}
			out.Closes = append(out.Closes, apitypes.ClosingOrg{Name: org.Name, Projects: projects, Cards: cards})
		}
		return nil
	})
	return out, err
}

// orgExit is what deleting an account does to one org it has a place in.
type orgExit int

const (
	// exitLeave: the person leaves and the org goes on.
	exitLeave orgExit = iota
	// exitClose: the person is the org's only one, so it closes with them.
	exitClose
	// exitBlocked: the only owner beside other people; the delete is refused.
	exitBlocked
)

// orgExitOf decides the exit for membership m, given the org's locked
// active owners. A pending membership is simply withdrawn.
func orgExitOf(ctx context.Context, st Store, m domain.Membership, owners []domain.Membership) (orgExit, error) {
	if !m.Status.Alive() {
		return exitLeave, nil
	}
	people, err := st.CountOrgPeople(ctx, m.OrgID)
	if err != nil {
		return 0, err
	}
	switch {
	case people <= 1:
		return exitClose, nil
	case m.Role == domain.RoleOwner && lastOwner(owners, m.Actor):
		return exitBlocked, nil
	}
	return exitLeave, nil
}

// leaveOrg ends u's place in one org as part of deleting the account: the
// membership, alive or pending, and the agents u created there. An org
// whose only person u is closes, with every agent in it; the only owner
// beside other people is refused. closed reports a closed org.
func (s *Service) leaveOrg(ctx context.Context, tx Store, p Principal, u domain.User, orgID domain.ID) (closed bool, err error) {
	owners, err := tx.LockOrgOwners(ctx, orgID)
	if err != nil {
		return false, err
	}
	m, err := tx.LockMembership(ctx, orgID, u.Ref())
	if err != nil {
		return false, fmt.Errorf("membership: %w", err)
	}
	alive := m.Status.Alive()
	if !alive && m.Status != domain.RecordPending {
		return false, nil
	}
	exit, err := orgExitOf(ctx, tx, m, owners)
	if err != nil {
		return false, err
	}
	org, err := tx.GetOrg(ctx, orgID)
	if err != nil {
		return false, fmt.Errorf("load org: %w", err)
	}
	if exit == exitBlocked {
		return false, newProblem(http.StatusConflict, fmt.Sprintf("you are the only owner of %s", org.Name),
			"make another member owner on the Access page first, then delete your account")
	}
	in := Principal{Actor: p.Actor, Session: p.Session, OrgID: orgID, Scope: p.Scope}
	if err := s.endMembership(ctx, tx, in, m); err != nil {
		return false, err
	}
	if !alive {
		// A pending invitation or request is withdrawn without an event:
		// only admins saw it, and members are not told of it now.
		return false, nil
	}
	agents, err := tx.ListAgents(ctx, orgID, false)
	if err != nil {
		return false, fmt.Errorf("list agents: %w", err)
	}
	for _, a := range agents {
		if a.CreatedBy != u.ID && exit != exitClose {
			continue
		}
		if a, err = tx.LockAgent(ctx, a.ID); err != nil {
			return false, fmt.Errorf("lock agent: %w", err)
		}
		if _, err := s.stopAgent(ctx, tx, in, a, domain.RecordRevoked); err != nil {
			return false, err
		}
		if err := s.emitP(ctx, tx, in, nil, domain.EventAgentRevoked, agentSubject(a.ID), payload(a.Name)); err != nil {
			return false, err
		}
	}
	if err := s.emitP(ctx, tx, in, nil, domain.EventUserDeleted, membershipSubject(u.ID), nil); err != nil {
		return false, err
	}
	if exit != exitClose {
		return false, nil
	}
	org.Status, org.UpdatedAt = domain.RecordDeleted, s.now()
	if _, err := tx.UpdateOrg(ctx, org); err != nil {
		return false, fmt.Errorf("close org: %w", err)
	}
	return true, nil
}
