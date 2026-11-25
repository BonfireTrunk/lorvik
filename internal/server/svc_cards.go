package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// conflict is a 409 for a structural rule the request runs into.
func conflict(detail, hint string) error {
	return newProblem(http.StatusConflict, detail, hint)
}

// parentRules checks that parent may hold a child of the given kind: two
// levels only, and only an open story is a parent.
func parentRules(childKind domain.CardKind, parent domain.Card) error {
	switch {
	case childKind == domain.KindStory:
		return conflict("a story has no parent", "stories are the top level; set a parent on a task, question or decision")
	case parent.Kind != domain.KindStory:
		return conflict(fmt.Sprintf("#%d is a %s and only a story may be a parent", parent.Number, parent.Kind),
			"group cards under a story: `lorvik add --kind story`, then use its number as the parent")
	case parent.Status.Terminal():
		return conflict(fmt.Sprintf("story #%d is closed", parent.Number),
			"a closed story is closed; start a new story and link it with relates")
	}
	return nil
}

// buildCard turns a create request into a validated domain card. When the
// request supersedes a decision it also returns that decision.
func (s *Service) buildCard(ctx context.Context, tx Store, p Principal, proj domain.Project, in apitypes.CreateCard, id domain.ID) (domain.Card, *domain.Card, error) {
	var errs []error
	kind := domain.KindTask
	if in.Kind != "" {
		k, err := domain.ParseCardKind(in.Kind)
		if err != nil {
			errs = append(errs, invalidField("kind", err.Error()))
		}
		kind = k
	}
	status := domain.StatusBacklog
	if kind == domain.KindQuestion || kind == domain.KindDecision {
		status = domain.StatusReady
	}
	if in.Status != "" {
		st, err := domain.ParseStatus(in.Status)
		switch {
		case err != nil:
			errs = append(errs, invalidField("status", err.Error()))
		case kind == domain.KindDecision && (st == domain.StatusReady || st == domain.StatusDone):
			status = st
		case kind != domain.KindDecision && (st == domain.StatusBacklog || st == domain.StatusReady):
			status = st
		case kind == domain.KindDecision:
			errs = append(errs, invalidField("status", "a new decision is ready (proposed) or done (recorded by a reviewer)"))
		default:
			errs = append(errs, invalidField("status", "new cards start as backlog or ready"))
		}
	}
	priority := domain.PriorityNormal
	if in.Priority != "" {
		pr, err := domain.ParsePriority(in.Priority)
		if err != nil {
			errs = append(errs, invalidField("priority", err.Error()))
		}
		priority = pr
	}
	now := s.now()
	c := domain.Card{ID: id, OrgID: p.OrgID, ProjectID: proj.ID, Kind: kind, Status: status, Title: strings.TrimSpace(in.Title),
		Body: in.Body, PageBytes: len(in.Page), Priority: priority, Lane: strings.TrimSpace(in.Lane), CreatedBy: p.Actor, CreatedAt: now, UpdatedAt: now}
	if kind == domain.KindDecision && status == domain.StatusDone {
		c.Acceptance = &domain.Acceptance{By: p.Actor, At: now}
		c.ClosedAt = &now
	}
	if kind == domain.KindQuestion {
		c.Question = &domain.Question{Choices: in.Choices, ForPerson: in.ForPerson}
	} else {
		if in.ForPerson {
			errs = append(errs, invalidField("for_person", "only question cards are marked for a person"))
		}
		if len(in.Choices) > 0 {
			errs = append(errs, invalidField("choices", "only question cards take choices"))
		}
		if len(in.Blocks) > 0 {
			errs = append(errs, invalidField("blocks", "only question cards can block other cards"))
		}
	}
	var structural error
	if in.Parent != nil {
		parent, err := refCard(ctx, tx, proj.ID, *in.Parent, "parent")
		if err != nil {
			errs = append(errs, err)
		} else if structural = parentRules(kind, parent); structural == nil {
			c.ParentID = &parent.ID
		}
	}
	if in.Assignee != nil {
		ref, err := s.resolveAssignee(ctx, tx, p, proj, *in.Assignee)
		if err != nil {
			errs = append(errs, err)
		} else {
			c.Assignee = ref
		}
	}
	var old *domain.Card
	if in.Supersedes != nil {
		o, err := s.supersedable(ctx, tx, kind, proj, *in.Supersedes)
		if err != nil {
			errs = append(errs, err)
		}
		old = o
	}
	errs = append(errs, c.Validate(), domain.ValidatePage(in.Page))
	if err := errors.Join(errs...); err != nil {
		return domain.Card{}, nil, err
	}
	if structural != nil {
		return domain.Card{}, nil, structural
	}
	return c, old, nil
}

