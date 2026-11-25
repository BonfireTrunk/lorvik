package domain

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

var (
	t0    = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	alice = ActorRef{Kind: ActorUser, ID: 101}
	carol = ActorRef{Kind: ActorUser, ID: 102}
	bot   = ActorRef{Kind: ActorAgent, ID: 201}
	bot2  = ActorRef{Kind: ActorAgent, ID: 202}
)

func readyCard() *Card {
	return &Card{ID: 5001, OrgID: 1, ProjectID: 11, Number: 7, Kind: KindTask, Status: StatusReady, Title: "t", Priority: PriorityNormal}
}

func TestCanTransition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind     CardKind
		from, to Status
		want     bool
	}{
		{KindTask, StatusBacklog, StatusReady, true},
		{KindTask, StatusBacklog, StatusWorking, false}, // must be groomed to ready first
		{KindTask, StatusReady, StatusDone, false},      // work must be started before done
		{KindTask, StatusWorking, StatusDone, true},
		{KindTask, StatusDone, StatusReady, true},    // reopen
		{KindTask, StatusDone, StatusWorking, false}, // reopen goes through ready
		{KindTask, StatusDropped, StatusDone, false},
		{KindTask, StatusWaiting, StatusWorking, true},
		{KindTask, StatusWorking, StatusWaiting, true},
		{KindQuestion, StatusDone, StatusReady, true},

		{KindStory, StatusWorking, StatusDone, true},
		{KindStory, StatusReady, StatusDone, true},
		{KindStory, StatusWaiting, StatusDone, true},
		{KindStory, StatusBacklog, StatusDone, false},
		{KindStory, StatusReady, StatusDropped, true},
		{KindStory, StatusReady, StatusWorking, false}, // only a child starting moves a story to working
		{KindStory, StatusWaiting, StatusWorking, false},
		{KindStory, StatusBacklog, StatusWorking, false},
		{KindStory, StatusWorking, StatusReady, true},
		{KindStory, StatusDone, StatusReady, false}, // closed is closed
		{KindStory, StatusDropped, StatusReady, false},

		{KindDecision, StatusReady, StatusDone, true}, // accepted
		{KindDecision, StatusReady, StatusDropped, true},
		{KindDecision, StatusDone, StatusDropped, true}, // revoked
		{KindDecision, StatusDropped, StatusReady, true},
		{KindDecision, StatusDone, StatusReady, false},
		{KindDecision, StatusReady, StatusWorking, false},
		{KindDecision, StatusReady, StatusWaiting, false},
		{KindDecision, StatusReady, StatusBacklog, false},
		{KindDecision, StatusBacklog, StatusReady, false},
		{KindDecision, StatusWorking, StatusDone, false},
	}
	for _, tt := range tests {
		if got := CanTransition(tt.kind, tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%s, %s, %s) = %v, want %v", tt.kind, tt.from, tt.to, got, tt.want)
		}
	}
}

func TestStatusSignConvention(t *testing.T) {
	t.Parallel()
	for _, s := range AllStatuses {
		if s.Terminal() != (s == StatusDone || s == StatusDropped) {
			t.Errorf("%s: Terminal() = %v", s, s.Terminal())
		}
		if s.Open() == s.Terminal() {
			t.Errorf("%s: Open and Terminal must disagree", s)
		}
	}
	if StatusReady >= StatusWaiting || StatusWaiting >= StatusWorking {
		t.Error("ready < waiting < working must hold so the board sorts by progress")
	}
}

