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
	"unicode"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const (
	signInTokenName = "sign-in"
	loginTokenName  = "login"
	// signInPath is where a sign-in token is accepted: the routes that list,
	// create and enter orgs. Every other route refuses it.
	signInPath = apiPrefix + "/login/orgs"
	// mePath takes a sign-in token for DELETE only: a person a GitHub
	// sign-in created by mistake deletes themself from the Create-an-org screen.
	mePath = apiPrefix + "/me"
)

// takesSignIn reports whether r is a request a sign-in token may make.
func takesSignIn(r *http.Request) bool {
	path := r.URL.Path
	return path == signInPath || strings.HasPrefix(path, signInPath+"/") || r.Method == http.MethodDelete && path == mePath
}

// signInAsk is what a sign-in asked for beside proving who it is.
type signInAsk struct {
	// Org names the org to enter; empty leaves the choice to the server.
	Org string
	// Choose asks for a sign-in token even when one org would follow, so a
	// person with one org can create another.
	Choose    bool
	TokenName string
	// AskAs is the GitHub login a request to join the named org is made
	// under. Empty refuses a person who is no member instead.
	AskAs string
	// Picked says the person picked the org themself: they typed it for the
	// CLI or the password sign-in, or chose it from their list. Only then is
	// an invitation to it accepted. A link that names the org is not the
	// person's pick, since anyone can send one.
	Picked bool
}

// signInResult is the outcome of a sign-in: a token, or a request to join
// waiting for an admin. With Choose the token is a sign-in token.
type signInResult struct {
	Token   apitypes.TokenCreated
	Choose  bool
	Pending bool
	// OrgName names the org a pending request is for.
	OrgName string
}

// namedOrg loads the org a sign-in names. An org that is not active answers
// like one that does not exist, with missing.
func (s *Service) namedOrg(ctx context.Context, requested string, missing error) (domain.Org, error) {
	id, err := domain.ParseID(requested)
	if err != nil {
		return domain.Org{}, invalidField("org", "must be an org id")
	}
	org, err := s.store.GetOrg(ctx, id)
	if errors.Is(err, domain.ErrNotFound) || err == nil && !org.Status.Alive() {
		return domain.Org{}, missing
	}
	if err != nil {
		return domain.Org{}, fmt.Errorf("load org: %w", err)
	}
	return org, nil
}

// openOrg is an org a person may see at sign-in, with their row in it.
type openOrg struct {
	org domain.Org
	m   domain.Membership
}

// Membership states of an org open to a sign-in, as the wire names them.
const (
	openMember    = "member"
	openInvited   = "invited"
	openRequested = "requested"
)

func (o openOrg) state() string {
	switch {
	case o.m.Status.Alive():
		return openMember
	case o.m.GrantedBy != nil:
		return openInvited
	}
	return openRequested
}

// openOrgs lists the active orgs the person is a member of, invited to, or
// has asked to join, by name.
func (s *Service) openOrgs(ctx context.Context, st Store, actor domain.ActorRef) ([]openOrg, error) {
	ms, err := st.ListOpenMembershipsForActor(ctx, actor)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	out := make([]openOrg, 0, len(ms))
	for _, m := range ms {
		org, err := st.GetOrg(ctx, m.OrgID)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("load org: %w", err)
		}
		if org.Status.Alive() {
			out = append(out, openOrg{org: org, m: m})
		}
	}
	slices.SortFunc(out, func(a, b openOrg) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.org.Name), strings.ToLower(b.org.Name)), cmp.Compare(a.org.ID, b.org.ID))
	})
	return out, nil
}