// supersedable resolves the decision a new decision replaces: an accepted
// decision of the same project that has no accepted successor yet.
func (s *Service) supersedable(ctx context.Context, tx Store, kind domain.CardKind, proj domain.Project, number int64) (*domain.Card, error) {
	if kind != domain.KindDecision {
		return nil, invalidField("supersedes", "only a decision supersedes another")
	}
	old, err := refCard(ctx, tx, proj.ID, number, "supersedes")
	if err != nil {
		return nil, err
	}
	if old.Kind != domain.KindDecision || old.Status != domain.StatusDone {
		return nil, invalidField("supersedes", fmt.Sprintf("#%d is not an accepted decision; only an accepted decision is superseded", number))
	}
	links, err := tx.ListCardLinks(ctx, old.ID)
	if err != nil {
		return nil, fmt.Errorf("list links of #%d: %w", old.Number, err)
	}
	for _, l := range links {
		if l.Kind != domain.LinkSupersedes || l.ToCardID != old.ID {
			continue
		}
		succ, err := tx.GetCard(ctx, l.FromCardID)
		if err != nil {
			return nil, fmt.Errorf("load successor of #%d: %w", old.Number, err)
		}
		if succ.Status == domain.StatusDone {
			return nil, invalidField("supersedes", fmt.Sprintf("#%d is already superseded by #%d", old.Number, succ.Number))
		}
	}
	return &old, nil
}

// supersedeOld drops the decision that the accepted decision d replaces. A
// decision that is no longer accepted by now (revoked meanwhile, or
// superseded by a rival) is left as it is.
func (s *Service) supersedeOld(ctx context.Context, tx Store, p Principal, d domain.Card) error {
	links, err := tx.ListCardLinks(ctx, d.ID)
	if err != nil {
		return fmt.Errorf("list links of #%d: %w", d.Number, err)
	}
	for _, l := range links {
		if l.Kind != domain.LinkSupersedes || l.FromCardID != d.ID {
			continue
		}
		old, err := tx.GetCard(ctx, l.ToCardID)
		if err != nil {
			return fmt.Errorf("load superseded decision: %w", err)
		}
		if old.Status != domain.StatusDone {
			continue
		}
		if err := old.Supersede(s.now()); err != nil {
			return err
		}
		updated, err := updateCard(ctx, tx, old, old.Version)
		if err != nil {
			return err
		}
		if err := s.emitStatusChanged(ctx, tx, p, updated, domain.StatusDone,
			"drop_reason", domain.DropSuperseded.String(), "superseded_by", d.Number); err != nil {
			return err
		}
	}
	return nil
}

// CreateCard creates a task, question, story or decision. A question's
// blocks links move the targets to waiting in the same transaction.
func (s *Service) CreateCard(ctx context.Context, slug string, in apitypes.CreateCard, key string) (apitypes.Card, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Card{}, err
	}
	var out domain.Card
	err = s.store.WithTx(ctx, func(tx Store) error {
		proj, a, perr := s.project(ctx, tx, p, slug)
		if perr != nil {
			return perr
		}
		id := s.ids.New()
		first, repeat, kerr := s.claimRequestKey(ctx, tx, p, key, domain.RequestCreateCard, proj, id, id)
		if kerr != nil {
			return kerr
		}
		if repeat {
			made, gerr := tx.GetCard(ctx, first.SubjectID)
			out = made
			return gerr
		}
		perm := domain.PermCardWrite
		switch in.Kind {
		case "question":
			perm = domain.PermQuestionAsk
		case "decision":
			perm = domain.PermDecisionPropose
		}
		if err := a.require(p, perm); err != nil {
			return err
		}
		if in.Kind == "decision" && in.Status == "done" {
			if err := requireHuman(p); err != nil {
				return err
			}
			if err := a.require(p, domain.PermDecisionAccept); err != nil {
				return err
			}
		}
		c, old, berr := s.buildCard(ctx, tx, p, proj, in, id)
		if berr != nil {
			return berr
		}
		if rerr := requireCreateReady(ctx, tx, p, a, c); rerr != nil {
			return rerr
		}
		// Resolve blocked targets before writing anything.
		targets := make([]domain.Card, 0, len(in.Blocks))
		for _, n := range in.Blocks {
			t, rerr := refCard(ctx, tx, proj.ID, n, "blocks")
			if rerr != nil {
				return rerr
			}
			targets = append(targets, t)
		}
		created, cerr := tx.CreateCard(ctx, c)
		if cerr != nil {
			return fmt.Errorf("create card: %w", cerr)
		}
		out = created
		if in.Page != "" {
			if perr := saveCardPage(ctx, tx, out.ID, in.Page); perr != nil {
				return perr
			}
		}
		if rerr := s.syncRefs(ctx, tx, p.OrgID, &out.ProjectID, domain.SubjectCard, out.ID, out.Body); rerr != nil {
			return rerr
		}
		pl := payload(out.Title, "kind", out.Kind.String(), "status", out.Status.String(), "priority", out.Priority.String())
		if old != nil {
			pl["supersedes"] = old.Number
		}
		if out.ForPerson() {
			pl["for_person"] = true
		}
		if eerr := s.emitP(ctx, tx, p, &proj.ID, domain.EventCardCreated, cardSubject(out), pl); eerr != nil {
			return eerr
		}
		if old != nil {
			if lerr := s.addLink(ctx, tx, p, out, *old, domain.LinkSupersedes); lerr != nil {
				return lerr
			}
			if out.Status == domain.StatusDone {
				if serr := s.supersedeOld(ctx, tx, p, out); serr != nil {
					return serr
				}
			}
		}
		for _, t := range targets {
			if berr := s.blockCard(ctx, tx, p, a, out, t); berr != nil {
				return berr
			}
		}
		return s.markSeen(ctx, tx, p, out)
	})
	if err != nil {
		return apitypes.Card{}, err
	}
	return s.lookup(s.store, p).card(ctx, out)
}

