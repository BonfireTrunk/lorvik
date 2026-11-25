package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

// moveCmd builds a command that refetches the card and transitions it to
// status with an optional -m note. check may veto based on the current card.
func moveCmd(a *app, use, short, long, status string, check func(apitypes.Card) error) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use: use + " <number>", Short: short, Long: long,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			return a.mutateCard(cmd.Context(), n, func(c *client.Client, slug string, cur apitypes.Card) (apitypes.Card, error) {
				if check != nil {
					if err := check(cur); err != nil {
						return apitypes.Card{}, err
					}
				}
				return c.TransitionCard(cmd.Context(), slug, n, apitypes.TransitionCard{Version: cur.Version, Status: status, Note: note})
			})
		},
	}
	cmd.Flags().StringVarP(&note, "message", "m", "", "Short note recorded on the event")
	return cmd
}

func newWait(a *app) *cobra.Command {
	cmd := moveCmd(a, "wait", "Mark a card waiting", `Mark a card waiting, with a note saying on what. Prefer 'lorvik ask --blocks'
when a human's answer is what you are waiting for. Waiting cards return to ready
by themselves when the questions blocking them are answered.

Example:
  lorvik wait 42 -m "needs staging credentials"`, "waiting", nil)
	cmd.Aliases = []string{"block"}
	return cmd
}

func newDone(a *app) *cobra.Command {
	var (
		note, into string
		noContract bool
	)
	cmd := &cobra.Command{
		Use:   "done <number>",
		Short: "Mark a card done",
		Long: `Mark a card done. Leave a comment first if the next person needs context.
If the card is not working under your claim (for example ready after an answer),
it is claimed for you first; a card held by someone else is never taken over.
If your own claim on it had run out meanwhile, a line on stderr says so.
When the server cannot be reached, 'done' waits in the queue, and 'lorvik sync'
or the next command sends it.

A story closes with its fold: pass --into <path> (the requirements document
you updated) or --no-contract-change. Every child must be done or dropped; the
server refuses otherwise and its hint names the open ones. Run 'lorvik fold N'
first to get the brief. A closed story is never reopened.

A card without a parent may carry the same fold, so it is closed and folded in
one call; a card with a parent is folded by its story and is refused one.

Examples:
  lorvik done 42 -m "merged in #318"
  lorvik done 20 -m "webhook delivery shipped" --into docs/requirements/webhooks.md
  lorvik done 21 --no-contract-change
  lorvik done 43 -m "retry limit raised" --into docs/requirements/delivery.md`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			fold, err := foldArg(into, noContract)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			key := newRequestKey()
			e := outboxEntry{ID: key, Command: cmdDone, Card: n, Note: note, Into: into, NoContractChange: noContract}
			card, queued, err := attempt(ctx, a, e, func(c *client.Client, slug string) (apitypes.Card, error) {
				return a.doneCard(ctx, c, slug, n, note, fold, key, a.session(), false)
			})
			if err != nil || queued {
				return err
			}
			return a.emitCard(ctx, card)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&note, "message", "m", "", "Short note recorded on the event")
	f.StringVar(&into, "into", "", "Requirements document the result was folded into (required for a story; allowed on a card without a parent)")
	f.BoolVar(&noContract, "no-contract-change", false, "Fold into nothing: no durable change (required for a story, or --into; allowed on a card without a parent)")
	return cmd
}

