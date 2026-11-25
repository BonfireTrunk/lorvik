package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// tokenTTL resolves a requested lifetime against the org's bounds.
func tokenTTL(org domain.Org, requested string) (time.Duration, error) {
	if requested == "" {
		return org.Settings.TokenTTLDefaultOrDefault(), nil
	}
	d, err := time.ParseDuration(requested)
	if err != nil || d < domain.MinTokenTTL {
		return 0, invalidField("expires_in", "must be a Go duration of at least 1h, such as 720h")
	}
	if max := org.Settings.TokenTTLMaxOrDefault(); d > max {
		return 0, invalidField("expires_in", "longer than the org allows ("+max.String()+")")
	}
	return d, nil
}

// agentTokenTTL is the lifetime of a new agent token: AgentTokenTTL, or a
// shorter one when asked.
func agentTokenTTL(requested string, field string) (time.Duration, error) {
	if requested == "" {
		return domain.AgentTokenTTL, nil
	}
	d, err := time.ParseDuration(requested)
	if err != nil || d <= 0 || d > domain.AgentTokenTTL {
		return 0, invalidField(field, "agent tokens live at most 1h; the CLI renews them from your login")
	}
	return d, nil
}

// mintToken creates a token row and returns its secret.
func (s *Service) mintToken(ctx context.Context, tx Store, orgID domain.ID, actor domain.ActorRef, name string, scope domain.TokenScope, ttl time.Duration, now time.Time) (domain.Token, string, error) {
	t, secret := domain.NewToken(s.ids.New(), orgID, actor, name, scope, ttl, now)
	t, err := tx.CreateToken(ctx, t)
	if err != nil {
		return domain.Token{}, "", fmt.Errorf("create token: %w", err)
	}
	return t, secret, nil
}

const tokenList = "tokens"

// ListTokens lists a page of the caller's own tokens.
func (s *Service) ListTokens(ctx context.Context, token string, limit int) (apitypes.TokenList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.TokenList{}, err
	}
	cur, err := decodeCursor(tokenList, token)
	if err != nil {
		return apitypes.TokenList{}, err
	}
	var after *query.TokenCursor
	if cur != nil {
		after = &query.TokenCursor{CreatedAt: time.Unix(0, cur.CreatedAt).UTC(), ID: domain.ID(cur.ID)}
	}
	toks, remaining, err := s.store.ListTokens(ctx, p.OrgID, p.Actor, query.Page{Limit: limit}, after)
	if err != nil {
		return apitypes.TokenList{}, fmt.Errorf("list tokens: %w", err)
	}
	out := make([]apitypes.Token, 0, len(toks))
	for _, t := range toks {
		out = append(out, toToken(t))
	}
	return apitypes.TokenList{Items: out, Page: forwardPage(tokenList, toks, func(t domain.Token) cursor {
		return cursor{CreatedAt: t.CreatedAt.UnixNano(), ID: int64(t.ID)}
	}, remaining)}, nil
}

// CreateToken mints a token for the calling user, or for an agent the
// caller registered or may manage.
func (s *Service) CreateToken(ctx context.Context, in apitypes.CreateToken) (apitypes.TokenCreated, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.TokenCreated{}, err
	}
	if err := p.requireWritable(); err != nil {
		return apitypes.TokenCreated{}, err
	}
	scope := domain.ScopeWrite
	if in.Scope != "" {
		if scope, err = domain.ParseTokenScope(in.Scope); err != nil {
			return apitypes.TokenCreated{}, invalidField("scope", err.Error())
		}
	}
	if in.Agent != "" {
		return s.createAgentToken(ctx, p, in, scope)
	}
	org, err := s.store.GetOrg(ctx, p.OrgID)
	if err != nil {
		return apitypes.TokenCreated{}, fmt.Errorf("load org: %w", err)
	}
	ttl, err := tokenTTL(org, in.ExpiresIn)
	if err != nil {
		return apitypes.TokenCreated{}, err
	}
	tok, secret, err := s.mintToken(ctx, s.store, p.OrgID, p.Actor, in.Name, scope, ttl, s.now())
	if err != nil {
		return apitypes.TokenCreated{}, err
	}
	return apitypes.TokenCreated{Token: toToken(tok), Secret: secret}, nil
}