// requireCreateReady holds an agent that files work straight into ready to
// the ready right, as moving it there from backlog is. A child of a story past
// backlog needs none: readying the story authorized its work.
func requireCreateReady(ctx context.Context, tx Store, p Principal, a access, c domain.Card) error {
	if !p.IsAgent() || c.Status != domain.StatusReady || c.Kind == domain.KindQuestion || c.Kind == domain.KindDecision {
		return nil
	}
	if c.ParentID != nil {
		story, err := tx.GetCard(ctx, *c.ParentID)
		if err != nil {
			return fmt.Errorf("load parent story: %w", err)
		}
		if story.Status != domain.StatusBacklog {
			return nil
		}
	}
	if a.can(domain.PermCardReady) {
		return nil
	}
	return p.lacksRight(domain.RightReady, a.slug, "file it in backlog for a person to ready")
}

// blockCard records "blocker blocks target" and parks an active target in
// waiting, releasing its claim. A question also parks a backlog target: the
// answer is what the card waited for, so answering readies it. An agent
// without the ready right in the target's project (ta) leaves a backlog
// target in backlog, since the answer or an unlink would ready it.
func (s *Service) blockCard(ctx context.Context, tx Store, p Principal, ta access, blocker, target domain.Card) error {
	if err := s.addLink(ctx, tx, p, blocker, target, domain.LinkBlocks); err != nil {
		return err
	}
	if !target.ParkedBy(blocker.Kind) {
		return nil
	}
	from := target.Status
	if from == domain.StatusBacklog {
		if p.IsAgent() && !ta.can(domain.PermCardReady) {
			return nil
		}
		if err := target.Transition(domain.StatusReady, domain.DropNone, s.now()); err != nil {
			return err
		}
	}
	if err := target.Transition(domain.StatusWaiting, domain.DropNone, s.now()); err != nil {
		return err
	}
	updated, err := updateCard(ctx, tx, target, target.Version)
	if err != nil {
		return err
	}
	if err := releaseClaim(ctx, tx, updated); err != nil {
		return err
	}
	in, err := s.sharedProject(ctx, tx, updated, blocker)
	if err != nil {
		return err
	}
	return s.emitStatusChangedIn(ctx, tx, p, in, updated, from, "blocked_by", blocker.Number, "blocker_title", blocker.Title)
}

// busyLanesProblem is the 404 for next when cards are ready but every one of
// them waits behind a working card in its lane.
func busyLanesProblem(slug string, waiting []query.LaneCount) *problemError {
	p := newProblem(http.StatusNotFound, fmt.Sprintf("no ready card in %s: %s", slug, domain.ErrNotFound),
		"cards in a busy lane become available when the working card in that lane is done or released")
	for _, w := range waiting {
		p.WaitingCards += w.Cards
		p.BusyLanes = append(p.BusyLanes, w.Lane)
	}
	return p
}

// NextCard returns the best ready card for the project, or a 404.
func (s *Service) NextCard(ctx context.Context, slug string) (apitypes.Card, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Card{}, err
	}
	proj, _, err := s.project(ctx, s.store, p, slug)
	if err != nil {
		return apitypes.Card{}, err
	}
	c, err := s.store.NextReady(ctx, proj.ID, p.Actor, s.now())
	if errors.Is(err, domain.ErrNotFound) {
		if waiting, werr := s.store.CountOnBusyLanes(ctx, proj.ID, p.Actor, s.now()); werr == nil && len(waiting) > 0 {
			return apitypes.Card{}, busyLanesProblem(slug, waiting)
		}
		return apitypes.Card{}, withHint(fmt.Errorf("no ready card in %s: %w", slug, domain.ErrNotFound),
			"nothing to claim; create or groom cards with `lorvik add` and `lorvik ready <n>`")
	}
	if err != nil {
		return apitypes.Card{}, fmt.Errorf("next ready: %w", err)
	}
	return s.lookup(s.store, p).card(ctx, c)
}

const cardList = "cards"