// settle gives a person who has proved who they are what the sign-in asked
// for, inside the sign-in's transaction. A named org is entered or asked to
// join. With none named, a person who is a member of one org and invited to
// none enters it; anyone else, or a sign-in that asks to choose, gets a
// sign-in token and the choice is the client's. An invitation is a row the
// inviting org writes alone, so it never decides where a sign-in goes.
func (s *Service) settle(ctx context.Context, tx Store, u domain.User, org *domain.Org, ask signInAsk, now time.Time) (signInResult, error) {
	if org != nil {
		return s.enter(ctx, tx, u, *org, ask, now)
	}
	// Only a sign-in that names an org asks to join it. Without this, a person removed while their sign-in settles on that org would leave a request there.
	ask.AskAs = ""
	if !ask.Choose {
		open, err := s.openOrgs(ctx, tx, u.Ref())
		if err != nil {
			return signInResult{}, err
		}
		open = slices.DeleteFunc(open, func(o openOrg) bool { return o.state() == openRequested })
		if len(open) == 1 && open[0].state() == openMember {
			return s.enter(ctx, tx, u, open[0].org, ask, now)
		}
	}
	return s.choice(ctx, tx, u, now)
}

// choice mints the sign-in token that lets the person list, create and enter
// their orgs.
func (s *Service) choice(ctx context.Context, tx Store, u domain.User, now time.Time) (signInResult, error) {
	tok, secret, err := s.mintToken(ctx, tx, 0, u.Ref(), signInTokenName, domain.ScopeSignIn, domain.SignInTokenTTL, now)
	if err != nil {
		return signInResult{}, err
	}
	return signInResult{Token: apitypes.TokenCreated{Token: toToken(tok), Secret: secret}, Choose: true}, nil
}

// enter mints the person's token for one org. A member gets it. An invited
// person gets it, and the invitation is accepted, when they picked the org
// themself (ask.Picked); otherwise they get the choice, where the invitation
// is listed. A person whose request to join is waiting is told so. Anyone
// else leaves a request to join when the sign-in may ask (ask.AskAs), and is
// refused otherwise.
func (s *Service) enter(ctx context.Context, tx Store, u domain.User, org domain.Org, ask signInAsk, now time.Time) (signInResult, error) {
	subj := membershipSubject(u.ID)
	m, err := tx.LockMembership(ctx, org.ID, u.Ref())
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return signInResult{}, fmt.Errorf("load membership: %w", err)
	}
	found := err == nil
	switch {
	case found && m.Status.Alive():
	case found && m.Status == domain.RecordPending && m.GrantedBy != nil:
		if !ask.Picked {
			return s.choice(ctx, tx, u, now)
		}
		m.Status, m.UpdatedAt = domain.RecordActive, now
		if _, err := putMembership(ctx, tx, m); err != nil {
			return signInResult{}, fmt.Errorf("activate membership: %w", err)
		}
		inviter := domain.ActorRef{Kind: domain.ActorUser, ID: *m.GrantedBy}
		if err := s.emit(ctx, tx, inviter, org.ID, domain.EventMembershipGranted, subj,
			map[string]any{"actor": toActor(u.Ref()), "role": m.Role.String()}); err != nil {
			return signInResult{}, err
		}
	case found && m.Status == domain.RecordPending:
		return signInResult{Pending: true, OrgName: org.Name}, nil
	case ask.AskAs != "":
		req := domain.Membership{OrgID: org.ID, Actor: u.Ref(), Role: domain.RoleViewer, Status: domain.RecordPending, CreatedAt: now, UpdatedAt: now}
		if _, err := putMembership(ctx, tx, req); err != nil {
			return signInResult{}, fmt.Errorf("request membership: %w", err)
		}
		if err := s.emit(ctx, tx, u.Ref(), org.ID, domain.EventMembershipRequested, subj,
			map[string]any{"actor": toActor(u.Ref()), "role": domain.RoleViewer.String(), "login": ask.AskAs}); err != nil {
			return signInResult{}, err
		}
		return signInResult{Pending: true, OrgName: org.Name}, nil
	default:
		return signInResult{}, notAMember()
	}
	tok, secret, err := s.mintToken(ctx, tx, org.ID, u.Ref(), ask.TokenName, domain.ScopeWrite, org.Settings.TokenTTLDefaultOrDefault(), now)
	if err != nil {
		return signInResult{}, err
	}
	return signInResult{Token: apitypes.TokenCreated{Token: toToken(tok), Secret: secret}}, nil
}

func notAMember() error {
	return newProblem(http.StatusForbidden, "not a member of that org", "ask an admin of the org to invite you")
}

func pendingApproval() error {
	return newProblem(http.StatusForbidden, "membership pending approval", pendingHint)
}

