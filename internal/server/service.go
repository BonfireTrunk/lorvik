package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// Service implements the API semantics on top of a Store. Handlers parse
// requests and call it; it owns transactions, events, permission checks and
// error meaning.
type Service struct {
	store    Store
	ids      *domain.IDGen
	now      func() time.Time
	logger   *slog.Logger
	hub      *hub
	password bool
	// github is nil when GitHub sign-in is not configured.
	github *githubClient
	links  linkStates
	limits *signInLimits
	// aside runs work the request must not wait for. Tests run it inline.
	aside func(func())
	// chats records that a chat is in use; the API's authenticator sets it.
	chats chatSeen
}

// NewService builds a Service. now may be nil for time.Now; ids may be nil
// for a fresh generator with a random node id.
func NewService(store Store, ids *domain.IDGen, now func() time.Time, logger *slog.Logger) *Service {
	if now == nil {
		now = time.Now
	}
	if ids == nil {
		ids, _ = domain.NewIDGen(-1, now) // a random node never fails
	}
	return &Service{store: store, ids: ids, now: now, logger: logger, hub: newHub(), limits: newSignInLimits(now, logger),
		aside: func(f func()) { go f() }}
}

func (s *Service) principal(ctx context.Context) (Principal, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return Principal{}, fmt.Errorf("%w: no credentials on request", domain.ErrUnauthorized)
	}
	if p.Scope == domain.ScopeSignIn {
		return Principal{}, signInOnly()
	}
	return p, nil
}

// lookup returns a converter over st that renders claims from p's point of view.
func (s *Service) lookup(st Store, p Principal) *lookup {
	return newLookup(st, p, s.now())
}

// userPrincipal is principal restricted to human callers.
func (s *Service) userPrincipal(ctx context.Context) (Principal, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return Principal{}, err
	}
	if err := requireHuman(p); err != nil {
		return Principal{}, err
	}
	return p, nil
}

// requireHuman refuses agent callers: answering, accepting, rejecting and
// revoking are judgments for people.
func requireHuman(p Principal) error {
	if p.IsAgent() {
		return withHint(fmt.Errorf("%w: this action is for users, not agents", domain.ErrForbidden),
			"ask a human to do this, or file a question card with `lorvik ask`")
	}
	return nil
}

// loadCard resolves a card by project slug and number with the caller's access.
func (s *Service) loadCard(ctx context.Context, st Store, p Principal, slug string, number int64) (domain.Project, access, domain.Card, error) {
	proj, a, err := s.project(ctx, st, p, slug)
	if err != nil {
		return domain.Project{}, access{}, domain.Card{}, err
	}
	c, err := st.GetCardByNumber(ctx, proj.ID, number)
	if err != nil {
		return domain.Project{}, access{}, domain.Card{}, fmt.Errorf("card #%d: %w", number, err)
	}
	return proj, a, c, nil
}

// refCard resolves a card number mentioned in a request body; a miss is a
// field error rather than a 404 for the URL.
func refCard(ctx context.Context, st Store, projectID domain.ID, number int64, field string) (domain.Card, error) {
	c, err := st.GetCardByNumber(ctx, projectID, number)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Card{}, invalidField(field, fmt.Sprintf("no card #%d in this project", number))
	}
	if err != nil {
		return domain.Card{}, fmt.Errorf("card #%d: %w", number, err)
	}
	return c, nil
}

func versionCheck(have, want int64, what string) error {
	if have != want {
		return versionConflict(have, fmt.Sprintf("%s is at version %d, you sent %d", what, have, want))
	}
	return nil
}

// updateCard writes c and, on a lost race, reports the version that won.
func updateCard(ctx context.Context, tx Store, c domain.Card, expected int64) (domain.Card, error) {
	out, err := tx.UpdateCard(ctx, c, expected)
	if errors.Is(err, domain.ErrVersionConflict) {
		if cur, gerr := tx.GetCard(ctx, c.ID); gerr == nil {
			return domain.Card{}, versionConflict(cur.Version, fmt.Sprintf("card #%d changed while you were editing", c.Number))
		}
	}
	if err != nil {
		return domain.Card{}, fmt.Errorf("update card #%d: %w", c.Number, err)
	}
	return out, nil
}

func (s *Service) updateNote(ctx context.Context, tx Store, n domain.Note, expected int64, pageChanged bool) (domain.Note, error) {
	out, err := tx.UpdateNote(ctx, n, expected, s.ids.New(), pageChanged)
	if errors.Is(err, domain.ErrVersionConflict) {
		if cur, gerr := tx.GetNote(ctx, n.ID); gerr == nil {
			return domain.Note{}, versionConflict(cur.Version, fmt.Sprintf("note %q changed while you were editing", n.Slug))
		}
	}
	if err != nil {
		return domain.Note{}, fmt.Errorf("update note %q: %w", n.Slug, err)
	}
	return out, nil
}