// cardFilters fills f from the request's filter set; it is the one place a
// card list's filters are read, for both lists. parentIn is the project the
// parent number belongs to; nil means the list spans projects and a parent
// cannot be named.
func (s *Service) cardFilters(ctx context.Context, p Principal, f *query.CardFilter, in CardFilterQuery, parentIn *domain.Project) error {
	f.Open, f.Query, f.NoStory, f.Lane, f.NoLane, f.Relayed = in.Open, in.Query, in.NoStory, in.Lane, in.NoLane, in.Relayed
	var err error
	if in.NoStory && in.Parent > 0 {
		return invalidField("no_story", "cannot be combined with parent")
	}
	if in.NoLane && in.Lane != "" {
		return invalidField("no_lane", "cannot be combined with lane")
	}
	if f.Created, err = parseRange("created", in.CreatedAfter, in.CreatedBefore); err != nil {
		return err
	}
	if f.Updated, err = parseRange("updated", in.UpdatedAfter, in.UpdatedBefore); err != nil {
		return err
	}
	if f.Closed, err = parseRange("closed", in.ClosedAfter, in.ClosedBefore); err != nil {
		return err
	}
	if in.AnsweredFor != "" {
		id, aerr := parseActorID(p, "answered_for", in.AnsweredFor)
		if aerr != nil {
			return aerr
		}
		f.AwaitsKeepBy = &id
	}
	switch in.Assignee {
	case "":
	case "none":
		f.Unassigned = true
	default:
		id, aerr := parseActorID(p, "assignee", in.Assignee)
		if aerr != nil {
			return aerr
		}
		f.Assignee = &id
	}
	for _, pr := range in.Priority {
		v, perr := domain.ParsePriority(pr)
		if perr != nil {
			return invalidField("priority", "unknown priority "+pr)
		}
		f.Priorities = append(f.Priorities, v)
	}
	if in.Kind != "" {
		if f.Kind, err = domain.ParseCardKind(in.Kind); err != nil {
			return invalidField("kind", err.Error())
		}
	}
	for _, st := range in.Status {
		v, err := domain.ParseStatus(st)
		if err != nil {
			return invalidField("status", "unknown status "+st)
		}
		f.Statuses = append(f.Statuses, v)
	}
	if in.Mine {
		actor := p.Actor
		f.Actor = &actor
	}
	if in.Parent > 0 {
		if parentIn == nil {
			return invalidField("parent", "card numbers are per project; name a project to filter by parent")
		}
		par, perr := refCard(ctx, s.store, parentIn.ID, in.Parent, "parent")
		if perr != nil {
			return perr
		}
		f.ParentID = &par.ID
	}
	return nil
}

// ListCards returns one page of briefs in board order.
func (s *Service) ListCards(ctx context.Context, slug string, in CardFilterQuery, sortName string, desc bool) (apitypes.CardList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.CardList{}, err
	}
	proj, _, err := s.project(ctx, s.store, p, slug)
	if err != nil {
		return apitypes.CardList{}, err
	}
	cur, err := decodeCursor(cardList, in.Cursor)
	if err != nil {
		return apitypes.CardList{}, err
	}
	if !cur.forSort(orderOf(in.Query, sortName), desc) {
		return apitypes.CardList{}, badCursor()
	}
	f := query.CardFilter{Page: query.Page{Limit: in.Limit, Backward: cur.backward()}, Cursor: cur.cardCursor()}
	sort, ok := cardSorts[sortName]
	if !ok {
		return apitypes.CardList{}, invalidField("sort", "want one of created, updated, priority, number")
	}
	if desc && sort == query.SortBoard {
		return apitypes.CardList{}, invalidField("desc", "needs sort")
	}
	if in.Query != "" && sort != query.SortBoard {
		return apitypes.CardList{}, invalidField("sort", "not available with q; matches are ranked by relevance")
	}
	f.Sort, f.Desc = sort, desc
	if err := s.cardFilters(ctx, p, &f, in, &proj); err != nil {
		return apitypes.CardList{}, err
	}
	cards, remaining, err := s.store.ListCards(ctx, proj.ID, f)
	if err != nil {
		return apitypes.CardList{}, fmt.Errorf("list cards: %w", err)
	}
	items, err := s.lookup(s.store, p).briefs(ctx, cards)
	if err != nil {
		return apitypes.CardList{}, err
	}
	out := apitypes.CardList{Items: items, Page: page(cardList, cards, cardKeyFor(sortName, desc), remaining)}
	if in.Query != "" {
		if out.Page, err = s.rankedPage(ctx, cardList, domain.SubjectCard, in.Query, rankedCardEdge(cards), remaining); err != nil {
			return apitypes.CardList{}, err
		}
	}
	return out, nil
}

const (
	allCardList  = "all-cards"
	unfoldedList = "unfolded"
)