// doneCard is what done does: read the card, claim it first when it is not
// held under this session's claim, then close it under key. The outbox runs
// it again for a queued done, with the session the entry was made in and
// replayed set.
func (a *app) doneCard(ctx context.Context, c *client.Client, slug string, n int64, note string, fold *apitypes.Fold, key, session string, replayed bool) (apitypes.Card, error) {
	cur, err := c.GetCard(ctx, slug, n)
	if err != nil {
		return apitypes.Card{}, err
	}
	mine := holdsClaim(cur)
	// Only tasks and questions are claimed; a story or decision has no lease.
	claimable := cur.Kind != "story" && cur.Kind != "decision"
	needsClaim := claimable && !mine && (cur.Status == "ready" || cur.Status == "waiting" || cur.Status == "working")
	if replayed && needsClaim {
		// The first send may have closed the card before its reply was lost,
		// and somebody reopened it since. The server answers a repeated key
		// with the card before it looks at anything else, so the key goes
		// first: claiming first would take the reopened card, or be refused
		// for one somebody else took. A key the server has not seen is
		// refused here for the missing claim and stays free.
		moved, err := c.TransitionCard(client.WithRequestKey(ctx, key), slug, n, apitypes.TransitionCard{Version: cur.Version, Status: "done", Note: note, Fold: fold})
		if _, refused := a.refusalOf(err); !refused {
			return moved, err
		}
	}
	if needsClaim {
		// Claim moves ready and waiting cards to working. A live claim by
		// another actor makes the server answer 409, which we surface.
		ranOut, lapsed := lapsedClaim(cur, session)
		claimed, err := c.ClaimCard(ctx, slug, n, apitypes.ClaimCard{})
		if err != nil {
			return apitypes.Card{}, err
		}
		if lapsed && claimed.Claim != nil && claimed.Claim.Actor == ranOut.by {
			fmt.Fprintf(a.errOut(), "lorvik: your claim on #%d ran out %s and nobody has taken the card since; claimed it again before marking it done\n", n, a.printer().Ago(ranOut.at))
		} else {
			fmt.Fprintf(a.errOut(), "lorvik: claimed #%d before marking it done\n", n)
		}
		cur = claimed
	}
	return c.TransitionCard(client.WithRequestKey(ctx, key), slug, n, apitypes.TransitionCard{Version: cur.Version, Status: "done", Note: note, Fold: fold})
}

// foldArg turns --into and --no-contract-change into the wire fold. Neither
// flag is nil; the server decides whether the card needed one.
func foldArg(into string, none bool) (*apitypes.Fold, error) {
	switch {
	case into != "" && none:
		return nil, cmdutil.Usagef("--into and --no-contract-change are exclusive: pass one")
	case into != "":
		return &apitypes.Fold{Into: into}, nil
	case none:
		return &apitypes.Fold{None: true}, nil
	}
	return nil, nil
}

// lapse is a claim that ran out: whose it was and when.
type lapse struct {
	by apitypes.Actor
	at time.Time
}

// lapsedClaim reports the claim this chat held on the card and lost to its
// lease, when nobody has claimed or released the card since: either the
// lapsed claim is still on the card, or the last claim event among the
// card's recent ones is its expiry, in this session for an agent and in any
// for a person, whose claim is theirs wherever it was taken. The caller
// checks the actor, which the CLI learns only from its own next claim.
func lapsedClaim(cur apitypes.Card, session string) (lapse, bool) {
	if cl := cur.Claim; cl != nil {
		return lapse{cl.Actor, cl.ExpiresAt}, cl.Owned && !cl.Mine
	}
	var last *apitypes.Event
	for i, e := range cur.Events {
		switch e.Type {
		case "card.claimed", "card.released", "card.claim_expired":
			if last == nil || e.CreatedAt.After(last.CreatedAt) {
				last = &cur.Events[i]
			}
		}
	}
	if last == nil || last.Type != "card.claim_expired" {
		return lapse{}, false
	}
	if last.Actor.Kind == "agent" && decodePayload(*last).str("session") != session {
		return lapse{}, false
	}
	at := last.CreatedAt
	if t, err := time.Parse(time.RFC3339, decodePayload(*last).str("expired_at")); err == nil {
		at = t
	}
	return lapse{last.Actor, at}, true
}

