package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

// payload is an event's decoded payload. The server writes plain JSON
// objects, so a map keeps the renderer tolerant of keys it does not know.
type payload map[string]any

func decodePayload(e apitypes.Event) payload {
	p := payload{}
	if len(e.Payload) > 0 {
		_ = json.Unmarshal(e.Payload, &p)
	}
	return p
}

func (p payload) str(key string) string {
	switch v := p[key].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	}
	return ""
}

func (p payload) int(key string) (int64, bool) {
	v, ok := p[key].(float64)
	return int64(v), ok
}

func (p payload) actor(key string) (apitypes.Actor, bool) {
	m, ok := p[key].(map[string]any)
	if !ok {
		return apitypes.Actor{}, false
	}
	id, _ := m["id"].(string)
	kind, _ := m["kind"].(string)
	return apitypes.Actor{Kind: kind, ID: id}, id != ""
}

func (p payload) list(key string) []string {
	raw, _ := p[key].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// payloadActors are the payload keys that hold an actor.
var payloadActors = []string{"actor", "taken_from", "released_from", "for"}

// eventsActors lists every actor an event list mentions, for name lookup.
func eventsActors(evs []apitypes.Event) []apitypes.Actor {
	var out []apitypes.Actor
	for _, e := range evs {
		out = append(out, e.Actor)
		p := decodePayload(e)
		for _, k := range payloadActors {
			if a, ok := p.actor(k); ok {
				out = append(out, a)
			}
		}
	}
	return out
}

// cardRef renders `#42 "title"`; the title is left out when the payload has none.
func cardRef(num int64, title string) string {
	if title == "" {
		return fmt.Sprintf("#%d", num)
	}
	return fmt.Sprintf("#%d %q", num, title)
}

// clip shortens free text, such as an answer, to 80 runes for a one-line sentence.
func clip(s string) string {
	const n = 80
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// subjectRef is the generic reference to an event's subject.
func subjectRef(s apitypes.Subject) string {
	if s.Type == "card" {
		return fmt.Sprintf("#%d", s.Number)
	}
	return s.Type + ":" + s.ID
}

// eventSentence renders one event as a sentence that starts with the actor.
// Types it does not know render as `<actor> <type> <subject>`.
func eventSentence(e apitypes.Event, name cmdutil.NameFunc) string {
	who := name.Marked
	p := decodePayload(e)
	by := who(e.Actor)
	title := p.str("title")
	card := cardRef(e.Subject.Number, title)

	switch e.Type {
	case "card.created":
		kind := p.str("kind")
		if kind == "" {
			kind = "card"
		}
		s := fmt.Sprintf("%s created %s %s", by, kind, card)
		switch kind {
		case "story":
			s = fmt.Sprintf("%s opened story %s", by, card)
		case "decision":
			verb := "proposed"
			if p.str("status") == "done" {
				verb = "recorded"
			}
			s = fmt.Sprintf("%s %s decision %s", by, verb, card)
		}
		s += parens(p.str("status"), p.str("priority"))
		if n, ok := p.int("supersedes"); ok {
			s += fmt.Sprintf(", supersedes #%d", n)
		}
		if person, _ := p["for_person"].(bool); person {
			s += ", for a person"
		}
		return s
	case "card.updated":
		s := fmt.Sprintf("%s edited %s", by, card)
		if f := p.list("fields"); len(f) > 0 {
			s += " (" + strings.Join(f, ", ") + ")"
		}
		return s
	case "card.status_changed":
		if s, ok := decisionStatusSentence(p, by, card, who); ok {
			return s
		}
		s := fmt.Sprintf("%s moved %s from %s to %s", by, card, p.str("from"), p.str("to"))
		if r := p.str("drop_reason"); r != "" {
			s += " (" + r + ")"
		}
		if n, ok := p.int("blocked_by"); ok {
			s += ", blocked by " + cardRef(n, p.str("blocker_title"))
		}
		if n, ok := p.int("unblocked_by"); ok {
			s += ", unblocked by " + cardRef(n, p.str("blocker_title"))
		}
		if n, ok := p.int("superseded_by"); ok {
			s += fmt.Sprintf(", superseded by #%d", n)
		}
		if n, ok := p.int("by_child"); ok {
			s += fmt.Sprintf(", because #%d started", n)
		}
		if n, ok := p.int("cascade_of"); ok {
			s += fmt.Sprintf(", dropped with story #%d", n)
		}
		return s
	case "card.claimed":
		s := fmt.Sprintf("%s claimed %s", by, card)
		if from, ok := p.actor("taken_from"); ok {
			s += " (taken over from " + who(from) + inSession(p.str("taken_from_session")) + ")"
		}
		return s
	case "card.released":
		if from, ok := p.actor("released_from"); ok {
			s := fmt.Sprintf("%s released %s's claim on %s", by, who(from), card)
			if session := p.str("released_from_session"); session != "" {
				s += " (session " + session + ")"
			}
			return s
		}
		return fmt.Sprintf("%s released %s", by, card)
	case "card.claim_ending":
		when := "soon"
		if end, err := time.Parse(time.RFC3339, p.str("expires_at")); err == nil && end.After(e.CreatedAt) {
			when = cmdutil.RelTime(e.CreatedAt, end)
		}
		return fmt.Sprintf("%s's claim on %s%s ends %s", by, card, inSession(p.str("session")), when)
	case "card.claim_expired":
		return fmt.Sprintf("%s's claim on %s%s expired", by, card, inSession(p.str("session")))
	case "card.answered":
		var s string
		relayed, _ := p["relayed"].(bool)
		confirmed, _ := p["confirmed"].(bool)
		switch prev := p.str("previous"); {
		case relayed:
			s = fmt.Sprintf("%s relayed an answer on %s: %s", by, card, clip(p.str("answer")))
		case confirmed && prev != "":
			s = fmt.Sprintf("%s changed the answer on %s: %s (was %s)", by, card, clip(p.str("answer")), clip(prev))
		case confirmed:
			s = fmt.Sprintf("%s confirmed the answer on %s", by, card)
		default:
			s = fmt.Sprintf("%s answered %s", by, card)
			if owner, ok := p.actor("for"); ok {
				s += " for " + who(owner)
			}
			s += ": " + clip(p.str("answer"))
		}
		if d, _ := p["decided"].(bool); d {
			s += " (recorded as a decision)"
		}
		return s
	case "card.for_person":
		if set, _ := p["set"].(bool); !set {
			return fmt.Sprintf("%s cleared the for-a-person mark on %s", by, card)
		}
		return fmt.Sprintf("%s marked %s for a person", by, card)
	case "card.kept":
		what := "answer"
		if p.str("kind") == "decision" {
			what = "verdict"
		}
		return fmt.Sprintf("%s kept the %s on %s", by, what, card)
	case "card.linked", "card.unlinked":
		verb := "linked"
		if e.Type == "card.unlinked" {
			verb = "unlinked"
		}
		to, _ := p.int("to")
		return fmt.Sprintf("%s %s %s %s %s", by, verb, card, p.str("kind"), cardRef(to, p.str("to_title")))
	case "card.comment":
		return fmt.Sprintf("%s commented on %s", by, card)
	case "card.folded":
		if into := p.str("into"); into != "" {
			return fmt.Sprintf("%s folded %s into %s", by, card, into)
		}
		return fmt.Sprintf("%s folded %s, no contract change", by, card)
	case "note.created", "note.updated":
		ref := "note " + p.str("slug")
		if title != "" {
			ref += fmt.Sprintf(" %q", title)
		}
		switch {
		case e.Type == "note.created":
			return fmt.Sprintf("%s created %s", by, ref)
		case p.str("status") == "archived":
			return fmt.Sprintf("%s archived %s", by, ref)
		}
		return fmt.Sprintf("%s edited %s", by, ref)
	case "project.created":
		return fmt.Sprintf("%s created project %s %q", by, p.str("slug"), title)
	case "project.updated":
		return fmt.Sprintf("%s updated project %s", by, p.str("slug"))
	case "project.archived":
		return fmt.Sprintf("%s archived project %s", by, p.str("slug"))
	case "agent.registered":
		s := fmt.Sprintf("%s registered agent %q", by, title)
		if p.str("status") == "pending" {
			s += " (pending approval)"
		}
		return s
	case "agent.approved":
		return fmt.Sprintf("%s approved agent %q", by, title)
	case "agent.revoked":
		return fmt.Sprintf("%s revoked agent %q", by, title)
	case "agent.restored":
		s := fmt.Sprintf("%s restored agent %q", by, title)
		if p.str("status") == "pending" {
			s += " (pending approval)"
		}
		return s
	case "agent.removed":
		return fmt.Sprintf("%s removed agent %q", by, title)
	case "agent.join_requested":
		return fmt.Sprintf("%s asked to join %s", by, p.str("project"))
	case "agent.joined":
		if auto, _ := p["auto"].(bool); auto {
			return fmt.Sprintf("%s joined %s with %s, allowed by its owner's settings", by, p.str("project"), grantWords(p))
		}
		return fmt.Sprintf("%s let agent %q into %s with %s", by, title, p.str("project"), grantWords(p))
	case "agent.session_rights":
		switch {
		case p.str("ended_at") != "":
			return fmt.Sprintf("the grant of chat %s of %s in %s reached its end", p.str("session"), title, p.str("project"))
		case len(p.list("rights")) == 0:
			return fmt.Sprintf("%s took back chat %s of %s in %s", by, p.str("session"), title, p.str("project"))
		}
		s := fmt.Sprintf("%s gave chat %s of %s %s in %s", by, p.str("session"), title, strings.Join(p.list("rights"), ", "), p.str("project"))
		if end, err := time.Parse(time.RFC3339, p.str("ends_at")); err == nil && end.After(e.CreatedAt) {
			s += ", ending " + cmdutil.RelTime(e.CreatedAt, end)
		}
		return s
	case "agent.updated":
		return fmt.Sprintf("%s set agent %q's join setting to %s", by, title, p.str("agent_join"))
	case "agent.join_denied":
		return fmt.Sprintf("%s denied agent %q in %s", by, title, p.str("project"))
	case "agent.join_setting":
		if proj := p.str("project"); proj != "" {
			return fmt.Sprintf("%s set their agents' joining in %s to %s", by, proj, p.str("agent_join"))
		}
		return fmt.Sprintf("%s set their agents' joining to %s", by, p.str("agent_join"))
	case "membership.granted", "membership.changed", "membership.removed", "membership.requested", "membership.invited":
		return membershipSentence(e, p, by, who)
	case "lock.acquired":
		return fmt.Sprintf("%s took lock %s%s", by, p.str("name"), parens(p.str("purpose")))
	case "lock.released":
		if from, ok := p.actor("holder"); ok {
			return fmt.Sprintf("%s force-released %s's lock %s", by, who(from), p.str("name"))
		}
		return fmt.Sprintf("%s released lock %s", by, p.str("name"))
	case "lock.expired":
		return fmt.Sprintf("%s's lock %s expired", by, p.str("name"))
	case "identity.linked":
		return fmt.Sprintf("%s linked %s to their account", by, identityWords(p))
	case "identity.unlinked":
		return fmt.Sprintf("%s unlinked %s from their account", by, identityWords(p))
	case "user.deleted":
		return fmt.Sprintf("%s deleted their account", by)
	case "identity.signin_failed":
		return fmt.Sprintf("a wrong password was entered for %s's login", by)
	case "identity.signin_refused":
		return fmt.Sprintf("repeated wrong passwords were entered for %s's login, up to a sign-in limit; tries over it are refused for now", by)
	case "token.rotated":
		return fmt.Sprintf("%s's token %s was rotated", by, p.str("prefix"))
	case "token.reuse_detected":
		return fmt.Sprintf("a rotated token of %s (%s) was used again and has been revoked", by, p.str("prefix"))
	}
	return fmt.Sprintf("%s %s %s", by, e.Type, subjectRef(e.Subject))
}

// identityWords names the sign-in an identity event is about.
func identityWords(p payload) string {
	if p.str("provider") == "github" {
		return "GitHub account " + p.str("login")
	}
	return fmt.Sprintf("%s sign-in %s", p.str("provider"), p.str("login"))
}

// decisionStatusSentence words a status change of a decision card. A plain
// accept (ready to done) is told apart by kind alone; events stored before the
// server sent kind are still recognised by their rejected, revoked or
// superseded drop reason. An agent's verdict names the person it was given
// for.
func decisionStatusSentence(p payload, by, card string, who func(apitypes.Actor) string) (string, bool) {
	reason := p.str("drop_reason")
	isDecision := p.str("kind") == "decision" || reason == "rejected" || reason == "revoked" || reason == "superseded"
	if !isDecision {
		return "", false
	}
	var s string
	switch {
	case reason == "superseded":
		s = fmt.Sprintf("%s superseded decision %s", by, card)
		if n, ok := p.int("superseded_by"); ok {
			s += fmt.Sprintf(" with #%d", n)
		}
	case reason == "rejected", reason == "revoked":
		s = fmt.Sprintf("%s %s decision %s", by, reason, card)
	case p.str("to") == "done":
		s = fmt.Sprintf("%s accepted decision %s", by, card)
	default:
		return "", false
	}
	if owner, ok := p.actor("for"); ok {
		s += " for " + who(owner)
	}
	if n, ok := p.int("cascade_of"); ok {
		s += fmt.Sprintf(", dropped with story #%d", n)
	}
	return s, true
}

func membershipSentence(e apitypes.Event, p payload, by string, who func(apitypes.Actor) string) string {
	target := e.Subject.ID
	if a, ok := p.actor("actor"); ok {
		target = who(a)
	}
	where := ""
	if slug := p.str("project"); slug != "" {
		where = " in project " + slug
	} else if e.Project != nil {
		where = " in project " + *e.Project
	}
	switch e.Type {
	case "membership.requested":
		return fmt.Sprintf("%s asked to join the org", by)
	case "membership.invited":
		return fmt.Sprintf("%s invited %s as %s", by, target, p.str("role"))
	case "membership.granted":
		if _, agent := p["rights"]; agent {
			return fmt.Sprintf("%s added %s with %s%s", by, target, grantWords(p), where)
		}
		return fmt.Sprintf("%s added %s as %s%s", by, target, p.str("role"), where)
	case "membership.changed":
		if _, agent := p["rights"]; agent {
			return fmt.Sprintf("%s gave %s %s%s", by, target, grantWords(p), where)
		}
		return fmt.Sprintf("%s made %s %s%s", by, target, p.str("role"), where)
	}
	return fmt.Sprintf("%s removed %s%s", by, target, strings.Replace(where, " in ", " from ", 1))
}

// grantWords says what a join or membership event gave: the agent's rights,
// or the role for an event stored before agents carried rights.
func grantWords(p payload) string {
	if _, ok := p["rights"]; !ok {
		return "role " + p.str("role")
	}
	if rights := p.list("rights"); len(rights) > 0 {
		return "rights " + strings.Join(rights, ", ")
	}
	return "no rights beyond reading"
}

// parens renders non-empty parts as ` (a, b)`, or "" when all are empty.
func parens(parts ...string) string {
	var keep []string
	for _, s := range parts {
		if s != "" {
			keep = append(keep, s)
		}
	}
	if len(keep) == 0 {
		return ""
	}
	return " (" + strings.Join(keep, ", ") + ")"
}

// eventLine is `3m ago  ana claimed #42 "Fix login"`.
func eventLine(p *cmdutil.Printer, e apitypes.Event) string {
	return p.Ago(e.CreatedAt) + "  " + eventSentence(e, p.Name)
}

// inSession names the session a claim was taken from, so a takeover between
// two chats of one agent reads as one.
func inSession(session string) string {
	if session == "" {
		return ""
	}
	return " in session " + session
}
