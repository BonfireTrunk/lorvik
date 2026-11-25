package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const (
	pendingHint     = "ask an org admin to grant your role on the Access page"
	webTokenName    = "dashboard"
	githubDisabled  = "GitHub login is not enabled"
	githubDisabledH = "set LORVIK_GITHUB_CLIENT_ID and LORVIK_GITHUB_CLIENT_SECRET on the server"
	lastSignInHint  = "you could not sign in again once your tokens expire; the other way is a password, on a server run with LORVIK_AUTH_PASSWORD=true"
)

func githubOff() error {
	return newProblem(http.StatusNotFound, githubDisabled, githubDisabledH)
}

// signInGitHub is the one place a GitHub profile becomes a Lorvik session:
// it finds or creates the user and identity and settles what the sign-in
// gets. The web callback and the CLI's POST /login/github differ only in how
// they answer.
func (s *Service) signInGitHub(ctx context.Context, prof githubProfile, ask signInAsk) (signInResult, error) {
	var org *domain.Org
	if ask.Org != "" {
		named, err := s.namedOrg(ctx, ask.Org, invalidField("org", "no such org"))
		if err != nil {
			return signInResult{}, err
		}
		org = &named
	}
	ask.TokenName = cmp.Or(ask.TokenName, prof.Login)
	ask.AskAs = prof.Login
	now := s.now()
	var res signInResult
	decide := func(tx Store) error {
		u, err := s.upsertGitHubUser(ctx, tx, prof, now, true)
		if err != nil {
			return err
		}
		if err := userGate(u); err != nil {
			return err
		}
		res, err = s.settle(ctx, tx, u, org, ask, now)
		return err
	}
	err := s.store.WithTx(ctx, decide)
	if identityRace(err) {
		err = s.store.WithTx(ctx, decide)
	}
	if err != nil {
		return signInResult{}, err
	}
	return res, nil
}

// identityRace reports the unique-key refusal of two transactions creating
// the same identity at once; the loser runs again and finds the winner's row.
func identityRace(err error) bool {
	ve, ok := errors.AsType[*domain.ValidationError](err)
	return ok && ve.Field == "identities_provider_key"
}

// upsertGitHubUser finds the user behind a GitHub profile by the numeric id
// and refreshes its identity's login, or creates user and identity. signedIn
// marks the identity as used; an invite leaves last_login_at null.
func (s *Service) upsertGitHubUser(ctx context.Context, tx Store, prof githubProfile, now time.Time, signedIn bool) (domain.User, error) {
	name := prof.Name
	if name == "" {
		name = prof.Login
	}
	id, err := tx.GetIdentity(ctx, domain.ProviderGitHub, githubID(prof.ID))
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, fmt.Errorf("load identity: %w", err)
	}
	if err == nil {
		u, err := tx.LockUser(ctx, id.UserID)
		if err != nil {
			return domain.User{}, fmt.Errorf("load user: %w", err)
		}
		if u.Status == domain.RecordDeleted {
			// Deleting the account removes its identities. One that is gone
			// now went while this waited on the row, and the profile is new
			// here; one still there belongs to a deleted user, whom the
			// caller refuses.
			if _, err := tx.GetIdentity(ctx, domain.ProviderGitHub, githubID(prof.ID)); errors.Is(err, domain.ErrNotFound) {
				return s.createGitHubUser(ctx, tx, prof, name, now, signedIn)
			} else if err != nil {
				return domain.User{}, fmt.Errorf("load identity: %w", err)
			}
			return u, nil
		}
		if !signedIn {
			return u, nil
		}
		// The person's name and avatar are theirs once the account exists:
		// GitHub fills them only when it creates the person (#436).
		id.Login, id.LastLoginAt = prof.Login, &now
		if err := tx.UpdateIdentity(ctx, id); err != nil {
			return domain.User{}, fmt.Errorf("record login: %w", err)
		}
		return u, nil
	}
	return s.createGitHubUser(ctx, tx, prof, name, now, signedIn)
}

