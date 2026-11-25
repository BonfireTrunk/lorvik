package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const (
	apiPrefix      = "/api/v1"
	touchInterval  = time.Minute
	problemContent = "application/problem+json"
	// forgetAfter is how long a token or chat stays in the touch memory
	// without a request; a sweep at most every sweepInterval drops the rest,
	// so labels sent once cannot grow the memory without bound.
	forgetAfter   = 24 * time.Hour
	sweepInterval = time.Hour
	// chatsKept is how many of an agent's chats the store keeps, most
	// recently seen first; chats that hold a grant are kept besides.
	chatsKept = 1000
)

// authenticator verifies bearer tokens and decorates requests with the
// caller's identity, token rotation, claim renewal and the unread header.
type authenticator struct {
	svc    *Service
	logger *slog.Logger

	mu      sync.Mutex
	touched map[domain.ID]time.Time
	seen    map[seenSession]time.Time
	// named is the name each chat sent on its last request and the store
	// took, so a busy chat reaches the store only when its name changes or
	// its touch is due. A request without a name forgets the entry.
	named map[seenSession]string
	// swept is when the memory was last swept of entries past forgetAfter.
	swept time.Time
}

// seenSession names a chat of an agent by its base label.
type seenSession struct {
	agent domain.ID
	label string
}

func newAuthenticator(svc *Service, logger *slog.Logger) *authenticator {
	a := &authenticator{svc: svc, logger: logger, touched: map[domain.ID]time.Time{}, seen: map[seenSession]time.Time{}, named: map[seenSession]string{}}
	if svc != nil {
		svc.chats = a
	}
	return a
}

func isPublicPath(path string) bool {
	switch path {
	case apiPrefix + "/healthz", apiPrefix + "/openapi.json", apiPrefix + "/openapi.yaml", apiPrefix + "/docs", apiPrefix + "/login", apiPrefix + "/login/github", apiPrefix + "/auth/github":
		return true
	}
	return strings.HasPrefix(path, apiPrefix+"/schemas/")
}

// middleware wraps the API handler. A nil service answers 503 for everything
// that is not public so the UI-only build stays usable.
func (a *authenticator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		if a.svc == nil {
			writeProblem(w, newProblem(http.StatusServiceUnavailable, "database not configured", "set LORVIK_DATABASE_URL and restart lorvikd"))
			return
		}
		p, prob := a.authenticate(ctx, w, r)
		if prob != nil {
			if isInternal(prob) {
				a.logger.ErrorContext(ctx, "authenticate", "request_id", RequestIDFrom(ctx))
			}
			writeProblem(w, prob)
			return
		}
		if p.Scope != domain.ScopeSignIn {
			a.decorate(ctx, w, r, p)
			if wantsHolding(r) {
				w = a.withHolding(ctx, w, p)
			}
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, p)))
	})
}

func (a *authenticator) authenticate(ctx context.Context, w http.ResponseWriter, r *http.Request) (Principal, *problemError) {
	scheme, secret, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	secret = strings.TrimSpace(secret)
	if !strings.EqualFold(scheme, "Bearer") || secret == "" {
		return Principal{}, newProblem(http.StatusUnauthorized, "missing bearer token", "sign in with `lorvik login`; agents and CI set LORVIK_TOKEN or LORVIK_CONFIG")
	}
	st := a.svc.store
	tok, err := st.GetTokenByHash(ctx, domain.HashToken(secret))
	if errors.Is(err, domain.ErrNotFound) {
		return Principal{}, newProblem(http.StatusUnauthorized, "unknown token", "set LORVIK_TOKEN to a valid token")
	}
	if clientGone(ctx, err) {
		a.logger.DebugContext(ctx, "request abandoned by the client", "request_id", RequestIDFrom(ctx))
		return Principal{}, abandoned()
	}
	if err != nil {
		a.logger.ErrorContext(ctx, "look up token", "error", err)
		return Principal{}, newProblem(http.StatusInternalServerError, "internal error", "")
	}
	now := a.svc.now()
	if tok.Scope == domain.ScopeSignIn && takesSignIn(r) {
		return a.svc.signInStanding(ctx, tok, now)
	}
	if tok.Status == domain.RecordRotated && !now.Before(tok.ExpiresAt) {
		// The grace period after rotation is over, yet the old secret is
		// still in use somewhere: record it so an admin can look.
		a.reuseDetected(ctx, tok, now)
	}
	p, prob := a.svc.standing(ctx, tok, r.Header.Get(apitypes.HeaderSession), now)
	if prob != nil {
		return Principal{}, prob
	}
	a.touch(ctx, tok.ID, now)
	if tok.NeedsRotation(now) {
		a.rotate(ctx, w, tok, now)
	}
	return p, nil
}