// createAgentToken mints a token for an agent the caller manages. The agent
// is locked while it is minted, so a revoke or removal either comes first
// and refuses it, or comes after and revokes it.
func (s *Service) createAgentToken(ctx context.Context, p Principal, in apitypes.CreateToken, scope domain.TokenScope) (apitypes.TokenCreated, error) {
	id, err := domain.ParseID(in.Agent)
	if err != nil {
		return apitypes.TokenCreated{}, invalidField("agent", "must be an agent id")
	}
	ttl, err := agentTokenTTL(in.ExpiresIn, "expires_in")
	if err != nil {
		return apitypes.TokenCreated{}, err
	}
	var out apitypes.TokenCreated
	err = s.store.WithTx(ctx, func(tx Store) error {
		ag, err := s.lockManageableAgent(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if ag.Status == domain.RecordRevoked {
			return withHint(fmt.Errorf("%w: %s", errAgentRevoked, ag.Name), revokedHint(ag.ID))
		}
		tok, secret, err := s.mintToken(ctx, tx, p.OrgID, ag.Ref(), in.Name, scope, ttl, s.now())
		if err != nil {
			return err
		}
		out = apitypes.TokenCreated{Token: toToken(tok), Secret: secret}
		return nil
	})
	return out, err
}

// RevokeToken revokes one of the caller's tokens, or anyone's for admins.
func (s *Service) RevokeToken(ctx context.Context, id domain.ID) error {
	p, err := s.principal(ctx)
	if err != nil {
		return err
	}
	if err := p.requireWritable(); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		t, err := tx.LockToken(ctx, id)
		if err != nil || t.OrgID != p.OrgID {
			return fmt.Errorf("token %s: %w", id, domain.ErrNotFound)
		}
		if t.Actor != p.Actor && !p.canOrg(domain.PermMemberManage) {
			if t.Actor.Kind != domain.ActorAgent {
				return fmt.Errorf("token %s: %w", id, domain.ErrNotFound)
			}
			if _, err := s.manageableAgent(ctx, tx, p, t.Actor.ID); err != nil {
				return err
			}
		}
		if t.Status == domain.RecordRevoked {
			return nil
		}
		t.Status = domain.RecordRevoked
		if err := tx.UpdateToken(ctx, t); err != nil {
			return fmt.Errorf("revoke token: %w", err)
		}
		return nil
	})
}

// rotateToken mints a successor for tok and keeps tok usable for the grace
// period. It returns "" when another request already rotated it.
func (s *Service) rotateToken(ctx context.Context, tok domain.Token, now time.Time) (string, error) {
	var secret string
	err := s.store.WithTx(ctx, func(tx Store) error {
		cur, err := tx.LockToken(ctx, tok.ID)
		if err != nil {
			return err
		}
		if cur.Status != domain.RecordActive {
			return nil
		}
		org, err := tx.GetOrg(ctx, cur.OrgID)
		if err != nil {
			return fmt.Errorf("load org: %w", err)
		}
		ttl := min(max(cur.ExpiresAt.Sub(cur.CreatedAt), domain.MinTokenTTL), org.Settings.TokenTTLMaxOrDefault())
		next, sec, err := s.mintToken(ctx, tx, cur.OrgID, cur.Actor, cur.Name, cur.Scope, ttl, now)
		if err != nil {
			return err
		}
		cur.Status, cur.ExpiresAt, cur.ReplacedBy = domain.RecordRotated, now.Add(domain.TokenRotationGrace), &next.ID
		if err := tx.UpdateToken(ctx, cur); err != nil {
			return fmt.Errorf("mark rotated: %w", err)
		}
		secret = sec
		return s.emit(ctx, tx, cur.Actor, cur.OrgID, domain.EventTokenRotated, tokenSubject(cur.ID),
			map[string]any{"prefix": cur.Prefix, "replaced_by": next.ID.String()})
	})
	return secret, err
}

// tokenReuse revokes a rotated token that is still presented after its grace
// period and records the fact, since the old secret is evidently in use. A
// token no longer rotated by then was handled by another request.
func (s *Service) tokenReuse(ctx context.Context, tok domain.Token, now time.Time) error {
	return s.store.WithTx(ctx, func(tx Store) error {
		tok, err := tx.LockToken(ctx, tok.ID)
		if err != nil {
			return err
		}
		if tok.Status != domain.RecordRotated {
			return nil
		}
		tok.Status = domain.RecordRevoked
		if err := tx.UpdateToken(ctx, tok); err != nil {
			return fmt.Errorf("revoke reused token: %w", err)
		}
		return s.emit(ctx, tx, tok.Actor, tok.OrgID, domain.EventTokenReuseDetected, tokenSubject(tok.ID),
			map[string]any{"prefix": tok.Prefix, "rotated_at": tok.ExpiresAt.Add(-domain.TokenRotationGrace), "seen_at": now})
	})
}

// signInFailedTimeout bounds the recording of a failed sign-in.
const signInFailedTimeout = 5 * time.Second

// recordSignInFailed writes an identity.signin_failed event in every org the
// user is an active member of, so they and the org's admins learn that the
// login is being tried; when the wrong password filled a sign-in limit
// (refused) it writes identity.signin_refused as well, which tells guessing
// from a slip. It runs aside of the request: a write for a login that exists
// and none for one that does not would otherwise show in how long the
// refusal takes. Only the first of each type in an hour is recorded
// (failedEventWindow), and a throttled attempt never gets here, so a caller
// cannot grow the event log at will. A failure is only logged.
func (s *Service) recordSignInFailed(ctx context.Context, userID domain.ID, refused bool) {
	types := []domain.EventType{domain.EventIdentitySignInFailed}
	if refused {
		types = append(types, domain.EventIdentitySignInRefused)
	}
	ctx = context.WithoutCancel(ctx)
	s.aside(func() {
		for _, typ := range types {
			s.recordSignInEvent(ctx, userID, typ)
		}
	})
}