// createGitHubUser creates the user and the GitHub identity of a profile no
// identity holds.
func (s *Service) createGitHubUser(ctx context.Context, tx Store, prof githubProfile, name string, now time.Time, signedIn bool) (domain.User, error) {
	u, err := tx.CreateUser(ctx, domain.User{ID: s.ids.New(), Login: prof.Login, Name: name, Email: prof.Email, AvatarURL: prof.AvatarURL,
		Status: domain.RecordActive, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	ident := domain.Identity{ID: s.ids.New(), UserID: u.ID, Provider: domain.ProviderGitHub, ProviderID: githubID(prof.ID), Login: prof.Login, CreatedAt: now}
	if signedIn {
		ident.LastLoginAt = &now
	}
	if _, err := tx.CreateIdentity(ctx, ident); err != nil {
		return domain.User{}, fmt.Errorf("create identity: %w", err)
	}
	return u, nil
}

// linkOutcome is how a link callback ended.
type linkOutcome int

const (
	linkDone linkOutcome = iota
	// linkWithdrawn: the token that started the link no longer stands.
	linkWithdrawn
	// linkElsewhere: the GitHub account belongs to another user.
	linkElsewhere
	// linkSecond: the user already has a different GitHub account linked.
	linkSecond
)

// linkGitHub adds a GitHub identity to the user who started the link. Every
// outcome but linkDone changes nothing. No token is minted and no membership
// is touched.
//
// A user holds at most one GitHub identity: with a second one allowed, anyone
// who held a user's token for a moment could add their own GitHub account and
// keep signing in as that user after the token is revoked, unseen behind the
// one login the profile shows.
func (s *Service) linkGitHub(ctx context.Context, by pendingLink, prof githubProfile) (linkOutcome, error) {
	now := s.now()
	tok, err := s.store.GetToken(ctx, by.tokenID)
	if errors.Is(err, domain.ErrNotFound) {
		return linkWithdrawn, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load token: %w", err)
	}
	p, prob := s.standing(ctx, tok, by.session, now)
	if prob != nil {
		if isInternal(prob) {
			return 0, prob
		}
		return linkWithdrawn, nil
	}
	var outcome linkOutcome
	link := func(tx Store) error {
		outcome = linkDone
		// Locked as deleting the account locks it: an account deleted since
		// the check above gets no identity.
		u, err := tx.LockUser(ctx, p.Actor.ID)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if !u.Status.Alive() {
			outcome = linkWithdrawn
			return nil
		}
		id, err := tx.GetIdentity(ctx, domain.ProviderGitHub, githubID(prof.ID))
		switch {
		case errors.Is(err, domain.ErrNotFound):
			mine, err := tx.ListIdentities(ctx, p.Actor.ID)
			if err != nil {
				return fmt.Errorf("load identities: %w", err)
			}
			if slices.ContainsFunc(mine, func(i domain.Identity) bool { return i.Provider == domain.ProviderGitHub }) {
				outcome = linkSecond
				return nil
			}
			ident := domain.Identity{ID: s.ids.New(), UserID: p.Actor.ID, Provider: domain.ProviderGitHub, ProviderID: githubID(prof.ID), Login: prof.Login, LastLoginAt: &now, CreatedAt: now}
			if _, err := tx.CreateIdentity(ctx, ident); err != nil {
				return fmt.Errorf("create identity: %w", err)
			}
			return s.emitP(ctx, tx, p, nil, domain.EventIdentityLinked, membershipSubject(p.Actor.ID), identityPayload(ident))
		case err != nil:
			return fmt.Errorf("load identity: %w", err)
		case id.UserID != p.Actor.ID:
			outcome = linkElsewhere
			return nil
		}
		id.Login, id.LastLoginAt = prof.Login, &now
		if err := tx.UpdateIdentity(ctx, id); err != nil {
			return fmt.Errorf("record login: %w", err)
		}
		return nil
	}
	err = s.store.WithTx(ctx, link)
	if identityRace(err) {
		err = s.store.WithTx(ctx, link)
	}
	return outcome, err
}

func identityPayload(id domain.Identity) map[string]any {
	return map[string]any{"provider": id.Provider.String(), "login": id.Login}
}

// UnlinkGitHub removes every GitHub identity of the calling user. It refuses
// when no other way to sign in would be left: tokens expire, and the user
// could then never get a new one. Tokens already issued keep working.
func (s *Service) UnlinkGitHub(ctx context.Context) error {
	p, err := s.userPrincipal(ctx)
	if err == nil {
		err = p.requireWritable()
	}
	if err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		ids, err := tx.ListIdentities(ctx, p.Actor.ID)
		if err != nil {
			return fmt.Errorf("load identities: %w", err)
		}
		var github []domain.Identity
		other := false
		for _, id := range ids {
			switch id.Provider {
			case domain.ProviderGitHub:
				github = append(github, id)
			case domain.ProviderPassword:
				// A password the server would not take is no way in.
				other = other || (s.password && len(id.Secret) > 0)
			}
		}
		if len(github) == 0 {
			return newProblem(http.StatusNotFound, "no GitHub account is linked", "")
		}
		if !other {
			return newProblem(http.StatusConflict, "GitHub is your only way to sign in", lastSignInHint)
		}
		for _, id := range github {
			err := tx.DeleteIdentity(ctx, p.Actor.ID, id.ID)
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			if err != nil {
				return fmt.Errorf("delete identity: %w", err)
			}
			if err := s.emitP(ctx, tx, p, nil, domain.EventIdentityUnlinked, membershipSubject(p.Actor.ID), identityPayload(id)); err != nil {
				return err
			}
		}
		return nil
	})
}

