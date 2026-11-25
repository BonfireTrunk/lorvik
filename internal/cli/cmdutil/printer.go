package cmdutil

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// NameFunc turns an actor into the name shown to people. A nil NameFunc, or
// one that does not know the actor, falls back to the id.
type NameFunc func(apitypes.Actor) string

func (n NameFunc) of(a apitypes.Actor) string {
	if n != nil {
		if s := n(a); s != "" {
			return s
		}
	}
	return a.ID
}

// AgentMarker follows an agent's name wherever the output names who wrote or
// did something, so a person's name is never mistaken for an agent's.
const AgentMarker = " (agent)"

// Marked is the name, or the id, with AgentMarker after it for an agent.
func (n NameFunc) Marked(a apitypes.Actor) string {
	s := n.of(a)
	if a.Kind == "agent" {
		s += AgentMarker
	}
	return s
}

// Printer writes compact, deterministic human output: one line per item.
type Printer struct {
	W     io.Writer
	Now   func() time.Time
	Color bool
	// Name resolves actors to names. Commands fill the cache before printing.
	Name NameFunc
}

// NewPrinter builds a Printer on the stdout of s. now and name may be nil.
func NewPrinter(s *IOStreams, now func() time.Time, name NameFunc) *Printer {
	if now == nil {
		now = time.Now
	}
	return &Printer{W: s.Out, Now: now, Color: s.ColorEnabled(), Name: name}
}

// Line writes one line.
func (p *Printer) Line(format string, args ...any) {
	fmt.Fprintf(p.W, format+"\n", args...)
}

// Card prints the one-line form of a card.
func (p *Printer) Card(b apitypes.CardBrief) { fmt.Fprintln(p.W, CardLine(b, p.Color, p.Name)) }

// Note prints the one-line form of a note.
func (p *Printer) Note(n apitypes.NoteBrief) { fmt.Fprintln(p.W, NoteLine(n, p.Now(), p.Name)) }

// Named renders an actor as its name, or its id when the name is unknown.
func (p *Printer) Named(a apitypes.Actor) string { return p.Name.of(a) }

// Author renders an actor as its name with AgentMarker for an agent, for
// lines that do not show the actor's kind in a column of their own.
func (p *Printer) Author(a apitypes.Actor) string { return p.Name.Marked(a) }

// Who renders an actor as @name, marked like Author.
func (p *Printer) Who(a apitypes.Actor) string { return "@" + p.Name.Marked(a) }

// Paging prints the trailing line of a paged list: how many rows lie beyond
// the page and the command that fetches the next one. next builds that
// command from the cursor; it is nil for lists that cannot be continued.
func (p *Printer) Paging(page apitypes.Page, next func(cursor string) string) {
	if page.Remaining <= 0 {
		return
	}
	n := RemainingText(page.Remaining)
	if page.Next == "" || next == nil {
		fmt.Fprintf(p.W, "remaining %s (narrow the query or raise --limit)\n", n)
		return
	}
	fmt.Fprintf(p.W, "remaining %s, next: %s\n", n, next(page.Next))
}

// Older prints the command that reads the next page of a newest-first list
// that has no remaining count; cursor is empty on the last page.
func (p *Printer) Older(cursor string, next func(cursor string) string) {
	if cursor != "" {
		fmt.Fprintf(p.W, "older, next: %s\n", next(cursor))
	}
}

// Ago renders t relative to the printer's clock.
func (p *Printer) Ago(t time.Time) string { return RelTime(p.Now(), t) }

const (
	ansiReset = "\x1b[0m"
	ansiDim   = "\x1b[2m"
)

var statusColor = map[string]string{
	"working": "\x1b[33m", "waiting": "\x1b[31m", "done": "\x1b[32m", "dropped": ansiDim,
	"accepted": "\x1b[32m", "superseded": ansiDim, "revoked": ansiDim, "rejected": ansiDim,
}