// standing is the caller a token stands for now, or the problem that refuses
// it: the token's state, the session label, the user or agent behind it, its
// org membership and, for an agent, its owner. Every request runs it, and an
// open event stream runs it again on each wake, so a caller that loses its
// rights loses the stream too.
func (s *Service) standing(ctx context.Context, tok domain.Token, session string, now time.Time) (Principal, *problemError) {
	switch {
	case tok.Scope == domain.ScopeSignIn:
		return Principal{}, signInOnly()
	case tok.Status == domain.RecordRevoked && tok.Actor.Kind == domain.ActorAgent && s.agentRevoked(ctx, tok.Actor.ID):
		return Principal{}, agentRevokedProblem()
	case tok.Status == domain.RecordRevoked:
		return Principal{}, newProblem(http.StatusUnauthorized, "token revoked", "token revoked: ask a user to create a new one and set LORVIK_TOKEN")
	case tok.Status == domain.RecordRotated && !now.Before(tok.ExpiresAt):
		return Principal{}, newProblem(http.StatusUnauthorized, "token rotated", "this token was replaced; use the secret from the X-Lorvik-Token header of an earlier response, or create a new token")
	case !tok.Usable(now):
		return Principal{}, newProblem(http.StatusUnauthorized, "token expired", "token expired: create a new one and set LORVIK_TOKEN")
	case tok.OutlivesAgentLimit():
		return Principal{}, newProblem(http.StatusUnauthorized, "agent token outlives the one-hour limit",
			"agent tokens live at most 1h: in a chat of a harness lorvik recognises, unset LORVIK_CONFIG and LORVIK_TOKEN and it renews the harness's own agent from your login; else mint one with `lorvik token create --agent <id>`")
	}
	if session != "" && !apitypes.ValidSession(session) {
		return Principal{}, newProblem(http.StatusUnprocessableEntity, apitypes.HeaderSession+" is not a valid session label",
			"use up to 40 lowercase letters, digits and hyphens; set LORVIK_SESSION")
	}
	p := Principal{Session: session, Actor: tok.Actor, OrgID: tok.OrgID, TokenID: tok.ID, TokenPrefix: tok.Prefix, Scope: tok.Scope, ReadOnly: tok.Scope == domain.ScopeRead}
	if !p.IsAgent() {
		u, err := s.store.GetUser(ctx, tok.Actor.ID)
		if errors.Is(err, domain.ErrNotFound) {
			return Principal{}, newProblem(http.StatusUnauthorized, "unknown user", "the user behind this token no longer exists")
		}
		if err != nil {
			s.logger.ErrorContext(ctx, "look up user", "error", err)
			return Principal{}, newProblem(http.StatusInternalServerError, "internal error", "")
		}
		if err := userGate(u); err != nil {
			return Principal{}, err
		}
	}
	// A closed org stops every token in it, so closing does not rest on each
	// token having been revoked.
	org, err := s.store.GetOrg(ctx, tok.OrgID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.logger.ErrorContext(ctx, "look up org", "error", err)
		return Principal{}, newProblem(http.StatusInternalServerError, "internal error", "")
	}
	if err != nil || !org.Status.Alive() {
		return Principal{}, newProblem(http.StatusUnauthorized, "org closed", "this org no longer exists; sign in again and choose another org")
	}
	m, err := s.store.GetMembership(ctx, tok.OrgID, tok.Actor)
	switch {
	case err == nil && m.Status.Alive():
		p.OrgRole, p.HasOrgRole, p.OrgRights = m.Role, true, m.Rights
	case err == nil && m.Status == domain.RecordPending:
		return Principal{}, newProblem(http.StatusForbidden, "membership pending approval", "ask an org admin to approve you, then retry")
	case err != nil && !errors.Is(err, domain.ErrNotFound):
		s.logger.ErrorContext(ctx, "look up membership", "error", err)
		return Principal{}, newProblem(http.StatusInternalServerError, "internal error", "")
	case !p.IsAgent():
		// Humans need an org membership; agents may be bound to projects only.
		return Principal{}, newProblem(http.StatusForbidden, "not a member of this org", "ask an org admin to add you, then retry")
	}
	if p.IsAgent() {
		ag, err := s.store.GetAgent(ctx, tok.Actor.ID)
		if err != nil || ag.OrgID != tok.OrgID {
			return Principal{}, newProblem(http.StatusUnauthorized, "unknown agent", "the agent behind this token no longer exists")
		}
		switch ag.Status {
		case domain.RecordPending:
			return Principal{}, newProblem(http.StatusForbidden, "agent pending approval", "ask an org admin to approve this agent with `lorvik agent approve`")
		case domain.RecordRevoked:
			return Principal{}, agentRevokedProblem()
		case domain.RecordForgotten:
			return Principal{}, newProblem(http.StatusUnauthorized, "unknown agent", "the agent behind this token no longer exists")
		}
		owner, role, gap, err := agentOwner(ctx, s.store, ag)
		if err != nil {
			s.logger.ErrorContext(ctx, "look up agent owner", "error", err)
			return Principal{}, newProblem(http.StatusInternalServerError, "internal error", "")
		}
		if prob := ownerProblem(gap); prob != nil {
			return Principal{}, prob
		}
		p.Owner, p.OwnerOrgRole, p.AgentName = owner, role, ag.Name
		p.OrgRole = min(p.OrgRole, role)
		p.OrgRights &= domain.RightsHeldBy(role)
	}
	return p, nil
}