func TestTransitionDropRequiresReason(t *testing.T) {
	t.Parallel()
	c := readyCard()
	if err := c.Transition(StatusDropped, DropNone, t0); !errors.As(err, new(*ValidationError)) {
		t.Fatalf("drop without reason error = %v, want ValidationError", err)
	}
	if err := c.Transition(StatusDropped, DropWontFix, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if c.ClosedAt == nil || c.DropReason != DropWontFix {
		t.Errorf("after drop: closed=%v reason=%v", c.ClosedAt, c.DropReason)
	}
	if err := c.Transition(StatusReady, DropNone, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if c.DropReason != DropNone || c.ClosedAt != nil {
		t.Errorf("reopen must clear drop reason and closed_at, got %v %v", c.DropReason, c.ClosedAt)
	}
}

func TestTheLastCallIsFiveMinutesAheadOrAQuarterOfAShortLease(t *testing.T) {
	t.Parallel()
	end := t0.Add(time.Hour)
	tests := []struct {
		name            string
		stated, project time.Duration
		want            time.Duration
	}{
		{"the project's default lease", 0, 30 * time.Minute, 5 * time.Minute},
		{"exactly twenty minutes", 0, 20 * time.Minute, 5 * time.Minute},
		{"a short project lease", 0, 10 * time.Minute, 150 * time.Second},
		{"a short stated lease in a long project", 4 * time.Minute, 30 * time.Minute, time.Minute},
		{"a long stated lease in a short project", 2 * time.Hour, 2 * time.Minute, 5 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cl := Claim{ExpiresAt: end, Lease: tt.stated}
			if got := end.Sub(cl.LastCallAt(tt.project)); got != tt.want {
				t.Errorf("last call %v before the end, want %v", got, tt.want)
			}
		})
	}
}

func TestAStatedLeaseBelongsToTheClaim(t *testing.T) {
	t.Parallel()
	c := readyCard()
	project := 30 * time.Minute

	cl, err := NewClaim(*c, nil, bot, "", t0, project, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if cl.Lease != time.Hour || !cl.ExpiresAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("claim = %+v, want a one-hour lease ending in an hour", cl)
	}

	// A renewal that states nothing keeps the claim's own length.
	cl.Renew(t0.Add(40*time.Minute), project, 0)
	if want := t0.Add(100 * time.Minute); !cl.ExpiresAt.Equal(want) || cl.Lease != time.Hour {
		t.Errorf("renewed = %+v, want expiry %v by the stated hour", cl, want)
	}

	// The holder claiming again without a length keeps the stated one.
	again, err := NewClaim(*c, &cl, bot, "", t0.Add(50*time.Minute), project, 0)
	if err != nil || again.Lease != time.Hour || !again.ExpiresAt.Equal(t0.Add(110*time.Minute)) {
		t.Errorf("re-claim = %+v, %v; want the stated hour kept", again, err)
	}

	// A newly stated length sets the expiry exactly, also to an earlier time.
	cl.Renew(t0.Add(50*time.Minute), project, 10*time.Minute)
	if want := t0.Add(60 * time.Minute); !cl.ExpiresAt.Equal(want) || cl.Lease != 10*time.Minute {
		t.Errorf("renewed for 10m = %+v, want expiry %v", cl, want)
	}

	// Whoever claims after the lease lapsed starts from the project's lease:
	// the stated length was the former holder's.
	lapsed := t0.Add(2 * time.Hour)
	for _, who := range []ActorRef{bot, bot2} {
		next, err := NewClaim(*c, &cl, who, "", lapsed, project, 0)
		if err != nil || next.Lease != 0 || !next.ExpiresAt.Equal(lapsed.Add(project)) {
			t.Errorf("claim by %v after expiry = %+v, %v; want the project's lease", who, next, err)
		}
	}
}

func TestValidLease(t *testing.T) {
	t.Parallel()
	for d, ok := range map[time.Duration]bool{
		59 * time.Second: false, time.Minute: true, time.Hour: true, 24 * time.Hour: true, 24*time.Hour + time.Second: false, 0: false,
	} {
		if err := ValidLease(d); (err == nil) != ok {
			t.Errorf("ValidLease(%s) = %v, want valid %v", d, err, ok)
		}
	}
}

func TestClaimLifecycle(t *testing.T) {
	t.Parallel()
	c := readyCard()
	lease := 30 * time.Minute

	cl, err := NewClaim(*c, nil, bot, "", t0, lease, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusReady {
		t.Fatalf("claiming must not change the card status, got %s", c.Status)
	}
	if !cl.HeldBy(bot, "", t0) || cl.CardID != c.ID || cl.ProjectID != c.ProjectID {
		t.Fatalf("claim = %+v", cl)
	}

	// Another agent is refused while the lease is live.
	if _, err := NewClaim(*c, &cl, bot2, "", t0.Add(10*time.Minute), lease, 0); !errors.Is(err, ErrClaimedByOther) {
		t.Fatalf("second claim error = %v, want ErrClaimedByOther", err)
	}
	// The holder may re-claim.
	if _, err := NewClaim(*c, &cl, bot, "", t0.Add(10*time.Minute), lease, 0); err != nil {
		t.Fatalf("re-claim by holder: %v", err)
	}

	// Renew only ever moves the expiry forward.
	cl.Renew(t0.Add(20*time.Minute), lease, 0)
	if want := t0.Add(50 * time.Minute); !cl.ExpiresAt.Equal(want) {
		t.Errorf("renewed expiry = %v, want %v", cl.ExpiresAt, want)
	}
	cl.Renew(t0.Add(5*time.Minute), lease, 0)
	if want := t0.Add(50 * time.Minute); !cl.ExpiresAt.Equal(want) {
		t.Errorf("renew must not shorten the lease, got %v", cl.ExpiresAt)
	}

	if cl.Expired(t0.Add(49 * time.Minute)) {
		t.Error("expired before the lease ended")
	}
	if !cl.Expired(t0.Add(50 * time.Minute)) {
		t.Error("not expired at the lease end")
	}

	// After expiry anyone may claim.
	if _, err := NewClaim(*c, &cl, bot2, "", t0.Add(51*time.Minute), lease, 0); err != nil {
		t.Fatalf("claim after expiry: %v", err)
	}

	// Closed cards cannot be claimed.
	_ = c.Transition(StatusDropped, DropObsolete, t0)
	if _, err := NewClaim(*c, nil, bot, "", t0, lease, 0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("claim on dropped card = %v, want ErrInvalidTransition", err)
	}
}

func TestStartAndReleased(t *testing.T) {
	t.Parallel()
	c := readyCard()
	if err := c.Start(t0); err != nil || c.Status != StatusWorking {
		t.Fatalf("Start: err=%v status=%s", err, c.Status)
	}
	if err := c.Start(t0); err != nil {
		t.Fatalf("Start on a working card must be a no-op, got %v", err)
	}
	if !c.Released(t0) || c.Status != StatusReady {
		t.Errorf("Released from working: changed=%v status=%s", c.Released(t0), c.Status)
	}
	_ = c.Transition(StatusWaiting, DropNone, t0)
	if c.Released(t0) || c.Status != StatusWaiting {
		t.Errorf("Released from waiting must not change the card, status=%s", c.Status)
	}
	b := &Card{Status: StatusBacklog, Kind: KindTask, Title: "x"}
	if err := b.Start(t0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("Start from backlog = %v, want ErrInvalidTransition", err)
	}
}

func TestAnswer(t *testing.T) {
	t.Parallel()
	q := &Card{ID: 9, Number: 3, Kind: KindQuestion, Status: StatusReady, Title: "db?", Priority: PriorityNormal,
		Question: &Question{Choices: []string{"postgres", "sqlite"}}}

	if _, err := q.Answer("  ", alice, false, nil, t0); !errors.As(err, new(*ValidationError)) {
		t.Fatalf("blank answer error = %v, want ValidationError", err)
	}
	// Choices are suggestions; a human may answer in their own words.
	if _, err := q.Answer("neither, explain the tradeoff first", alice, false, nil, t0); err != nil {
		t.Fatal(err)
	}
	if q.Status != StatusDone || q.Question.AnsweredBy == nil || *q.Question.AnsweredBy != alice || q.ClosedAt == nil {
		t.Errorf("after answer: %+v", q)
	}
	if _, err := q.Answer("sqlite", alice, false, nil, t0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("second answer error = %v, want ErrInvalidTransition", err)
	}
	task := readyCard()
	if _, err := task.Answer("x", alice, false, nil, t0); !errors.As(err, new(*ValidationError)) {
		t.Errorf("answering a task error = %v, want ValidationError", err)
	}
}

func TestAnswerRelay(t *testing.T) {
	t.Parallel()
	t1 := t0.Add(time.Hour)
	open := func() *Card {
		return &Card{Kind: KindQuestion, Status: StatusReady, Title: "db?", Priority: PriorityNormal, Question: &Question{}}
	}
	forAlice := func() *Behalf { return &Behalf{For: alice.ID, Reason: "decision #7", Session: "calm-fox"} }
	answeredFor := func(answer string) *Card {
		c := open()
		if _, err := c.Answer(answer, bot, false, forAlice(), t0); err != nil {
			t.Fatal(err)
		}
		return c
	}
	relayedBy := func(answer string) *Card {
		c := open()
		if _, err := c.Answer(answer, bot, true, nil, t0); err != nil {
			t.Fatal(err)
		}
		return c
	}
	tests := []struct {
		name      string
		card      *Card
		answer    string
		by        ActorRef
		relayed   bool
		behalf    *Behalf
		wantErr   error
		wantField string
		want      Reanswer
		wantBy    ActorRef
		stillRel  bool
		wantFor   bool
	}{
		{name: "agent relays on an open question", card: open(), answer: "sqlite", by: bot, relayed: true, wantBy: bot, stillRel: true},
		{name: "agent without relayed is forbidden", card: open(), answer: "sqlite", by: bot, wantErr: ErrForbidden},
		{name: "entitled agent answers for a person", card: open(), answer: "sqlite", by: bot, behalf: forAlice(), wantBy: bot, wantFor: true},
		{name: "user changes an answer given for a person", card: answeredFor("sqlite"), answer: "postgres", by: alice, want: Reanswer{Confirmed: true, Previous: "sqlite"}, wantBy: alice},
		{name: "another user answers one given for a person as their own", card: answeredFor("sqlite"), answer: "sqlite", by: carol, want: Reanswer{Confirmed: true}, wantBy: carol},
		{name: "agent cannot answer over one given for a person", card: answeredFor("sqlite"), answer: "postgres", by: bot2, behalf: forAlice(), wantErr: ErrInvalidTransition},
		{name: "a kept answer is closed to a user too", card: func() *Card {
			c := answeredFor("sqlite")
			if err := c.Keep(alice, t0); err != nil {
				t.Fatal(err)
			}
			return c
		}(), answer: "postgres", by: alice, wantErr: ErrInvalidTransition},
		{name: "user cannot relay", card: open(), answer: "sqlite", by: alice, relayed: true, wantField: "relayed"},
		{name: "user answers an open question", card: open(), answer: "sqlite", by: alice, wantBy: alice},
		{name: "user confirms a relayed answer", card: relayedBy("sqlite"), answer: "sqlite", by: alice, want: Reanswer{Confirmed: true}, wantBy: alice},
		{name: "user changes a relayed answer", card: relayedBy("sqlite"), answer: "postgres", by: alice, want: Reanswer{Confirmed: true, Previous: "sqlite"}, wantBy: alice},
		{name: "agent cannot relay over a relayed answer", card: relayedBy("sqlite"), answer: "postgres", by: bot2, relayed: true, wantErr: ErrInvalidTransition},
		{name: "user cannot reanswer a user's answer", card: func() *Card {
			c := open()
			_, _ = c.Answer("sqlite", alice, false, nil, t0)
			return c
		}(), answer: "postgres", by: alice, wantErr: ErrInvalidTransition},
		{name: "dropped question stays refused", card: &Card{Kind: KindQuestion, Status: StatusDropped, Question: &Question{}}, answer: "x", by: alice, wantErr: ErrInvalidTransition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.card.Answer(tt.answer, tt.by, tt.relayed, tt.behalf, t1)
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			case tt.wantField != "":
				if !fieldIn(err, tt.wantField) {
					t.Fatalf("err = %v, want invalid %s", err, tt.wantField)
				}
				return
			case err != nil:
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("reanswer = %+v, want %+v", got, tt.want)
			}
			q := tt.card.Question
			if q.Answer != tt.answer || q.AnsweredBy == nil || *q.AnsweredBy != tt.wantBy || q.AnsweredAt == nil || !q.AnsweredAt.Equal(t1) {
				t.Errorf("question = %+v, want answer %q by %v at %v", q, tt.answer, tt.wantBy, t1)
			}
			if tt.card.Status != StatusDone || tt.card.ClosedAt == nil {
				t.Errorf("status = %s, closed %v; want done and closed", tt.card.Status, tt.card.ClosedAt)
			}
			if tt.card.Relayed() != tt.stillRel {
				t.Errorf("Relayed() = %v, want %v", tt.card.Relayed(), tt.stillRel)
			}
			if tt.card.AwaitsKeep() != tt.wantFor {
				t.Errorf("AwaitsKeep() = %v (%+v), want %v", tt.card.AwaitsKeep(), tt.card.Behalf, tt.wantFor)
			}
		})
	}
}