func paint(on bool, status string) string {
	if c, ok := statusColor[status]; on && ok {
		return c + status + ansiReset
	}
	return status
}

// BriefOf reduces a full card to its list-view shape.
func BriefOf(c apitypes.Card) apitypes.CardBrief {
	return apitypes.CardBrief{
		ID: c.ID, Number: c.Number, Kind: c.Kind, Status: c.Status, Title: c.Title, Priority: c.Priority,
		Parent: c.Parent, Claim: c.Claim, Assignee: c.Assignee, Decision: c.Decision,
		DropReason: c.DropReason, Fold: c.Fold, UpdatedAt: c.UpdatedAt, ClosedAt: c.ClosedAt,
	}
}

// kindMarkers are the title prefixes that tell a card's kind apart in a list.
var kindMarkers = map[string]string{"question": "? ", "story": "S ", "decision": "D "}

// KindMarker is the title prefix for a card kind: "? ", "S ", "D " or "".
func KindMarker(kind string) string { return kindMarkers[kind] }

// ChatLabel is a chat as people read it: "name (label)", or the bare label
// when the chat has no name. The dashboard's chatLabel is its twin.
func ChatLabel(label, name string) string {
	if name == "" {
		return label
	}
	return name + " (" + label + ")"
}

// HolderChat is the same chat inside the parentheses that follow its
// holder's name: "name, label".
func HolderChat(label, name string) string {
	if name == "" {
		return label
	}
	return name + ", " + label
}

// CardLine renders `#42  working  high  Fix login  @claude-code`. Questions,
// stories and decisions get a "? ", "S " or "D " title prefix. Color only
// decorates the status. The holder is the claimant, with the claim's session
// in parentheses when it has one (see HolderChat), else the assignee. Rows of the cross-project
// list name their project in parentheses at the end.
func CardLine(b apitypes.CardBrief, color bool, name NameFunc) string {
	title := KindMarker(b.Kind) + b.Title
	parts := []string{fmt.Sprintf("#%d", b.Number), paint(color, b.Status), b.Priority, title}
	switch {
	case b.Claim != nil:
		holder := "@" + name.Marked(b.Claim.Actor)
		if b.Claim.Session != "" {
			holder += " (" + HolderChat(b.Claim.Session, b.Claim.SessionName) + ")"
		}
		parts = append(parts, holder)
	case b.Assignee != nil:
		parts = append(parts, "@"+name.Marked(*b.Assignee))
	}
	if b.Project != "" {
		parts = append(parts, "("+b.Project+")")
	}
	return strings.Join(parts, "  ")
}

// NoteLine renders `slug  Title  @name  2d ago  (project|org)`, with
// `archived` after the scope for archived notes.
func NoteLine(n apitypes.NoteBrief, now time.Time, name NameFunc) string {
	scope := "org"
	if n.Project != nil {
		scope = *n.Project
	}
	line := fmt.Sprintf("%s  %s  @%s  %s  (%s)", n.Slug, n.Title, name.Marked(n.UpdatedBy), RelTime(now, n.UpdatedAt), scope)
	if n.Status == "archived" {
		line += "  archived"
	}
	return line
}

// RelTime renders t relative to now: "now", "3m ago", "5h ago", "2d ago",
// "in 28m" for the future, and an ISO date beyond 30 days. The date is in
// the machine's zone; Go uses UTC when the machine has none.
func RelTime(now, t time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		return "in " + shortDur(-d)
	}
	if d >= 30*24*time.Hour {
		return t.Local().Format("2006-01-02")
	}
	if d < time.Minute {
		return "now"
	}
	return shortDur(d) + " ago"
}

func shortDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// RemainingText formats a remaining count, capped as "N+" at apitypes.RemainingCap.
func RemainingText(n int) string {
	if n >= apitypes.RemainingCap {
		return strconv.Itoa(apitypes.RemainingCap) + "+"
	}
	return strconv.Itoa(n)
}