// agentRevoked reports whether the agent was revoked, which revokes its
// tokens too, so the caller hears why rather than only that its token is.
func (s *Service) agentRevoked(ctx context.Context, id domain.ID) bool {
	ag, err := s.store.GetAgent(ctx, id)
	return err == nil && ag.Status == domain.RecordRevoked
}

func agentRevokedProblem() *problemError {
	return newProblem(http.StatusUnauthorized, "agent revoked", "its owner revoked this agent; a revoked agent is never registered again by itself, so ask the owner to restore it on the dashboard's Access page, or to remove it there so the next chat registers a new one")
}

// ownerProblem refuses an agent whose owner cannot lend it rights, the way
// the owner themselves would be refused where there is a match.
func ownerProblem(gap ownerGap) *problemError {
	switch gap {
	case ownerSuspended:
		return newProblem(http.StatusForbidden, "agent's owner is suspended", "ask an org admin to reinstate the user who owns this agent")
	case ownerGone:
		return newProblem(http.StatusUnauthorized, "agent's owner is deleted", "the user who owned this agent was deleted; register a new agent from an active account")
	case ownerNotMember:
		return newProblem(http.StatusForbidden, "agent's owner is not a member of this org", "ask an org admin to add the agent's owner back, or register a new agent from a member's account")
	}
	return nil
}

// rotate mints a successor for a token inside its rotation window and hands
// the new secret back in a response header. The old token keeps working for
// the grace period so concurrent processes can pick up the new one.
func (a *authenticator) rotate(ctx context.Context, w http.ResponseWriter, tok domain.Token, now time.Time) {
	secret, err := a.svc.rotateToken(ctx, tok, now)
	if err != nil {
		a.logger.WarnContext(ctx, "rotate token", "token", tok.Prefix, "error", err)
		return
	}
	if secret != "" {
		w.Header().Set(apitypes.HeaderToken, secret)
	}
}

func (a *authenticator) reuseDetected(ctx context.Context, tok domain.Token, now time.Time) {
	if err := a.svc.tokenReuse(ctx, tok, now); err != nil {
		a.logger.WarnContext(ctx, "record token reuse", "token", tok.Prefix, "error", err)
	}
}

// dueAt reports whether key was last recorded a touchInterval or more ago,
// and if so records now. The caller holds the authenticator mu.
func dueAt[K comparable](last map[K]time.Time, key K, now time.Time) bool {
	at, seen := last[key]
	due := !seen || now.Sub(at) >= touchInterval
	if due {
		last[key] = now
	}
	return due
}

// sweep drops the tokens and chats with no request for forgetAfter, at most
// once per sweepInterval. The caller holds the authenticator mu.
func (a *authenticator) sweep(now time.Time) {
	if now.Sub(a.swept) < sweepInterval {
		return
	}
	a.swept = now
	for id, at := range a.touched {
		if now.Sub(at) >= forgetAfter {
			delete(a.touched, id)
		}
	}
	for key, at := range a.seen {
		if now.Sub(at) >= forgetAfter {
			delete(a.seen, key)
			delete(a.named, key)
		}
	}
}

// touch records token use at most once per touchInterval.
func (a *authenticator) touch(ctx context.Context, id domain.ID, now time.Time) {
	a.mu.Lock()
	a.sweep(now)
	due := dueAt(a.touched, id, now)
	a.mu.Unlock()
	if !due {
		return
	}
	if err := a.svc.store.TouchToken(ctx, id, now); err != nil {
		a.logger.WarnContext(ctx, "touch token", "token_id", id, "error", err)
	}
}