// emit appends an event of the org, in no project and no session, inside
// the caller's transaction and wakes streams for the org once the
// transaction is the caller's to commit.
func (s *Service) emit(ctx context.Context, tx Store, actor domain.ActorRef, orgID domain.ID, typ domain.EventType, subj domain.Subject, payload map[string]any) error {
	return s.emitSession(ctx, tx, actor, "", orgID, nil, typ, subj, payload)
}

func (s *Service) emitSession(ctx context.Context, tx Store, actor domain.ActorRef, session string, orgID domain.ID, projectID *domain.ID, typ domain.EventType, subj domain.Subject, payload map[string]any) error {
	ev, err := domain.NewEvent(s.ids.New(), orgID, projectID, typ, actor, subj, payload)
	if err != nil {
		return fmt.Errorf("build %s event: %w", typ, err)
	}
	ev.Session = session
	if err := tx.AppendEvent(ctx, ev); err != nil {
		return fmt.Errorf("append %s event: %w", typ, err)
	}
	s.hub.wake(orgID)
	return nil
}

// emitP is emit for the common case: the principal is the actor.
func (s *Service) emitP(ctx context.Context, tx Store, p Principal, projectID *domain.ID, typ domain.EventType, subj domain.Subject, payload map[string]any) error {
	return s.emitSession(ctx, tx, p.Actor, p.Session, p.OrgID, projectID, typ, subj, payload)
}

func cardSubject(c domain.Card) domain.Subject {
	return domain.Subject{Type: domain.SubjectCard, ID: c.ID, Number: c.Number}
}

func noteSubject(n domain.Note) domain.Subject {
	return domain.Subject{Type: domain.SubjectNote, ID: n.ID}
}

func projectSubject(id domain.ID) domain.Subject {
	return domain.Subject{Type: domain.SubjectProject, ID: id}
}

// membershipSubject names a membership by the member's actor id.
func membershipSubject(actorID domain.ID) domain.Subject {
	return domain.Subject{Type: domain.SubjectMembership, ID: actorID}
}

func agentSubject(id domain.ID) domain.Subject {
	return domain.Subject{Type: domain.SubjectAgent, ID: id}
}

func tokenSubject(id domain.ID) domain.Subject {
	return domain.Subject{Type: domain.SubjectToken, ID: id}
}

func lockSubject(l domain.Lock) domain.Subject {
	return domain.Subject{Type: domain.SubjectLock, ID: l.ID}
}

// emitStatusChanged records a card's move from one status to the one it now
// has. Every such event carries from, to and kind; kv adds the move's cause.
func (s *Service) emitStatusChanged(ctx context.Context, tx Store, p Principal, c domain.Card, from domain.Status, kv ...any) error {
	return s.emitStatusChangedIn(ctx, tx, p, c.ProjectID, c, from, kv...)
}

// emitStatusChangedIn is emitStatusChanged recorded in projectID, for a move
// whose cause is a card of another project (see sharedProject).
func (s *Service) emitStatusChangedIn(ctx context.Context, tx Store, p Principal, projectID domain.ID, c domain.Card, from domain.Status, kv ...any) error {
	kv = append([]any{"from", from.String(), "to", c.Status.String(), "kind", c.Kind.String()}, kv...)
	return s.emitP(ctx, tx, p, &projectID, domain.EventCardStatusChanged, cardSubject(c), payload(c.Title, kv...))
}

// sharedProject is the project an event about subject that names other is
// recorded in. Cards of two projects meet only when one is the global
// project, and every reader of a project can read the global one; so the
// event goes to the other project when the subject is global, and stays in
// the subject's project otherwise. Recording it in the global project would
// show a private card's title to every reader of the global project.
func (s *Service) sharedProject(ctx context.Context, tx Store, subject, other domain.Card) (domain.ID, error) {
	if subject.ProjectID == other.ProjectID {
		return subject.ProjectID, nil
	}
	proj, err := tx.GetProject(ctx, subject.ProjectID)
	if err != nil {
		return 0, fmt.Errorf("load project of #%d: %w", subject.Number, err)
	}
	if proj.Scope == domain.ProjectScopeGlobal {
		return other.ProjectID, nil
	}
	return subject.ProjectID, nil
}

// addLink validates and stores "from kind to", made by the principal now.
func (s *Service) addLink(ctx context.Context, tx Store, p Principal, from, to domain.Card, kind domain.CardLinkKind) error {
	link := domain.CardLink{FromCardID: from.ID, ToCardID: to.ID, Kind: kind, CreatedBy: p.Actor, CreatedAt: s.now()}
	if err := link.Validate(); err != nil {
		return err
	}
	if err := tx.AddCardLink(ctx, link); err != nil {
		return fmt.Errorf("link #%d %s #%d: %w", from.Number, kind, to.Number, err)
	}
	return nil
}

func releaseClaim(ctx context.Context, tx Store, c domain.Card) error {
	if err := tx.DeleteClaim(ctx, c.ID); err != nil {
		return fmt.Errorf("release claim on #%d: %w", c.Number, err)
	}
	return nil
}

// payload builds an event payload with the subject's title first.
func payload(title string, kv ...any) map[string]any {
	m := map[string]any{"title": title}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}
