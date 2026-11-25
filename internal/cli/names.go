package cli

import (
	"context"
	"slices"
	"strings"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

// nameOf is the cmdutil.NameFunc over the per-process cache. It returns ""
// for actors that were not resolved, and the printer falls back to the id.
func (a *app) nameOf(act apitypes.Actor) string { return a.names[act.ID] }

// resolve looks up the names of actors that are not cached yet, in one batch
// request. It does nothing under --json, which prints ids as the API sent
// them. A failed lookup is not an error: names are decoration, and the
// printer falls back to ids.
func (a *app) resolve(ctx context.Context, actors ...apitypes.Actor) {
	if a.asJSON || a.cl == nil {
		return
	}
	var missing []string
	for _, act := range actors {
		if act.ID == "" || act.ID == "0" {
			continue
		}
		if _, ok := a.names[act.ID]; !ok && !slices.Contains(missing, act.ID) {
			missing = append(missing, act.ID)
		}
	}
	if len(missing) == 0 {
		return
	}
	if a.names == nil {
		a.names = map[string]string{}
	}
	got, err := a.cl.Actors(ctx, missing)
	if err != nil {
		return
	}
	for _, u := range got.Users {
		a.names[u.ID] = userName(u)
	}
	for _, ag := range got.Agents {
		a.names[ag.ID] = ag.Name
	}
}

// userName prefers the display name and falls back to the login.
func userName(u apitypes.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Login
}

func ptrActor(a *apitypes.Actor) []apitypes.Actor {
	if a == nil {
		return nil
	}
	return []apitypes.Actor{*a}
}

func briefActors(b apitypes.CardBrief) []apitypes.Actor {
	var out []apitypes.Actor
	if b.Claim != nil {
		out = append(out, b.Claim.Actor)
	}
	return append(out, ptrActor(b.Assignee)...)
}

func briefsActors(bs []apitypes.CardBrief) []apitypes.Actor {
	var out []apitypes.Actor
	for _, b := range bs {
		out = append(out, briefActors(b)...)
	}
	return out
}

func cardActors(c apitypes.Card) []apitypes.Actor {
	out := append([]apitypes.Actor{c.CreatedBy}, briefActors(cardBrief(c))...)
	if c.Question != nil {
		out = append(out, ptrActor(c.Question.AnsweredBy)...)
	}
	if c.Behalf != nil {
		out = append(out, c.Behalf.For)
	}
	if c.Comment != nil {
		out = append(out, c.Comment.Actor)
	}
	out = append(out, briefsActors(c.Children)...)
	out = append(out, briefsActors(c.BlockedBy)...)
	return append(out, eventsActors(c.Events)...)
}

func cardBrief(c apitypes.Card) apitypes.CardBrief {
	return apitypes.CardBrief{Claim: c.Claim, Assignee: c.Assignee}
}

// parseActor reads `user:ID`, `agent:ID` or a bare id. A bare id takes
// defaultKind; pass "" to make a bare id a usage error.
func parseActor(s, defaultKind string) (apitypes.Actor, error) {
	s = strings.TrimSpace(s)
	kind, id, ok := strings.Cut(s, ":")
	if !ok {
		kind, id = defaultKind, s
	}
	if kind != "user" && kind != "agent" || !isID(id) {
		return apitypes.Actor{}, cmdutil.Usagef("%q is not an actor: use user:<id> or agent:<id>", s)
	}
	return apitypes.Actor{Kind: kind, ID: id}, nil
}

// isID reports whether s looks like a snowflake id: decimal digits only.
func isID(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

// lookupActor finds the kind of a bare id through GET /actors.
func (a *app) lookupActor(ctx context.Context, c *client.Client, id string) (apitypes.Actor, error) {
	got, err := c.Actors(ctx, []string{id})
	if err != nil {
		return apitypes.Actor{}, err
	}
	if slices.ContainsFunc(got.Users, func(u apitypes.User) bool { return u.ID == id }) {
		return apitypes.Actor{Kind: "user", ID: id}, nil
	}
	if slices.ContainsFunc(got.Agents, func(ag apitypes.Agent) bool { return ag.ID == id }) {
		return apitypes.Actor{Kind: "agent", ID: id}, nil
	}
	return apitypes.Actor{}, cmdutil.Usagef("no user or agent with id %s in this org; write user:<id> or agent:<id>", id)
}

func actorsOf[T any](items []T, actor func(T) apitypes.Actor) []apitypes.Actor {
	out := make([]apitypes.Actor, 0, len(items))
	for _, item := range items {
		out = append(out, actor(item))
	}
	return out
}

// assigneeRef turns the --assignee value into the wire actor. An empty value
// is an actor with an empty id, which clears the assignee.
func (a *app) assigneeRef(ctx context.Context, c *client.Client, s string) (apitypes.Actor, error) {
	s = strings.TrimSpace(s)
	switch s {
	case "":
		return apitypes.Actor{Kind: "user"}, nil
	case "me":
		me, err := c.Me(ctx)
		if err != nil {
			return apitypes.Actor{}, err
		}
		return me.Actor, nil
	}
	if strings.Contains(s, ":") {
		return parseActor(s, "")
	}
	if !isID(s) {
		return apitypes.Actor{}, cmdutil.Usagef("--assignee %q: want an actor id, user:<id> or agent:<id>", s)
	}
	return a.lookupActor(ctx, c, s)
}