func fieldIn(err error, field string) bool {
	var ve *ValidationError
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range j.Unwrap() {
			if errors.As(e, &ve) && ve.Field == field {
				return true
			}
		}
		return false
	}
	return errors.As(err, &ve) && ve.Field == field
}

func TestCardValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		mut   func(*Card)
		field string
	}{
		{"empty title", func(c *Card) { c.Title = " " }, "title"},
		{"drop reason without dropped", func(c *Card) { c.DropReason = DropObsolete }, "drop_reason"},
		{"dropped without reason", func(c *Card) { c.Status = StatusDropped }, "drop_reason"},
		{"unknown drop reason", func(c *Card) { c.Status = StatusDropped; c.DropReason = 9 }, "drop_reason"},
		{"self parent", func(c *Card) { id := c.ID; c.ParentID = &id }, "parent_id"},
		{"unknown status", func(c *Card) { c.Status = 3 }, "status"},
		{"unknown priority", func(c *Card) { c.Priority = 4 }, "priority"},
		{"lane with a space", func(c *Card) { c.Lane = "gen erated" }, "lane"},
		{"lane with a comma", func(c *Card) { c.Lane = "web,schema" }, "lane"},
		{"lane too long", func(c *Card) { c.Lane = strings.Repeat("l", MaxLaneLen+1) }, "lane"},
		{"branch with a space", func(c *Card) { c.Branch = "card 8" }, "branch"},
		{"branch too long", func(c *Card) { c.Branch = strings.Repeat("b", MaxBranchLen+1) }, "branch"},
		{"single choice", func(c *Card) { c.Kind = KindQuestion; c.Question = &Question{Choices: []string{"only"}} }, "choices"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := readyCard()
			tt.mut(c)
			if err := c.Validate(); !fieldIn(err, tt.field) {
				t.Errorf("Validate() = %v, want a problem on field %q", err, tt.field)
			}
		})
	}
	if err := readyCard().Validate(); err != nil {
		t.Errorf("valid card: %v", err)
	}
}

