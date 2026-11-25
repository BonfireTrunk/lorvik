package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/store/sqlcgen"
)

// Conversions between the generated row types and the domain. Ids are
// int64 on both sides; enums are int16 columns and typed on the domain side.

func ids(in []domain.ID) []int64 {
	out := make([]int64, len(in))
	for i, id := range in {
		out[i] = int64(id)
	}
	return out
}

func idPtr(p *domain.ID) *int64 {
	if p == nil {
		return nil
	}
	v := int64(*p)
	return &v
}

// optText maps an optional text column: "" is stored as NULL.
func optText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func domainIDPtr(p *int64) *domain.ID {
	if p == nil {
		return nil
	}
	v := domain.ID(*p)
	return &v
}

// actorCols splits an optional actor into its two nullable columns.
func actorCols(a *domain.ActorRef) (*int16, *int64) {
	if a == nil || a.IsZero() {
		return nil, nil
	}
	k, id := int16(a.Kind), int64(a.ID)
	return &k, &id
}

func actorFromCols(kind *int16, id *int64) *domain.ActorRef {
	if kind == nil || id == nil {
		return nil
	}
	return &domain.ActorRef{Kind: domain.ActorKind(*kind), ID: domain.ID(*id)}
}

func actor(kind int16, id int64) domain.ActorRef {
	return domain.ActorRef{Kind: domain.ActorKind(kind), ID: domain.ID(id)}
}

// getMany backs the batch loaders: it returns the rows among in that exist,
// and a non-nil empty slice for empty input without querying.
func getMany[R, D any](ctx context.Context, in []domain.ID, op string,
	load func(context.Context, []int64) ([]R, error), conv func(R) D) ([]D, error) {
	if len(in) == 0 {
		return []D{}, nil
	}
	rows, err := load(ctx, ids(in))
	if err != nil {
		return nil, wrap(op, err)
	}
	return mapRows(rows, conv), nil
}

func mapRows[R, D any](rows []R, f func(R) D) []D {
	out := make([]D, len(rows))
	for i, r := range rows {
		out[i] = f(r)
	}
	return out
}