// ListAllCards lists cards across the projects the caller can see, newest
// first, with the project slug on every row.
func (s *Service) ListAllCards(ctx context.Context, project string, in CardFilterQuery) (apitypes.CardList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.CardList{}, err
	}
	cur, err := decodeCursor(allCardList, in.Cursor)
	if err != nil {
		return apitypes.CardList{}, err
	}
	if !cur.forSort(orderOf(in.Query, ""), false) {
		return apitypes.CardList{}, badCursor()
	}
	var ids []domain.ID
	var single *domain.Project
	if project == "" || project == "all" {
		visible, _, verr := s.visibleProjects(ctx, s.store, p)
		if verr != nil {
			return apitypes.CardList{}, verr
		}
		for _, pr := range visible {
			if pr.Status.Alive() {
				ids = append(ids, pr.ID)
			}
		}
	} else {
		proj, _, perr := s.project(ctx, s.store, p, project)
		if perr != nil {
			return apitypes.CardList{}, perr
		}
		single, ids = &proj, []domain.ID{proj.ID}
	}
	f := query.CardFilter{Page: query.Page{Limit: in.Limit, Backward: cur.backward()}, Cursor: cur.cardCursor()}
	if err := s.cardFilters(ctx, p, &f, in, single); err != nil {
		return apitypes.CardList{}, err
	}
	cards, remaining, err := s.store.ListCardsIn(ctx, ids, f)
	if err != nil {
		return apitypes.CardList{}, fmt.Errorf("list cards: %w", err)
	}
	lk := s.lookup(s.store, p)
	items, err := lk.briefs(ctx, cards)
	if err != nil {
		return apitypes.CardList{}, err
	}
	for i, c := range cards {
		if items[i].Project, err = lk.projectSlug(ctx, c.ProjectID); err != nil {
			return apitypes.CardList{}, err
		}
	}
	out := apitypes.CardList{Items: items, Page: page(allCardList, cards, func(c domain.Card) cursor { return idKey(c.ID) }, remaining)}
	if in.Query != "" {
		if out.Page, err = s.rankedPage(ctx, allCardList, domain.SubjectCard, in.Query, rankedCardEdge(cards), remaining); err != nil {
			return apitypes.CardList{}, err
		}
	}
	return out, nil
}

// ListUnfolded lists the project's done cards that have no parent and no
// fold mark, newest closed first.
func (s *Service) ListUnfolded(ctx context.Context, slug string, limit int, token string) (apitypes.CardList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.CardList{}, err
	}
	proj, _, err := s.project(ctx, s.store, p, slug)
	if err != nil {
		return apitypes.CardList{}, err
	}
	cur, err := decodeCursor(unfoldedList, token)
	if err != nil {
		return apitypes.CardList{}, err
	}
	var uc *query.UnfoldedCursor
	if cur != nil {
		uc = &query.UnfoldedCursor{ClosedAt: time.Unix(0, cur.ClosedAt).UTC(), ID: domain.ID(cur.ID)}
	}
	cards, remaining, err := s.store.ListUnfolded(ctx, proj.ID, query.Page{Limit: limit, Backward: cur.backward()}, uc)
	if err != nil {
		return apitypes.CardList{}, fmt.Errorf("list unfolded: %w", err)
	}
	items, err := s.lookup(s.store, p).briefs(ctx, cards)
	if err != nil {
		return apitypes.CardList{}, err
	}
	key := func(c domain.Card) cursor {
		k := cursor{ID: int64(c.ID)}
		if c.ClosedAt != nil {
			k.ClosedAt = c.ClosedAt.UnixNano()
		}
		return k
	}
	return apitypes.CardList{Items: items, Page: page(unfoldedList, cards, key, remaining)}, nil
}

// GetCard returns a card with links, children, progress, comment, recent
// events and open blockers.
func (s *Service) GetCard(ctx context.Context, slug string, number int64) (apitypes.Card, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Card{}, err
	}
	_, _, c, err := s.loadCard(ctx, s.store, p, slug, number)
	if err != nil {
		return apitypes.Card{}, err
	}
	return s.cardDetail(ctx, s.store, p, c)
}