func decisionCard(status Status) *Card {
	c := &Card{ID: 9, OrgID: 1, ProjectID: 11, Number: 3, Kind: KindDecision, Status: status, Title: "Use Go", Priority: PriorityNormal}
	if status == StatusDone {
		c.Acceptance = &Acceptance{By: alice, At: t0}
	}
	return c
}

func TestDecisionLifecycle(t *testing.T) {
	t.Parallel()
	d := decisionCard(StatusReady)
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := d.Accept(alice, t0); err != nil {
		t.Fatal(err)
	}
	if d.Status != StatusDone || d.Acceptance == nil || d.Acceptance.By != alice || d.ClosedAt == nil {
		t.Errorf("after accept: %+v", d)
	}
	if err := d.Validate(); err != nil {
		t.Errorf("accepted decision: %v", err)
	}
	if err := d.Transition(StatusDropped, DropObsolete, t0); !fieldIn(err, "drop_reason") {
		t.Errorf("done -> dropped(obsolete) = %v, want drop_reason", err)
	}
	if err := d.Transition(StatusDropped, DropRevoked, t0); err != nil || d.DropReason != DropRevoked {
		t.Fatalf("revoke: err=%v reason=%s", err, d.DropReason)
	}
	if d.Acceptance == nil {
		t.Error("a revoked decision keeps who accepted it")
	}
	if err := d.Transition(StatusReady, DropNone, t0); !fieldIn(err, "status") {
		t.Errorf("revoked -> ready = %v, want status", err)
	}

	r := decisionCard(StatusReady)
	if err := r.Transition(StatusDropped, DropObsolete, t0); !fieldIn(err, "drop_reason") {
		t.Errorf("ready -> dropped(obsolete) = %v, want drop_reason", err)
	}
	if err := r.Transition(StatusDropped, DropRejected, t0); err != nil {
		t.Fatal(err)
	}
	if err := r.Transition(StatusReady, DropNone, t0); err != nil || r.Status != StatusReady || r.DropReason != DropNone || r.ClosedAt != nil {
		t.Errorf("rejected -> ready (re-propose) err=%v card=%+v", err, r)
	}
}