// LoginGitHub signs in with a GitHub access token the CLI obtained through
// the device flow. Only a token issued to this server's own OAuth app counts:
// a token the user gave some other app proves nothing about who is calling.
// The token is used for that check and the profile read, then dropped.
func (s *Service) LoginGitHub(ctx context.Context, in apitypes.GitHubLogin) (apitypes.TokenCreated, error) {
	if s.github == nil {
		return apitypes.TokenCreated{}, githubOff()
	}
	var prof githubProfile
	err := s.github.issuedHere(ctx, in.AccessToken)
	if err == nil {
		prof, err = s.github.profile(ctx, in.AccessToken)
	}
	if errors.Is(err, errGitHubRejected) {
		return apitypes.TokenCreated{}, newProblem(http.StatusUnauthorized, "GitHub rejected the token", "run `lorvik login` again")
	}
	if err != nil {
		return apitypes.TokenCreated{}, s.githubUnavailable(ctx, err)
	}
	res, err := s.signInGitHub(ctx, prof, signInAsk{Org: in.Org, Choose: in.Choose, TokenName: in.TokenName, Picked: true})
	if err != nil {
		return apitypes.TokenCreated{}, err
	}
	if res.Pending {
		return apitypes.TokenCreated{}, pendingApproval()
	}
	return res.Token, nil
}

func (s *Service) githubUnavailable(ctx context.Context, err error) error {
	s.logger.WarnContext(ctx, "github request failed", "error", err)
	return newProblem(http.StatusBadGateway, "GitHub did not answer", "try again")
}

// Invite records a pending membership with a role for a GitHub user who may
// not have signed in yet. Admins invite up to reviewer, owners any role.
func (s *Service) Invite(ctx context.Context, in apitypes.Invite) (apitypes.Membership, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.Membership{}, err
	}
	if err := p.requireOrg(domain.PermMemberManage); err != nil {
		return apitypes.Membership{}, err
	}
	role, err := domain.ParseRole(in.Role)
	if err != nil {
		return apitypes.Membership{}, invalidField("role", err.Error())
	}
	if err := grantable(p, role); err != nil {
		return apitypes.Membership{}, err
	}
	if s.github == nil {
		return apitypes.Membership{}, githubOff()
	}
	prof, err := s.github.lookup(ctx, strings.TrimSpace(in.Login))
	if errors.Is(err, errGitHubNoUser) {
		return apitypes.Membership{}, invalidField("login", "no such GitHub user")
	}
	if err != nil {
		return apitypes.Membership{}, s.githubUnavailable(ctx, err)
	}
	var out domain.Membership
	invite := func(tx Store) error {
		now := s.now()
		u, err := s.upsertGitHubUser(ctx, tx, prof, now, false)
		if err != nil {
			return err
		}
		m := domain.Membership{OrgID: p.OrgID, Actor: u.Ref(), Role: role, Status: domain.RecordPending, GrantedBy: &p.Actor.ID, CreatedAt: now, UpdatedAt: now}
		existing, err := tx.LockMembership(ctx, p.OrgID, u.Ref())
		switch {
		case err == nil && existing.Status.Alive():
			return newProblem(http.StatusConflict, "already a member", "change their role with PUT /members")
		case err == nil:
			m.CreatedAt = existing.CreatedAt
		case !errors.Is(err, domain.ErrNotFound):
			return fmt.Errorf("load membership: %w", err)
		}
		if out, err = putMembership(ctx, tx, m); err != nil {
			return fmt.Errorf("put membership: %w", err)
		}
		return s.emitP(ctx, tx, p, nil, domain.EventMembershipInvited, membershipSubject(u.ID),
			map[string]any{"actor": toActor(u.Ref()), "role": role.String(), "login": prof.Login})
	}
	err = s.store.WithTx(ctx, invite)
	if identityRace(err) {
		err = s.store.WithTx(ctx, invite)
	}
	if err != nil {
		return apitypes.Membership{}, err
	}
	return toMembership(out), nil
}