// cardDetail renders c for p, who can read c's project. Linked cards,
// blockers and history events from projects p cannot read are left out: a
// global card can be linked to a private one.
func (s *Service) cardDetail(ctx context.Context, st Store, p Principal, c domain.Card) (apitypes.Card, error) {
	lk := s.lookup(st, p)
	out, err := lk.card(ctx, c)
	if err != nil {
		return apitypes.Card{}, err
	}
	v := s.sightOf(st, p)
	v.seen[c.ProjectID] = true
	links, err := st.ListCardLinks(ctx, c.ID)
	if err != nil {
		return apitypes.Card{}, fmt.Errorf("list links: %w", err)
	}
	for _, l := range links {
		other, dir := l.ToCardID, "out"
		if l.ToCardID == c.ID {
			other, dir = l.FromCardID, "in"
		}
		oc, gerr := st.GetCard(ctx, other)
		if gerr != nil {
			return apitypes.Card{}, fmt.Errorf("load linked card: %w", gerr)
		}
		if ok, serr := v.sees(ctx, oc.ProjectID); serr != nil {
			return apitypes.Card{}, serr
		} else if !ok {
			continue
		}
		link := apitypes.CardLink{Kind: l.Kind.String(), Number: oc.Number, Title: oc.Title, Status: oc.Status.String(), Direction: dir}
		if oc.ProjectID != c.ProjectID {
			if link.Project, err = lk.projectSlug(ctx, oc.ProjectID); err != nil {
				return apitypes.Card{}, err
			}
		}
		out.Links = append(out.Links, link)
	}
	children, err := st.ListFirstChildren(ctx, c.ID, apitypes.MaxEmbeddedChildren)
	if err != nil {
		return apitypes.Card{}, fmt.Errorf("list children: %w", err)
	}
	if out.Children, err = lk.briefs(ctx, children); err != nil {
		return apitypes.Card{}, err
	}
	if out.Progress != nil {
		out.ChildrenRemaining = max(out.Progress.Total-len(children), 0)
	}
	if h, herr := st.LatestComment(ctx, c.ID); herr == nil {
		ho := toComment(h, c.Number)
		out.Comment = &ho
	} else if !errors.Is(herr, domain.ErrNotFound) {
		return apitypes.Card{}, fmt.Errorf("latest comment: %w", herr)
	}
	evs, err := st.ListEventsForSubject(ctx, c.OrgID, cardSubject(c), 10)
	if err != nil {
		return apitypes.Card{}, fmt.Errorf("list card events: %w", err)
	}
	evs, err = filterSeen(ctx, v, evs, func(e domain.Event) *domain.ID { return e.ProjectID })
	if err != nil {
		return apitypes.Card{}, err
	}
	newestFirst(evs)
	if out.Events, err = lk.events(ctx, evs); err != nil {
		return apitypes.Card{}, err
	}
	blockers, err := st.ListBlockers(ctx, c.ID)
	if err != nil {
		return apitypes.Card{}, fmt.Errorf("list blockers: %w", err)
	}
	blockers, err = filterSeen(ctx, v, blockers, func(b domain.Card) *domain.ID { return &b.ProjectID })
	if err != nil {
		return apitypes.Card{}, err
	}
	if out.BlockedBy, err = lk.briefs(ctx, blockers); err != nil {
		return apitypes.Card{}, err
	}
	return out, nil
}

// resolveAssignee checks that the actor exists in the org and may see the
// project.
func (s *Service) resolveAssignee(ctx context.Context, st Store, p Principal, proj domain.Project, in apitypes.Actor) (*domain.ActorRef, error) {
	if in.ID == "" {
		return nil, nil
	}
	ref, err := parseActorPath(in.Kind, in.ID)
	if err != nil {
		return nil, invalidField("assignee", "must be {kind, id}")
	}
	if err := s.actorInOrg(ctx, st, p.OrgID, ref); err != nil {
		return nil, invalidField("assignee", "no user or agent with that id")
	}
	ok, err := s.assignable(ctx, st, p.OrgID, ref, proj)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, invalidField("assignee", "that actor is not active or has no role in this project")
	}
	return &ref, nil
}