func TestDecisionRefusals(t *testing.T) {
	t.Parallel()
	for _, to := range []Status{StatusBacklog, StatusWorking, StatusWaiting} {
		d := decisionCard(StatusReady)
		if err := d.Transition(to, DropNone, t0); !fieldIn(err, "status") || errors.Is(err, ErrInvalidTransition) {
			t.Errorf("ready -> %s = %v, want a status field error", to, err)
		}
	}
	d := decisionCard(StatusReady)
	if err := d.Transition(StatusDropped, DropSuperseded, t0); !fieldIn(err, "drop_reason") {
		t.Errorf("client-set superseded = %v, want drop_reason", err)
	}
	if err := (&Card{Kind: KindTask, Status: StatusReady}).Transition(StatusDropped, DropRejected, t0); !fieldIn(err, "drop_reason") {
		t.Errorf("task dropped as rejected = %v, want drop_reason", err)
	}
	if _, err := NewClaim(*d, nil, bot, "", t0, time.Hour, 0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("claiming a decision = %v, want ErrInvalidTransition", err)
	}
	done := decisionCard(StatusDone)
	done.Acceptance = nil
	if err := done.Validate(); !fieldIn(err, "status") {
		t.Errorf("done decision without acceptor = %v, want status", err)
	}
}

func TestSupersede(t *testing.T) {
	t.Parallel()
	d := decisionCard(StatusDone)
	if err := d.Supersede(t0); err != nil || d.Status != StatusDropped || d.DropReason != DropSuperseded {
		t.Fatalf("supersede: err=%v card=%+v", err, d)
	}
	if err := d.Validate(); err != nil {
		t.Errorf("superseded decision: %v", err)
	}
	if err := decisionCard(StatusReady).Supersede(t0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("supersede a proposal = %v, want ErrInvalidTransition", err)
	}
}

func TestStoryRules(t *testing.T) {
	t.Parallel()
	s := &Card{Kind: KindStory, Status: StatusBacklog, Title: "s", Priority: PriorityNormal}
	if !s.WorkBegan(t0) || s.Status != StatusWorking {
		t.Errorf("backlog story after a child started = %s", s.Status)
	}
	if s.WorkBegan(t0) {
		t.Error("a working story does not change again")
	}
	if (&Card{Kind: KindTask, Status: StatusReady}).WorkBegan(t0) {
		t.Error("only a story follows its children")
	}
	closed := &Card{Kind: KindStory, Status: StatusDone}
	if err := closed.Transition(StatusReady, DropNone, t0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("reopening a story = %v, want ErrInvalidTransition", err)
	}
}

func TestFoldInto(t *testing.T) {
	t.Parallel()
	c := readyCard()
	if err := c.FoldInto("docs/a.md", alice, t0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("fold a ready card = %v", err)
	}
	c.Status = StatusDone
	parent := ID(2)
	c.ParentID = &parent
	if err := c.FoldInto("", alice, t0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("fold a child = %v", err)
	}
	c.ParentID = nil
	if err := c.FoldInto("", alice, t0); err != nil || c.Fold == nil || c.Fold.Into != "" || c.Fold.By != alice {
		t.Fatalf("fold with no document: err=%v fold=%+v", err, c.Fold)
	}
	if err := c.FoldInto("x.md", alice, t0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("fold twice = %v", err)
	}
}

func TestParkedBy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		kind    CardKind
		status  Status
		blocker CardKind
		want    bool
	}{
		{"ready task by task", KindTask, StatusReady, KindTask, true},
		{"working task by task", KindTask, StatusWorking, KindTask, true},
		{"ready task by question", KindTask, StatusReady, KindQuestion, true},
		{"backlog task by task", KindTask, StatusBacklog, KindTask, false},
		{"backlog task by question", KindTask, StatusBacklog, KindQuestion, true},
		{"backlog question by question", KindQuestion, StatusBacklog, KindQuestion, true},
		{"waiting task by question", KindTask, StatusWaiting, KindQuestion, false},
		{"done task by question", KindTask, StatusDone, KindQuestion, false},
		{"ready decision by question", KindDecision, StatusReady, KindQuestion, false},
		{"backlog decision by question", KindDecision, StatusBacklog, KindQuestion, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := Card{Kind: tt.kind, Status: tt.status}
			if got := c.ParkedBy(tt.blocker); got != tt.want {
				t.Errorf("ParkedBy(%v) = %v, want %v", tt.blocker, got, tt.want)
			}
		})
	}
}