// recordSignInEvent is recordSignInFailed for one event type, in its own
// signInFailedTimeout: a slow write of one type must not cost the next its
// time.
func (s *Service) recordSignInEvent(base context.Context, userID domain.ID, typ domain.EventType) {
	ctx, cancel := context.WithTimeout(base, signInFailedTimeout)
	defer cancel()
	// Nothing recovers a panic on the aside goroutine but this.
	defer func() {
		if r := recover(); r != nil {
			s.logger.ErrorContext(ctx, "record failed sign-in panicked", "user_id", userID, "type", typ.String(), "panic", fmt.Sprint(r))
		}
	}()
	if !s.limits.recordsFailure(ctx, typ, userID) {
		return
	}
	// The hour's one event is spent only when one was written: a failed
	// write must not leave the wrong passwords that follow unrecorded.
	recorded := false
	defer func() {
		if !recorded {
			// The write's deadline may be what failed it, so giving the
			// slot back gets one of its own.
			ctx, cancel := context.WithTimeout(base, signInFailedTimeout)
			defer cancel()
			s.limits.forgetFailure(ctx, typ, userID)
		}
	}()
	user := domain.ActorRef{Kind: domain.ActorUser, ID: userID}
	ms, err := s.store.ListMembershipsForActor(ctx, user)
	if err != nil {
		s.logger.WarnContext(ctx, "record failed sign-in", "user_id", userID, "type", typ.String(), "error", err)
		return
	}
	for _, m := range ms {
		if m.Status != domain.RecordActive {
			continue
		}
		err := s.store.WithTx(ctx, func(tx Store) error {
			return s.emitSession(ctx, tx, user, domain.UnpromptedSession, m.OrgID, nil, typ, membershipSubject(userID), nil)
		})
		if err != nil {
			s.logger.WarnContext(ctx, "record failed sign-in", "user_id", userID, "type", typ.String(), "error", err)
			continue
		}
		recorded = true
	}
}

// Login exchanges a password identity for a token. It is open only when the
// server enables password auth.
func (s *Service) Login(ctx context.Context, in apitypes.Login) (apitypes.TokenCreated, error) {
	if !s.password {
		return apitypes.TokenCreated{}, newProblem(http.StatusNotFound, "password login is disabled", "set LORVIK_AUTH_PASSWORD=true on the server, or sign in with a provider")
	}
	try := s.limits.attempt(ctx, in.Login)
	if err := s.limits.throttled(ctx, try); err != nil {
		return apitypes.TokenCreated{}, err
	}
	id, err := s.store.GetIdentity(ctx, domain.ProviderPassword, try.login)
	known := err == nil
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return apitypes.TokenCreated{}, fmt.Errorf("load identity: %w", err)
	}
	ok, filled, err := s.limits.checkPassword(ctx, try, id.Secret, known, in.Password)
	if err != nil {
		return apitypes.TokenCreated{}, err
	}
	if !ok {
		// An unknown login and a wrong password differ in this log line
		// and in the event recorded after the answer, and nowhere a caller
		// can see.
		event := eventUnknownLogin
		if known {
			event = eventWrongPassword
			s.recordSignInFailed(ctx, id.UserID, filled)
		}
		s.limits.log(ctx, event, try.caller, try.login, id.UserID)
		return apitypes.TokenCreated{}, newProblem(http.StatusUnauthorized, "wrong login or password", "check the login and password")
	}
	u, err := s.store.GetUser(ctx, id.UserID)
	if err != nil {
		return apitypes.TokenCreated{}, fmt.Errorf("load user: %w", err)
	}
	if err := userGate(u); err != nil {
		return apitypes.TokenCreated{}, err
	}
	var org *domain.Org
	if in.Org != "" {
		// The same answer as for an org the user is no member of, so a
		// password does not tell which org ids exist.
		named, err := s.namedOrg(ctx, in.Org, notAMember())
		if err != nil {
			return apitypes.TokenCreated{}, err
		}
		org = &named
	}
	ask := signInAsk{Org: in.Org, Choose: in.Choose, TokenName: cmp.Or(in.TokenName, loginTokenName), Picked: true}
	now := s.now()
	var res signInResult
	err = s.store.WithTx(ctx, func(tx Store) error {
		if res, err = s.settle(ctx, tx, u, org, ask, now); err != nil {
			return err
		}
		id.LastLoginAt = &now
		if err := tx.UpdateIdentity(ctx, id); err != nil {
			return fmt.Errorf("record login: %w", err)
		}
		return nil
	})
	if err != nil {
		return apitypes.TokenCreated{}, err
	}
	if res.Pending {
		return apitypes.TokenCreated{}, pendingApproval()
	}
	return res.Token, nil
}