// touchSession records that a chat used the agent token at most once per
// touchInterval, so a busy chat does not reach the store on every request.
// The name its client sent, when it sent one, is stored with the touch, and
// at once when it differs from the last one sent, so a renamed chat reads
// by its new name from its next request. The last name sent wins: one set
// through PUT /me/session-name holds until the client sends another. A chat
// not in the memory may be new to the store too, so the agent's chats are
// trimmed to chatsKept after its touch.
func (a *authenticator) touchSession(ctx context.Context, agent domain.ID, label, name string, now time.Time) {
	key := seenSession{agent, label}
	a.mu.Lock()
	a.sweep(now)
	_, known := a.seen[key]
	due := dueAt(a.seen, key, now)
	renamed := name != "" && a.named[key] != name
	if name == "" {
		delete(a.named, key)
	} else {
		a.named[key] = name
	}
	a.mu.Unlock()
	if due {
		a.storeTouch(ctx, agent, label, known, now)
	}
	if name == "" || !due && !renamed {
		return
	}
	if err := a.svc.store.SetAgentSessionName(ctx, agent, label, name, now); err != nil {
		a.logger.WarnContext(ctx, "name agent session", "agent", agent, "error", err)
		a.mu.Lock()
		if a.named[key] == name {
			delete(a.named, key)
		}
		a.mu.Unlock()
	}
}

// keepSeen is touchSession for a request that stays open, such as an event
// stream, and so has no name to send. It leaves the remembered name alone.
func (a *authenticator) keepSeen(ctx context.Context, agent domain.ID, label string, now time.Time) {
	key := seenSession{agent, label}
	a.mu.Lock()
	a.sweep(now)
	_, known := a.seen[key]
	due := dueAt(a.seen, key, now)
	a.mu.Unlock()
	if due {
		a.storeTouch(ctx, agent, label, known, now)
	}
}

// storeTouch writes the touch of a chat. A chat not in the memory may be new
// to the store too, so the agent's chats are trimmed to chatsKept after it.
func (a *authenticator) storeTouch(ctx context.Context, agent domain.ID, label string, known bool, now time.Time) {
	if err := a.svc.store.TouchAgentSession(ctx, agent, label, now); err != nil {
		a.logger.WarnContext(ctx, "touch agent session", "agent", agent, "error", err)
	} else if !known {
		if err := a.svc.store.TrimAgentSessions(ctx, agent, chatsKept); err != nil {
			a.logger.WarnContext(ctx, "trim agent sessions", "agent", agent, "error", err)
		}
	}
}

// sentSessionName reads the chat name a client sent: percent-encoded in the
// header, cleaned like any other. A value that does not decode is no name.
func sentSessionName(r *http.Request) string {
	raw, err := url.PathUnescape(r.Header.Get(apitypes.HeaderSessionName))
	if err != nil {
		return ""
	}
	return domain.CleanSessionName(raw)
}

// renewsClaims reports whether a request counts as the caller working. The
// event stream does not, and neither does any request a background listener
// marks as its own: a listener left running in a closed chat would otherwise
// keep that chat's claims alive.
func renewsClaims(r *http.Request) bool {
	return r.URL.Path != apiPrefix+"/events/stream" && r.Header.Get(apitypes.HeaderListener) == ""
}

// decorate renews the caller's claims and, when the request carries a
// cursor, answers with the unread count. Failures are logged and never block
// the request.
func (a *authenticator) decorate(ctx context.Context, w http.ResponseWriter, r *http.Request, p Principal) {
	now := a.svc.now()
	st := a.svc.store
	if renewsClaims(r) {
		renewed, err := st.RenewClaims(ctx, p.OrgID, p.Actor, p.Session, now)
		if err != nil {
			a.logger.WarnContext(ctx, "renew claims", "actor", p.Actor.ID, "error", err)
		}
		for _, c := range renewed {
			a.logger.DebugContext(ctx, "lease renewed", "card", c.CardID, "actor", p.Actor.ID, "expires_at", c.ExpiresAt)
		}
	}
	if p.IsAgent() && p.Session != "" {
		a.touchSession(ctx, p.Actor.ID, domain.BaseSession(p.Session), sentSessionName(r), now)
	}
	raw := r.Header.Get(apitypes.HeaderCursor)
	if raw == "" {
		return
	}
	after, err := parseEventCursor(apitypes.HeaderCursor, raw)
	if err != nil {
		a.logger.DebugContext(ctx, "unread header skipped", "cursor", raw, "error", err)
		return
	}
	unread, err := a.svc.unread(ctx, p, after)
	if err != nil {
		a.logger.WarnContext(ctx, "count unread", "error", err)
		return
	}
	w.Header().Set(apitypes.HeaderUnread, strconv.Itoa(unread))
}

func writeProblem(w http.ResponseWriter, p *problemError) {
	maps.Copy(w.Header(), p.GetHeaders())
	w.Header().Set("Content-Type", problemContent)
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// ParseActorKind is the path form of an actor kind.
func parseActorPath(kind, id string) (domain.ActorRef, error) {
	k, err := domain.ParseActorKind(kind)
	if err != nil {
		return domain.ActorRef{}, invalidField("kind", "must be user or agent")
	}
	aid, err := domain.ParseID(id)
	if err != nil {
		return domain.ActorRef{}, invalidField("id", "must be a numeric id")
	}
	return domain.ActorRef{Kind: k, ID: aid}, nil
}