func TestTokenSecret(t *testing.T) {
	t.Parallel()
	secret, prefix, hash := NewTokenSecret(ActorAgent)
	if !LooksLikeToken(secret) || secret[:6] != "lvk_a_" {
		t.Errorf("secret %q has wrong shape", secret)
	}
	if secret[:len(prefix)] != prefix || len(prefix) != 12 {
		t.Errorf("prefix %q is not a 12-char prefix of the secret", prefix)
	}
	if !bytes.Equal(HashToken(secret), hash) || len(hash) != 48 {
		t.Error("hash does not match HashToken(secret) or is not 48 bytes")
	}
	// Stored hashes are looked up by value, so the algorithm is part of the
	// data: this digest is SHA3-384 from another implementation.
	const known = "lvk_u_EXAMPLEEXAMPLEEXAMPLEEX"
	if got, want := hex.EncodeToString(HashToken(known)), "1961c5bda54efcd909937e9930584b75f3d3b331737fbd3a5fd7379ee3496d6727d7c8c108778acdcd03bbfa9a403e92"; got != want {
		t.Errorf("HashToken(%q) = %s, want SHA3-384 %s", known, got, want)
	}
	s2, _, _ := NewTokenSecret(ActorAgent)
	if s2 == secret {
		t.Error("two secrets were identical")
	}
}

func TestTokenUsableAndRotation(t *testing.T) {
	t.Parallel()
	tok := Token{Status: RecordActive, ExpiresAt: t0}
	if tok.Usable(t0) || !tok.Usable(t0.Add(-time.Second)) {
		t.Error("Usable must be false at and after expiry, true before")
	}
	tok.Status = RecordRevoked
	if tok.Usable(t0.Add(-time.Hour)) {
		t.Error("revoked token must not be usable")
	}
	grace := Token{Status: RecordRotated, ExpiresAt: t0.Add(TokenRotationGrace)}
	if !grace.Usable(t0.Add(time.Hour)) || grace.Usable(t0.Add(TokenRotationGrace)) {
		t.Error("rotated token is usable during grace only")
	}

	active := Token{Status: RecordActive, CreatedAt: t0, ExpiresAt: t0.Add(30 * 24 * time.Hour)}
	if active.NeedsRotation(t0) {
		t.Error("fresh token must not rotate")
	}
	if !active.NeedsRotation(t0.Add(24 * 24 * time.Hour)) {
		t.Error("token inside its last 7 days must rotate")
	}
	short := Token{Status: RecordActive, CreatedAt: t0, ExpiresAt: t0.Add(3 * 24 * time.Hour)}
	if short.NeedsRotation(t0.Add(time.Hour)) {
		t.Error("a token that lives under two rotation windows never rotates")
	}
	forever := Token{Status: RecordActive, ExpiresAt: time.Date(TokenNoExpiryYear, 1, 1, 0, 0, 0, 0, time.UTC)}
	if forever.NeedsRotation(forever.ExpiresAt.Add(-time.Hour)) {
		t.Error("a no-expiry token never rotates")
	}
}

func TestProjectVisibilityAndEffectiveRole(t *testing.T) {
	t.Parallel()
	open := Project{Visibility: VisibilityOrg}
	private := Project{Visibility: VisibilityPrivate}
	global := Project{Visibility: VisibilityOrg, Scope: ProjectScopeGlobal}
	tests := []struct {
		name          string
		p             Project
		orgRole       Role
		hasOrg        bool
		projRole      Role
		hasProj       bool
		wantRole      Role
		wantVisible   bool
		wantRoleEqual bool
		hasAny        bool
	}{
		{"member sees org project", open, RoleMember, true, 0, false, RoleMember, true, true, false},
		{"project role raises org role", open, RoleMember, true, RoleReviewer, true, RoleReviewer, true, true, false},
		{"org role is kept when higher", open, RoleReviewer, true, RoleViewer, true, RoleReviewer, true, true, false},
		{"agent with project row only", open, 0, false, RoleMember, true, RoleMember, true, true, false},
		{"nobody", open, 0, false, 0, false, 0, false, true, false},
		{"member cannot see private", private, RoleMember, true, 0, false, 0, false, true, false},
		{"admin sees private", private, RoleAdmin, true, 0, false, RoleAdmin, true, true, false},
		{"private uses the project role only", private, RoleReviewer, true, RoleViewer, true, RoleViewer, true, true, false},
		{"global keeps the org role", global, RoleMember, true, 0, false, RoleMember, true, true, false},
		{"global is viewer for a project-only actor", global, 0, false, RoleMember, true, RoleViewer, true, true, true},
		{"global is hidden from an actor with no roles", global, 0, false, 0, false, 0, false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			role, ok := tt.p.EffectiveRole(Standing{OrgRole: tt.orgRole, HasOrgRole: tt.hasOrg, ProjectRole: tt.projRole, HasProjectRole: tt.hasProj, InAnyProject: tt.hasAny})
			if ok != tt.wantVisible || role != tt.wantRole {
				t.Errorf("EffectiveRole = (%s, %v), want (%s, %v)", role, ok, tt.wantRole, tt.wantVisible)
			}
		})
	}
}