// assignable reports whether ref may be assigned cards in proj: an active
// user, or an active agent of the org whose owner could act too, with a role
// there.
func (s *Service) assignable(ctx context.Context, st Store, orgID domain.ID, ref domain.ActorRef, proj domain.Project) (bool, error) {
	q := Principal{Actor: ref, OrgID: orgID}
	switch ref.Kind {
	case domain.ActorUser:
		u, err := st.GetUser(ctx, ref.ID)
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("load user: %w", err)
		}
		if u.Status != domain.RecordActive {
			return false, nil
		}
	case domain.ActorAgent:
		a, err := st.GetAgent(ctx, ref.ID)
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("load agent: %w", err)
		}
		if a.OrgID != orgID || a.Status != domain.RecordActive {
			return false, nil
		}
		owner, role, gap, err := agentOwner(ctx, st, a)
		if err != nil || gap != ownerActive {
			return false, err
		}
		q.Owner, q.OwnerOrgRole = owner, role
	default:
		return false, nil
	}
	m, ok, err := orgMember(ctx, st, orgID, ref)
	if err != nil {
		return false, err
	}
	q.OrgRole, q.HasOrgRole = m.Role, ok
	if q.IsAgent() {
		q.OrgRole = min(q.OrgRole, q.OwnerOrgRole)
		q.OrgRights = m.Rights & domain.RightsHeldBy(q.OwnerOrgRole)
	}
	if _, err := s.access(ctx, st, q, proj); errors.Is(err, domain.ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// dropAssignments clears the assignee on the actor's open cards in projects
// where they may no longer be assigned, after they were revoked or lost a
// role, so that next offers those cards to others again. Each cleared card
// gets the card.updated event an edit of its assignee would.
func (s *Service) dropAssignments(ctx context.Context, tx Store, p Principal, actor domain.ActorRef) error {
	cards, err := tx.ListOpenCardsAssignedTo(ctx, p.OrgID, actor)
	if err != nil {
		return fmt.Errorf("list assigned cards: %w", err)
	}
	keep := map[domain.ID]bool{}
	for _, c := range cards {
		ok, seen := keep[c.ProjectID]
		if !seen {
			proj, err := tx.GetProject(ctx, c.ProjectID)
			if err != nil {
				return fmt.Errorf("load project of #%d: %w", c.Number, err)
			}
			if ok, err = s.assignable(ctx, tx, p.OrgID, actor, proj); err != nil {
				return err
			}
			keep[c.ProjectID] = ok
		}
		if ok {
			continue
		}
		expected := c.Version
		c.Assignee, c.UpdatedAt = nil, s.now()
		updated, err := updateCard(ctx, tx, c, expected)
		if err != nil {
			return err
		}
		if err := s.emitP(ctx, tx, p, &updated.ProjectID, domain.EventCardUpdated, cardSubject(updated), payload(updated.Title, "fields", []string{"assignee"})); err != nil {
			return err
		}
	}
	return nil
}

// ownsCard reports whether the caller created, is assigned to, or holds the
// claim on the card; owners edit with PermCardWrite, others need triage.
func ownsCard(p Principal, c domain.Card, cl *domain.Claim) bool {
	return c.CreatedBy == p.Actor || (c.Assignee != nil && *c.Assignee == p.Actor) || (cl != nil && cl.Actor == p.Actor)
}

// requireCardEdit checks the caller may edit c. Owners need PermCardWrite;
// anyone else needs triage, except that a member may change the title and
// body of an open card (textOnly).
func (s *Service) requireCardEdit(ctx context.Context, st Store, p Principal, a access, c domain.Card, textOnly bool) (*domain.Claim, error) {
	claim, err := currentClaim(ctx, st, c.ID)
	if err != nil {
		return nil, err
	}
	perm := domain.PermCardTriage
	if ownsCard(p, c, claim) || (textOnly && c.Status.Open()) {
		perm = domain.PermCardWrite
	}
	return claim, a.require(p, perm)
}

// textOnly reports whether the edit touches nothing but title, body and page;
// the rest (priority, assignee, parent, lane, branch) is triage.
func textOnly(in apitypes.UpdateCard) bool {
	return in.Priority == nil && in.Assignee == nil && in.Parent == nil && in.Branch == nil && in.Lane == nil
}

// requireUpdate checks the caller may make this edit. A decision's text (title,
// body and page) is the proposer's while it is ready (decision-propose), a reviewer's to edit
// otherwise (decision-accept), and frozen once the decision is done or
// dropped; its other fields follow the ordinary card rules.
func (s *Service) requireUpdate(ctx context.Context, tx Store, p Principal, a access, c domain.Card, in apitypes.UpdateCard) error {
	if c.Kind != domain.KindDecision || (in.Title == nil && in.Body == nil && in.Page == nil) {
		_, err := s.requireCardEdit(ctx, tx, p, a, c, textOnly(in))
		return err
	}
	if err := a.require(p, decisionAuthorPerm(p, c)); err != nil {
		return err
	}
	if !textOnly(in) {
		if _, err := s.requireCardEdit(ctx, tx, p, a, c, false); err != nil {
			return err
		}
	}
	if c.Status.Terminal() {
		return withHint(fmt.Errorf("%w: decision #%d is %s and its text is frozen", domain.ErrInvalidTransition, c.Number, c.Status),
			"accepted text is frozen; record a superseding decision")
	}
	return nil
}

// UpdateCard applies a versioned partial edit. Edits that change the title,
// body or page keep the replaced text as a card revision.
func (s *Service) UpdateCard(ctx context.Context, slug string, number int64, in apitypes.UpdateCard) (apitypes.Card, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Card{}, err
	}
	var out domain.Card
	err = s.store.WithTx(ctx, func(tx Store) error {
		proj, a, c, lerr := s.loadCard(ctx, tx, p, slug, number)
		if lerr != nil {
			return lerr
		}
		if err := s.requireUpdate(ctx, tx, p, a, c, in); err != nil {
			return err
		}
		if verr := versionCheck(c.Version, in.Version, fmt.Sprintf("card #%d", c.Number)); verr != nil {
			return verr
		}
		prev := c
		changed, aerr := s.applyCardEdit(ctx, tx, p, proj, &c, in)
		if aerr != nil {
			return aerr
		}
		pageChanged := false
		var pageErr error
		if in.Page != nil {
			pageErr = domain.ValidatePage(*in.Page)
			if pageErr == nil {
				old, perr := currentCardPage(ctx, tx, prev)
				if perr != nil {
					return perr
				}
				if pageChanged = old != *in.Page; pageChanged {
					c.PageBytes = len(*in.Page)
					changed = append(changed, "page")
				}
			}
		}
		if verr := errors.Join(c.Validate(), pageErr); verr != nil {
			return verr
		}
		c.UpdatedAt = s.now()
		if c.Title != prev.Title || c.Body != prev.Body || pageChanged {
			if serr := tx.SnapshotCardRevision(ctx, c.ID, in.Version, s.ids.New(), p.Actor, c.UpdatedAt, pageChanged); serr != nil {
				if errors.Is(serr, domain.ErrVersionConflict) {
					return versionConflict(prev.Version, fmt.Sprintf("card #%d changed while you were editing", c.Number))
				}
				return fmt.Errorf("keep card #%d revision: %w", c.Number, serr)
			}
		}
		updated, uerr := updateCard(ctx, tx, c, in.Version)
		if uerr != nil {
			return uerr
		}
		out = updated
		if pageChanged {
			if perr := saveCardPage(ctx, tx, out.ID, *in.Page); perr != nil {
				return perr
			}
		}
		if rerr := s.syncRefs(ctx, tx, p.OrgID, &out.ProjectID, domain.SubjectCard, out.ID, out.Body); rerr != nil {
			return rerr
		}
		if eerr := s.emitP(ctx, tx, p, &proj.ID, domain.EventCardUpdated, cardSubject(out), payload(out.Title, "fields", changed)); eerr != nil {
			return eerr
		}
		return s.markSeen(ctx, tx, p, out)
	})
	if err != nil {
		return apitypes.Card{}, err
	}
	return s.lookup(s.store, p).card(ctx, out)
}