// holdsClaim reports whether the caller holds the live claim on a working card;
// the server decides, including the agent-session rule.
func holdsClaim(cur apitypes.Card) bool {
	return cur.Status == "working" && cur.Claim != nil && cur.Claim.Mine
}

func newReady(a *app) *cobra.Command {
	return moveCmd(a, "ready", "Make a card pullable by 'next'", `Move a backlog or waiting card to ready so 'lorvik next' can offer it.

Example:
  lorvik ready 42`, "ready", nil)
}

func newReopen(a *app) *cobra.Command {
	return moveCmd(a, "reopen", "Reopen a done or dropped card", `Move a done or dropped card back to ready.

Example:
  lorvik reopen 42 -m "regressed in v0.3"`, "ready", func(c apitypes.Card) error {
		if c.Status != "done" && c.Status != "dropped" {
			return cmdutil.Usagef("card #%d is %s, not closed; nothing to reopen", c.Number, c.Status)
		}
		return nil
	})
}

func newDrop(a *app) *cobra.Command {
	var (
		reason, note string
		dup          int64
		cascade      bool
	)
	cmd := &cobra.Command{
		Use:   "drop <number>",
		Short: "Drop a card with a reason",
		Long: `Close a card without doing it. --reason is required: obsolete, duplicate
or wontfix. A duplicate must name the surviving card with --dup. Dropping a
story with open children needs --cascade, which drops them too with the same
reason and releases their claims. A dropped story is not folded. Decisions are
dropped with 'lorvik decisions reject' or 'revoke'.

Examples:
  lorvik drop 42 --reason wontfix -m "out of scope for v1"
  lorvik drop 43 --reason duplicate --dup 42
  lorvik drop 20 --reason wontfix --cascade -m "abandoned"`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			if reason == "" {
				return cmdutil.Usagef("--reason is required: obsolete, duplicate or wontfix")
			}
			if err := oneOf("reason", reason, "obsolete", "duplicate", "wontfix"); err != nil {
				return err
			}
			in := apitypes.TransitionCard{Status: "dropped", DropReason: reason, Note: note, Cascade: cascade}
			if reason == "duplicate" {
				if !cmd.Flags().Changed("dup") {
					return cmdutil.Usagef("--reason duplicate needs --dup <surviving card number>")
				}
				in.Duplicate = &dup
			}
			return a.mutateCard(cmd.Context(), n, func(c *client.Client, slug string, cur apitypes.Card) (apitypes.Card, error) {
				in.Version = cur.Version
				return c.TransitionCard(cmd.Context(), slug, n, in)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&reason, "reason", "", "obsolete, duplicate or wontfix (required)")
	f.Int64Var(&dup, "dup", 0, "Surviving card number when the reason is duplicate")
	f.StringVarP(&note, "message", "m", "", "Short note recorded on the event")
	f.BoolVar(&cascade, "cascade", false, "Drop a story's open children along with it")
	return cmd
}

func newStart(a *app) *cobra.Command {
	var branch, lease string
	cmd := &cobra.Command{
		Use:   "start <number>",
		Short: "Claim a card and mark it working",
		Long: `Claim the card; that already moves it to working, so this is one request.
Fails with exit 4 if another actor holds the lease. --branch records the git
branch the work is on, shown by 'show' and the dashboard. --for states how
long the claim lasts without a renewal, as on 'claim'.

Examples:
  lorvik start 42
  lorvik start 42 --branch card-42-lanes
  lorvik start 42 --for 1h`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			card, err := c.ClaimCard(cmd.Context(), slug, n, apitypes.ClaimCard{Lease: lease})
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("branch") {
				card, err = c.UpdateCard(cmd.Context(), slug, n, apitypes.UpdateCard{Version: card.Version, Branch: &branch})
				if err != nil {
					return err
				}
			}
			return a.emitCard(cmd.Context(), card)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Git branch the work is on")
	cmd.Flags().StringVar(&lease, "for", "", forHelp)
	return cmd
}