func TestOrgSettings(t *testing.T) {
	t.Parallel()
	var s OrgSettings
	if s.TokenTTLDefaultOrDefault() != DefaultTokenTTL || s.TokenTTLMaxOrDefault() != MaxTokenTTL || s.AgentsNeedApproval() || s.ProjectVisibilityOrDefault() != VisibilityOrg {
		t.Error("zero settings must yield the defaults")
	}
	s = OrgSettings{AgentApproval: "admin", TokenTTLDefault: "48h", ProjectVisibilityDefault: "private"}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if !s.AgentsNeedApproval() || s.TokenTTLDefaultOrDefault() != 48*time.Hour || s.ProjectVisibilityOrDefault() != VisibilityPrivate {
		t.Errorf("settings not applied: %+v", s)
	}
	for _, bad := range []OrgSettings{{AgentApproval: "maybe"}, {TokenTTLMax: "10000h"}, {TokenTTLDefault: "1m"}, {ProjectVisibilityDefault: "secret"}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want error", bad)
		}
	}
}

func TestMembershipValidate(t *testing.T) {
	t.Parallel()
	if err := (Membership{Actor: bot, Role: RoleAdmin}).Validate(); err == nil {
		t.Error("agent as admin must be refused")
	}
	if err := (Membership{Actor: bot, Role: RoleReviewer}).Validate(); !fieldIn(err, "role") {
		t.Errorf("agent as reviewer = %v, want a role error: its rights carry the rest", err)
	}
	if err := (Membership{Actor: bot, Role: RoleViewer, Rights: RightWork | RightReady}).Validate(); err != nil {
		t.Errorf("agent as viewer with rights: %v", err)
	}
	if err := (ProjectMember{Actor: bot, Role: RoleMember}).Validate(); !fieldIn(err, "role") {
		t.Errorf("agent project member = %v, want a role error", err)
	}
	if err := (ProjectMember{Actor: alice, Role: RoleAdmin}).Validate(); err == nil {
		t.Error("admin is not a project role")
	}
	if err := (ProjectMember{Actor: alice, Role: 25}).Validate(); err == nil {
		t.Error("unknown role must be refused")
	}
}

func TestUserValidate(t *testing.T) {
	t.Parallel()
	u := User{Login: "ada", Locale: "en-IN", Timezone: "Asia/Kolkata"}
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
	u.Timezone = "Mars/Olympus"
	if err := u.Validate(); !fieldIn(err, "timezone") {
		t.Errorf("bad timezone = %v", err)
	}
	if err := (User{Login: "ada"}).Validate(); err != nil {
		t.Errorf("unset locale and timezone should be valid, got %v", err)
	}
}

// An agent's org-wide rights apply where the org role counts in the project.
func TestUsesOrgRole(t *testing.T) {
	t.Parallel()
	open := Project{Visibility: VisibilityOrg}
	private := Project{Visibility: VisibilityPrivate}
	global := Project{Visibility: VisibilityOrg, Scope: ProjectScopeGlobal}
	tests := []struct {
		name string
		p    Project
		s    Standing
		want bool
	}{
		{"org project, org role", open, Standing{OrgRole: RoleViewer, HasOrgRole: true}, true},
		{"org project, both roles", open, Standing{OrgRole: RoleViewer, HasOrgRole: true, ProjectRole: RoleMember, HasProjectRole: true}, true},
		{"org project, project row only", open, Standing{ProjectRole: RoleViewer, HasProjectRole: true}, false},
		{"private project, non-admin org role and a row", private, Standing{OrgRole: RoleViewer, HasOrgRole: true, ProjectRole: RoleViewer, HasProjectRole: true}, false},
		{"private project, admin sees it", private, Standing{OrgRole: RoleAdmin, HasOrgRole: true}, true},
		{"private project, hidden", private, Standing{OrgRole: RoleViewer, HasOrgRole: true}, false},
		{"global, org role", global, Standing{OrgRole: RoleViewer, HasOrgRole: true}, true},
		{"global, project row only", global, Standing{ProjectRole: RoleViewer, HasProjectRole: true, InAnyProject: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.p.UsesOrgRole(tt.s); got != tt.want {
				t.Errorf("UsesOrgRole = %v, want %v", got, tt.want)
			}
		})
	}
}