// signInStanding is standing for a sign-in token on the routes that take
// one: the token must still be usable and its person allowed to sign in. It
// carries no org and no role.
func (s *Service) signInStanding(ctx context.Context, tok domain.Token, now time.Time) (Principal, *problemError) {
	if !tok.Usable(now) || tok.Actor.Kind != domain.ActorUser {
		return Principal{}, signInOver()
	}
	u, err := s.store.GetUser(ctx, tok.Actor.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return Principal{}, newProblem(http.StatusUnauthorized, "unknown user", "the user behind this token no longer exists")
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "look up user", "error", err)
		return Principal{}, newProblem(http.StatusInternalServerError, "internal error", "")
	}
	if prob := userGate(u); prob != nil {
		return Principal{}, prob
	}
	return Principal{Actor: tok.Actor, TokenID: tok.ID, TokenPrefix: tok.Prefix, Scope: tok.Scope}, nil
}

func signInOver() *problemError {
	return newProblem(http.StatusUnauthorized, "this sign-in was used or has expired", "sign in again")
}

// signInOnly refuses a sign-in token outside the routes that take one.
func signInOnly() *problemError {
	return newProblem(http.StatusForbidden, "no org chosen yet", "this sign-in only chooses or creates an org: GET /login/orgs, then POST /login/orgs/{id}/token. A lorvik CLI that saved it is older than this server: upgrade it and run lorvik login again")
}

// signInPrincipal is the caller of a route that takes a sign-in token. An
// ordinary token is refused: it belongs to one org and must not open another.
func (s *Service) signInPrincipal(ctx context.Context) (Principal, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return Principal{}, fmt.Errorf("%w: no credentials on request", domain.ErrUnauthorized)
	}
	if p.Scope != domain.ScopeSignIn {
		return Principal{}, newProblem(http.StatusForbidden, "a token of one org does not open another", "sign in again and choose the org")
	}
	return p, nil
}

// liveSignIn locks the caller's sign-in token and refuses one that was used
// or has expired since the request was let in.
func (s *Service) liveSignIn(ctx context.Context, tx Store, p Principal, now time.Time) (domain.Token, error) {
	tok, err := tx.LockToken(ctx, p.TokenID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Token{}, signInOver()
	}
	if err != nil {
		return domain.Token{}, fmt.Errorf("load sign-in token: %w", err)
	}
	if !tok.Usable(now) {
		return domain.Token{}, signInOver()
	}
	return tok, nil
}

func toSignInOrg(o openOrg) apitypes.SignInOrg {
	return apitypes.SignInOrg{ID: o.org.ID.String(), Name: o.org.Name, Role: o.m.Role.String(), Status: o.state()}
}

// SignInOrgs lists the orgs open to the person who signed in.
func (s *Service) SignInOrgs(ctx context.Context) (apitypes.SignInOrgs, error) {
	p, err := s.signInPrincipal(ctx)
	if err != nil {
		return apitypes.SignInOrgs{}, err
	}
	open, err := s.openOrgs(ctx, s.store, p.Actor)
	if err != nil {
		return apitypes.SignInOrgs{}, err
	}
	u, err := s.store.GetUser(ctx, p.Actor.ID)
	if err != nil {
		return apitypes.SignInOrgs{}, fmt.Errorf("load user: %w", err)
	}
	out := apitypes.SignInOrgs{Orgs: make([]apitypes.SignInOrg, 0, len(open)), Login: u.Login}
	for _, o := range open {
		out.Orgs = append(out.Orgs, toSignInOrg(o))
	}
	return out, nil
}

// CreateOrg creates an org owned by the person who signed in. Any person
// may: a server takes new orgs the way a hosted service does.
func (s *Service) CreateOrg(ctx context.Context, in apitypes.CreateOrg) (apitypes.SignInOrg, error) {
	p, err := s.signInPrincipal(ctx)
	if err != nil {
		return apitypes.SignInOrg{}, err
	}
	name, err := orgName(in.Name)
	if err != nil {
		return apitypes.SignInOrg{}, err
	}
	now := s.now()
	var out openOrg
	err = s.store.WithTx(ctx, func(tx Store) error {
		if _, err := s.liveSignIn(ctx, tx, p, now); err != nil {
			return err
		}
		org, m, err := NewOrg(ctx, tx, s.ids, name, p.Actor, now)
		out = openOrg{org: org, m: m}
		return err
	})
	if err != nil {
		return apitypes.SignInOrg{}, err
	}
	s.logger.InfoContext(ctx, "org created", "org_id", out.org.ID, "user_id", p.Actor.ID, "request_id", RequestIDFrom(ctx))
	return toSignInOrg(out), nil
}