// applyCardEdit mutates c from the request and names the fields touched.
func (s *Service) applyCardEdit(ctx context.Context, tx Store, p Principal, proj domain.Project, c *domain.Card, in apitypes.UpdateCard) ([]string, error) {
	var changed []string
	if in.Title != nil {
		c.Title = strings.TrimSpace(*in.Title)
		changed = append(changed, "title")
	}
	if in.Body != nil {
		c.Body = *in.Body
		changed = append(changed, "body")
	}
	if in.Priority != nil {
		pr, err := domain.ParsePriority(*in.Priority)
		if err != nil {
			return nil, invalidField("priority", err.Error())
		}
		c.Priority = pr
		changed = append(changed, "priority")
	}
	if in.Assignee != nil {
		ref, err := s.resolveAssignee(ctx, tx, p, proj, *in.Assignee)
		if err != nil {
			return nil, err
		}
		c.Assignee = ref
		changed = append(changed, "assignee")
	}
	if in.Branch != nil {
		c.Branch = strings.TrimSpace(*in.Branch)
		changed = append(changed, "branch")
	}
	if in.Lane != nil {
		c.Lane = strings.TrimSpace(*in.Lane)
		changed = append(changed, "lane")
	}
	if in.Parent != nil {
		if err := s.setParent(ctx, tx, c, *in.Parent); err != nil {
			return nil, err
		}
		changed = append(changed, "parent")
	}
	if len(changed) == 0 {
		changed = []string{}
	}
	return changed, nil
}

// setParent re-parents c under an open story of its project. number 0 clears
// the parent. Parents are stories and stories have none, so a cycle cannot form. A card in a closed story stays there: its fold names it.
func (s *Service) setParent(ctx context.Context, tx Store, c *domain.Card, number int64) error {
	if c.ParentID != nil {
		cur, err := tx.GetCard(ctx, *c.ParentID)
		if err != nil {
			return fmt.Errorf("load parent of #%d: %w", c.Number, err)
		}
		if cur.Number == number {
			return nil
		}
		if cur.Status.Terminal() {
			return conflict(fmt.Sprintf("story #%d is closed", cur.Number), "a closed story is closed; its cards stay in it")
		}
	}
	if number == 0 {
		c.ParentID = nil
		return nil
	}
	parent, err := refCard(ctx, tx, c.ProjectID, number, "parent")
	if err != nil {
		return err
	}
	if err := parentRules(c.Kind, parent); err != nil {
		return err
	}
	c.ParentID = &parent.ID
	return nil
}

// ListCardRevisions returns one page of a card's past titles, newest first,
// without bodies.
func (s *Service) ListCardRevisions(ctx context.Context, slug string, number int64, pg query.HistoryPage) ([]apitypes.CardRevision, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	_, _, c, err := s.loadCard(ctx, s.store, p, slug, number)
	if err != nil {
		return nil, err
	}
	revs, err := s.store.ListCardRevisions(ctx, c.ID, pg)
	if err != nil {
		return nil, fmt.Errorf("list card revisions: %w", err)
	}
	out := make([]apitypes.CardRevision, 0, len(revs))
	for _, r := range revs {
		out = append(out, toCardRevision(r))
	}
	return out, nil
}

// GetCardRevision returns one past version of a card with its body.
func (s *Service) GetCardRevision(ctx context.Context, slug string, number, version int64) (apitypes.CardRevision, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.CardRevision{}, err
	}
	_, _, c, err := s.loadCard(ctx, s.store, p, slug, number)
	if err != nil {
		return apitypes.CardRevision{}, err
	}
	r, err := s.store.GetCardRevision(ctx, c.ID, version)
	if err != nil {
		return apitypes.CardRevision{}, fmt.Errorf("revision %d of card #%d: %w", version, c.Number, err)
	}
	return toCardRevision(r), nil
}