// Only the person an agent settled a card for keeps it, once; nobody keeps a
// card no agent settled for someone.
func TestKeep(t *testing.T) {
	t.Parallel()
	t1 := t0.Add(time.Hour)
	answered := func() *Card {
		c := &Card{Kind: KindQuestion, Status: StatusReady, Title: "db?", Priority: PriorityNormal, Question: &Question{}}
		if _, err := c.Answer("sqlite", bot, false, &Behalf{For: alice.ID, Reason: "decision #7"}, t0); err != nil {
			t.Fatal(err)
		}
		return c
	}
	kept := answered()
	if err := kept.Keep(alice, t0); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		card    *Card
		by      ActorRef
		wantErr error
	}{
		{"the person it was answered for", answered(), alice, nil},
		{"another person", answered(), carol, ErrForbidden},
		{"the agent that answered", answered(), bot, ErrForbidden},
		{"an agent with the person's id", answered(), ActorRef{Kind: ActorAgent, ID: alice.ID}, ErrForbidden},
		{"already kept", kept, alice, ErrInvalidTransition},
		{"a person's own answer", &Card{Kind: KindQuestion, Status: StatusDone, Question: &Question{Answer: "x", AnsweredBy: &alice}}, alice, ErrInvalidTransition},
	}
	for _, tt := range tests {
		err := tt.card.Keep(tt.by, t1)
		if !errors.Is(err, tt.wantErr) {
			t.Errorf("%s: Keep = %v, want %v", tt.name, err, tt.wantErr)
			continue
		}
		if err == nil && (tt.card.AwaitsKeep() || !tt.card.Behalf.KeptAt.Equal(t1) || tt.card.Question.AnsweredBy == nil || *tt.card.Question.AnsweredBy != bot) {
			t.Errorf("%s: after Keep behalf = %+v, answered by %v; want kept at %v and still the agent's answer", tt.name, tt.card.Behalf, tt.card.Question.AnsweredBy, t1)
		}
	}
	reopened := answered()
	if err := reopened.Transition(StatusReady, DropNone, t1); err != nil {
		t.Fatal(err)
	}
	if reopened.Behalf != nil || reopened.Question.Answer != "" {
		t.Errorf("reopened: behalf = %+v, answer %q; want an open question with nothing waiting to be kept", reopened.Behalf, reopened.Question.Answer)
	}
}

func TestAVerdictAnAgentGaveIsForgottenWhenTheDecisionMoves(t *testing.T) {
	t.Parallel()
	settled := func(status Status, reason DropReason) *Card {
		d := decisionCard(status)
		d.DropReason = reason
		d.Behalf = &Behalf{For: alice.ID, By: bot.ID, Reason: "requirements 2.3"}
		return d
	}
	tests := []struct {
		name string
		card *Card
		move func(*Card) error
	}{
		{"revoked", settled(StatusDone, DropNone), func(c *Card) error { return c.Transition(StatusDropped, DropRevoked, t0) }},
		{"proposed again", settled(StatusDropped, DropRejected), func(c *Card) error { return c.Transition(StatusReady, DropNone, t0) }},
		{"superseded", settled(StatusDone, DropNone), func(c *Card) error { return c.Supersede(t0) }},
	}
	for _, tt := range tests {
		if err := tt.move(tt.card); err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if tt.card.Behalf != nil {
			t.Errorf("%s: behalf = %+v, want nothing left waiting for a look", tt.name, tt.card.Behalf)
		}
	}
}

func TestAQuestionForAPerson(t *testing.T) {
	t.Parallel()
	open := func() *Card {
		return &Card{Kind: KindQuestion, Status: StatusReady, Title: "buy?", Priority: PriorityNormal, Question: &Question{}}
	}
	marked := open()
	if err := marked.MarkForPerson(t0); err != nil || !marked.ForPerson() {
		t.Fatalf("MarkForPerson = %v, for person %v", err, marked.ForPerson())
	}
	for name, c := range map[string]*Card{
		"already marked":  marked,
		"an answered one": {Kind: KindQuestion, Status: StatusDone, Question: &Question{Answer: "x"}},
		"a dropped one":   {Kind: KindQuestion, Status: StatusDropped, Question: &Question{}},
	} {
		if err := c.MarkForPerson(t0); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("%s: MarkForPerson = %v, want ErrInvalidTransition", name, err)
		}
	}
	if err := (&Card{Kind: KindTask, Status: StatusReady}).MarkForPerson(t0); !errors.As(err, new(*ValidationError)) {
		t.Errorf("a task: MarkForPerson = %v, want ValidationError", err)
	}

	answers := []struct {
		name    string
		by      ActorRef
		relayed bool
		behalf  *Behalf
		wantErr error
	}{
		{"an agent holding the answer right", bot, false, &Behalf{For: alice.ID, Reason: "decision #7"}, ErrForbidden},
		{"an agent relaying a person", bot, true, nil, nil},
		{"a person", alice, false, nil, nil},
	}
	for _, tt := range answers {
		c := open()
		c.Question.ForPerson = true
		if _, err := c.Answer("yes", tt.by, tt.relayed, tt.behalf, t0); !errors.Is(err, tt.wantErr) {
			t.Errorf("%s answers: %v, want %v", tt.name, err, tt.wantErr)
		} else if err != nil && (c.Status != StatusReady || c.Question.Answer != "") {
			t.Errorf("%s: the refused answer changed the card: %+v", tt.name, c)
		}
	}

	if err := marked.ClearForPerson(bot, t0); !errors.Is(err, ErrForbidden) || !marked.ForPerson() {
		t.Errorf("an agent clears: %v, still marked %v; want ErrForbidden and the mark kept", err, marked.ForPerson())
	}
	if err := marked.ClearForPerson(alice, t0); err != nil || marked.ForPerson() {
		t.Errorf("a person clears: %v, still marked %v", err, marked.ForPerson())
	}
	if err := marked.ClearForPerson(alice, t0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("clearing an unmarked question = %v, want ErrInvalidTransition", err)
	}
}