// orgName is the name an org may carry. People who are only invited to an
// org read its name in their terminal at sign-in, so a name holds no control
// characters: a line break or an escape sequence could redraw the list it is
// printed in.
func orgName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", invalidField("name", "required")
	}
	if strings.ContainsFunc(name, unicode.IsControl) {
		return "", invalidField("name", "must not hold line breaks or other control characters")
	}
	return name, nil
}

// NewOrg creates an org with its owner and its global project. It is the one
// way an org comes to exist: lorvikd admin bootstrap and a person creating
// one after signing in both call it inside their transaction.
func NewOrg(ctx context.Context, tx Store, ids *domain.IDGen, name string, owner domain.ActorRef, now time.Time) (domain.Org, domain.Membership, error) {
	org, err := tx.CreateOrg(ctx, domain.Org{ID: ids.New(), Name: name, Status: domain.RecordActive, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return domain.Org{}, domain.Membership{}, fmt.Errorf("create org: %w", err)
	}
	m, err := putMembership(ctx, tx, domain.Membership{OrgID: org.ID, Actor: owner, Role: domain.RoleOwner, Status: domain.RecordActive, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return domain.Org{}, domain.Membership{}, fmt.Errorf("add membership: %w", err)
	}
	global := domain.Project{ID: ids.New(), OrgID: org.ID, Slug: domain.GlobalSlug, Name: "Global",
		Description: "What applies across all projects of the org.", Visibility: domain.VisibilityOrg,
		Concurrency: domain.ConcurrencyParallel, Scope: domain.ProjectScopeGlobal, Lease: domain.DefaultLeaseDuration,
		CreatedBy: owner, Status: domain.RecordActive, CreatedAt: now, UpdatedAt: now}
	if err := global.Validate(); err != nil {
		return domain.Org{}, domain.Membership{}, fmt.Errorf("global project: %w", err)
	}
	if _, err := tx.CreateProject(ctx, global); err != nil {
		return domain.Org{}, domain.Membership{}, fmt.Errorf("create global project: %w", err)
	}
	return org, m, nil
}

// EnterOrg ends the sign-in: it mints the person's token for an org they are
// a member of or invited to, and the sign-in token stops working. An org the
// person has no place in answers the same whether it exists or not.
func (s *Service) EnterOrg(ctx context.Context, id domain.ID, in apitypes.EnterOrg) (apitypes.TokenCreated, error) {
	p, err := s.signInPrincipal(ctx)
	if err != nil {
		return apitypes.TokenCreated{}, err
	}
	org, err := s.store.GetOrg(ctx, id)
	if errors.Is(err, domain.ErrNotFound) || err == nil && !org.Status.Alive() {
		return apitypes.TokenCreated{}, notAMember()
	}
	if err != nil {
		return apitypes.TokenCreated{}, fmt.Errorf("load org: %w", err)
	}
	ask := signInAsk{TokenName: cmp.Or(in.TokenName, loginTokenName), Picked: true}
	now := s.now()
	var res signInResult
	err = s.store.WithTx(ctx, func(tx Store) error {
		tok, err := s.liveSignIn(ctx, tx, p, now)
		if err != nil {
			return err
		}
		u, err := tx.GetUser(ctx, p.Actor.ID)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if res, err = s.enter(ctx, tx, u, org, ask, now); err != nil {
			return err
		}
		if res.Pending {
			return pendingApproval()
		}
		tok.Status = domain.RecordRevoked
		if err := tx.UpdateToken(ctx, tok); err != nil {
			return fmt.Errorf("end sign-in: %w", err)
		}
		return nil
	})
	if err != nil {
		return apitypes.TokenCreated{}, err
	}
	return res.Token, nil
}