func orgFromRow(r sqlcgen.Org) (domain.Org, error) {
	o := domain.Org{
		ID: domain.ID(r.ID), Name: r.Name, Status: domain.RecordStatus(r.Status),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if len(r.Settings) > 0 {
		if err := json.Unmarshal(r.Settings, &o.Settings); err != nil {
			return domain.Org{}, fmt.Errorf("decode org %d settings: %w", r.ID, err)
		}
	}
	return o, nil
}

func userFromRow(r sqlcgen.User) domain.User {
	return domain.User{
		ID: domain.ID(r.ID), Login: r.Login, Name: r.Name, Email: r.Email, AvatarURL: r.AvatarUrl,
		Locale: deref(r.Locale), Timezone: deref(r.Timezone), Status: domain.RecordStatus(r.Status),
		AgentJoin: domain.AgentJoin(r.AgentJoin), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func identityFromRow(r sqlcgen.Identity) domain.Identity {
	return domain.Identity{
		ID: domain.ID(r.ID), UserID: domain.ID(r.UserID), Provider: domain.Provider(r.Provider),
		ProviderID: r.ProviderID, Secret: r.Secret, Login: r.Login, LastLoginAt: r.LastLoginAt, CreatedAt: r.CreatedAt,
	}
}

func membershipFromRow(r sqlcgen.Membership) domain.Membership {
	return domain.Membership{
		OrgID: domain.ID(r.OrgID), Actor: actor(r.ActorKind, r.ActorID), Role: domain.Role(r.Role),
		Status: domain.RecordStatus(r.Status), Rights: domain.Rights(r.Rights), GrantedBy: domainIDPtr(r.GrantedByUserID),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func projectMemberFromRow(r sqlcgen.ProjectMember) domain.ProjectMember {
	return domain.ProjectMember{
		ProjectID: domain.ID(r.ProjectID), Actor: actor(r.ActorKind, r.ActorID), Role: domain.Role(r.Role),
		Status: domain.RecordStatus(r.Status), Rights: domain.Rights(r.Rights), GrantedBy: domainIDPtr(r.GrantedByUserID),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func agentJoinRuleFromRow(r sqlcgen.AgentJoinRule) domain.AgentJoinRule {
	return domain.AgentJoinRule{
		ProjectID: domain.ID(r.ProjectID), UserID: domain.ID(r.UserID), AgentJoin: domain.AgentJoin(r.AgentJoin), UpdatedAt: r.UpdatedAt,
	}
}

func agentSessionFromRow(r sqlcgen.AgentSession) domain.AgentSession {
	return domain.AgentSession{AgentID: domain.ID(r.AgentID), Session: r.Session, Name: deref(r.Name), CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt}
}

func agentSessionGrantFromRow(r sqlcgen.AgentSessionGrant) domain.AgentSessionGrant {
	return domain.AgentSessionGrant{
		ProjectID: domain.ID(r.ProjectID), AgentID: domain.ID(r.AgentID), Session: r.Session, Rights: domain.Rights(r.Rights),
		GrantedBy: domain.ID(r.GrantedByUserID), EndsAt: r.EndsAt, Instructions: deref(r.Instructions),
		Status: domain.RecordStatus(r.Status), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func agentFromRow(r sqlcgen.Agent) domain.Agent {
	return domain.Agent{
		ID: domain.ID(r.ID), OrgID: domain.ID(r.OrgID), Name: r.Name, Harness: domain.Harness(r.Harness),
		Machine: r.Machine, CreatedBy: domain.ID(r.CreatedByUserID), Status: domain.RecordStatus(r.Status),
		AgentJoin: domain.AgentJoin(r.AgentJoin), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func tokenFromRow(r sqlcgen.Token) domain.Token {
	return domain.Token{
		ID: domain.ID(r.ID), OrgID: domain.ID(r.OrgID), Actor: actor(r.ActorKind, r.ActorID),
		Name: r.Name, Prefix: r.Prefix, Hash: r.Hash, Scope: domain.TokenScope(r.Scope),
		Status: domain.RecordStatus(r.Status), ExpiresAt: r.ExpiresAt, ReplacedBy: domainIDPtr(r.ReplacedBy),
		LastUsedAt: r.LastUsedAt, CreatedAt: r.CreatedAt,
	}
}

func projectFromRow(r sqlcgen.Project) domain.Project {
	return domain.Project{
		ID: domain.ID(r.ID), OrgID: domain.ID(r.OrgID), Slug: r.Slug, Name: r.Name, Description: r.Description,
		Visibility: domain.Visibility(r.Visibility), Concurrency: domain.Concurrency(r.Concurrency), Scope: domain.ProjectScope(r.Scope), Lease: time.Duration(r.LeaseSeconds) * time.Second,
		NextCardNumber: r.NextCardNumber,
		CreatedBy:      actor(r.CreatedByKind, r.CreatedByID), Status: domain.RecordStatus(r.Status),
		AgentJoin: domain.AgentJoin(r.AgentJoin), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func cardFromRow(r sqlcgen.Card) domain.Card {
	c := domain.Card{
		ID: domain.ID(r.ID), OrgID: domain.ID(r.OrgID), ProjectID: domain.ID(r.ProjectID), Number: r.Number,
		ParentID: domainIDPtr(r.ParentID), Kind: domain.CardKind(r.Kind), Status: domain.Status(r.Status),
		Priority: domain.Priority(r.Priority), Title: r.Title, Body: r.Body,
		Assignee: actorFromCols(r.AssigneeKind, r.AssigneeID), CreatedBy: actor(r.CreatedByKind, r.CreatedByID),
		ClosedAt: r.ClosedAt, Version: r.Version, PageBytes: int(r.PageBytes), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.DropReason != nil {
		c.DropReason = domain.DropReason(*r.DropReason)
	}
	c.Branch, c.Lane, c.ClosingNote = deref(r.Branch), deref(r.Lane), deref(r.ClosingNote)
	if r.BehalfUserID != nil {
		c.Behalf = &domain.Behalf{For: domain.ID(*r.BehalfUserID), Reason: deref(r.BehalfReason), Session: deref(r.BehalfSession), KeptAt: r.BehalfKeptAt}
		if r.BehalfAgentID != nil {
			c.Behalf.By = domain.ID(*r.BehalfAgentID)
		}
	}
	if c.Kind == domain.KindQuestion {
		q := &domain.Question{Choices: r.QuestionChoices, Relayed: r.AnswerRelayed, ForPerson: r.ForPerson, AnsweredBy: actorFromCols(r.AnsweredByKind, r.AnsweredByID), AnsweredAt: r.AnsweredAt}
		if q.Choices == nil {
			q.Choices = []string{}
		}
		if r.Answer != nil {
			q.Answer = *r.Answer
		}
		c.Question = q
	}
	if c.Kind == domain.KindDecision {
		if by := actorFromCols(r.AnsweredByKind, r.AnsweredByID); by != nil && r.AnsweredAt != nil {
			c.Acceptance = &domain.Acceptance{By: *by, At: *r.AnsweredAt}
		}
	}
	if by := actorFromCols(r.FoldedByKind, r.FoldedByID); by != nil && r.FoldedAt != nil {
		c.Fold = &domain.Fold{Into: deref(r.FoldedInto), At: *r.FoldedAt, By: *by}
	}
	return c
}

// cardCols flattens the parts of a card that are stored as nullable columns.
type cardCols struct {
	relayed        bool
	forPerson      bool
	dropReason     *int16
	answeredByKind *int16
	answeredByID   *int64
	answeredAt     *time.Time
	choices        []string
	answer         *string
	branch         *string
	lane           *string
	foldedInto     *string
	foldedAt       *time.Time
	foldedByKind   *int16
	foldedByID     *int64
	behalfUserID   *int64
	behalfAgentID  *int64
	behalfKeptAt   *time.Time
	behalfReason   *string
	behalfSession  *string
}

func cardToCols(c domain.Card) cardCols {
	var out cardCols
	if c.DropReason != domain.DropNone {
		v := int16(c.DropReason)
		out.dropReason = &v
	}
	out.choices = []string{}
	out.branch, out.lane = optText(c.Branch), optText(c.Lane)
	if b := c.Behalf; b != nil {
		out.behalfUserID, out.behalfAgentID, out.behalfKeptAt = new(int64(b.For)), new(int64(b.By)), b.KeptAt
		out.behalfReason, out.behalfSession = optText(b.Reason), optText(b.Session)
	}
	if c.Acceptance != nil {
		out.answeredByKind, out.answeredByID = actorCols(&c.Acceptance.By)
		at := c.Acceptance.At
		out.answeredAt = &at
	}
	if c.Fold != nil {
		into, at := c.Fold.Into, c.Fold.At
		out.foldedInto, out.foldedAt = &into, &at
		out.foldedByKind, out.foldedByID = actorCols(&c.Fold.By)
	}
	if c.Question != nil {
		if c.Question.Choices != nil {
			out.choices = c.Question.Choices
		}
		out.answeredByKind, out.answeredByID = actorCols(c.Question.AnsweredBy)
		out.answeredAt = c.Question.AnsweredAt
		out.relayed, out.forPerson = c.Question.Relayed, c.Question.ForPerson
		if c.Question.Answer != "" {
			a := c.Question.Answer
			out.answer = &a
		}
	}
	return out
}

func claimFromRow(r sqlcgen.Claim) domain.Claim {
	c := domain.Claim{
		CardID: domain.ID(r.CardID), OrgID: domain.ID(r.OrgID), ProjectID: domain.ID(r.ProjectID),
		Actor: actor(r.ActorKind, r.ActorID), Session: r.Session, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt,
	}
	if r.LeaseSeconds != nil {
		c.Lease = time.Duration(*r.LeaseSeconds) * time.Second
	}
	return c
}

// statedLease is the column form of a claim's own lease: null when the
// holder stated none.
func statedLease(d time.Duration) *int32 {
	if d == 0 {
		return nil
	}
	return new(int32(d / time.Second))
}

func lockFromRow(r sqlcgen.Lock) domain.Lock {
	return domain.Lock{
		ID: domain.ID(r.ID), OrgID: domain.ID(r.OrgID), Name: r.Name, Holder: actor(r.HolderKind, r.HolderID),
		Session: r.Session, Purpose: r.Purpose, CardID: domainIDPtr(r.CardID), AcquiredAt: r.AcquiredAt, ExpiresAt: r.ExpiresAt,
	}
}

func cardLinkFromRow(r sqlcgen.CardLink) domain.CardLink {
	return domain.CardLink{
		FromCardID: domain.ID(r.FromCardID), ToCardID: domain.ID(r.ToCardID), Kind: domain.CardLinkKind(r.Kind),
		CreatedBy: actor(r.CreatedByKind, r.CreatedByID), CreatedAt: r.CreatedAt,
	}
}

func commentFromRow(r sqlcgen.Comment) domain.Comment {
	return domain.Comment{ID: domain.ID(r.ID), CardID: domain.ID(r.CardID), Actor: actor(r.ActorKind, r.ActorID), Body: r.Body}
}

func replyFromRow(r sqlcgen.ListRepliesRow) domain.Reply {
	return domain.Reply{
		Comment: domain.Comment{ID: domain.ID(r.ID), CardID: domain.ID(r.CardID), Actor: actor(r.ActorKind, r.ActorID), Body: r.Body},
		Unseen:  int(r.Unseen),
	}
}

func noteFromRow(r sqlcgen.Note) domain.Note {
	return domain.Note{
		ID: domain.ID(r.ID), OrgID: domain.ID(r.OrgID), ProjectID: domain.ID(r.ProjectID),
		Status: domain.RecordStatus(r.Status), Version: r.Version,
		CreatedBy: actor(r.CreatedByKind, r.CreatedByID), UpdatedBy: actor(r.UpdatedByKind, r.UpdatedByID),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Slug: r.Slug, Title: r.Title, Body: r.Body, PageBytes: int(r.PageBytes),
	}
}

func noteRevisionFromRow(r sqlcgen.GetNoteRevisionRow) domain.NoteRevision {
	return domain.NoteRevision{
		ID: domain.ID(r.ID), NoteID: domain.ID(r.NoteID), Version: r.Version,
		UpdatedBy: actor(r.UpdatedByKind, r.UpdatedByID), Title: r.Title, Body: r.Body, Page: r.Page,
	}
}

func cardRevisionFromRow(r sqlcgen.GetCardRevisionRow) domain.CardRevision {
	return domain.CardRevision{
		ID: domain.ID(r.ID), CardID: domain.ID(r.CardID), Version: r.Version,
		UpdatedBy: actor(r.UpdatedByKind, r.UpdatedByID), CreatedAt: r.CreatedAt, Title: r.Title, Body: r.Body, Page: r.Page,
	}
}

func refFromRow(r sqlcgen.Ref) domain.RefLink {
	return domain.RefLink{
		SourceType: domain.SubjectType(r.SourceType), SourceID: domain.ID(r.SourceID),
		TargetType: domain.SubjectType(r.TargetType), TargetID: domain.ID(r.TargetID),
	}
}

// eventRow is the column set every event query returns (xid is internal).
type eventRow struct {
	OrgID         int64  `db:"org_id"`
	ID            int64  `db:"id"`
	ProjectID     *int64 `db:"project_id"`
	Type          int16  `db:"type"`
	ActorKind     int16  `db:"actor_kind"`
	ActorID       int64  `db:"actor_id"`
	ActorSession  string `db:"actor_session"`
	SubjectType   int16  `db:"subject_type"`
	SubjectID     int64  `db:"subject_id"`
	SubjectNumber int64  `db:"subject_number"`
	Payload       []byte `db:"payload"`
	Xid           string `db:"xid"`
}

// parseXid reads an xid8 that Postgres rendered as text. The text is always
// an unsigned 64-bit decimal, so there is no error to return.
func parseXid(s string) uint64 {
	x, _ := strconv.ParseUint(s, 10, 64)
	return x
}

func eventFromRow(r eventRow) domain.Event {
	e := domain.Event{
		OrgID: domain.ID(r.OrgID), ID: domain.ID(r.ID), Xid: parseXid(r.Xid), ProjectID: domainIDPtr(r.ProjectID),
		Type: domain.EventType(r.Type), Actor: actor(r.ActorKind, r.ActorID), Session: r.ActorSession,
		Subject: domain.Subject{Type: domain.SubjectType(r.SubjectType), ID: domain.ID(r.SubjectID), Number: r.SubjectNumber},
	}
	if len(r.Payload) > 0 {
		e.Payload = json.RawMessage(r.Payload)
	}
	return e
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
