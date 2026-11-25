package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

type claimOp func(ctx context.Context, c *client.Client, slug string, n int64, in apitypes.ClaimCard) (apitypes.Card, error)

// leaseCmd builds claim, heartbeat and release. Lease calls carry no version:
// it is optional on the wire and a stale one would only cause needless 409s
// on an operation that is already guarded by the claim itself.
func leaseCmd(a *app, use, short, long string, op claimOp) *cobra.Command {
	var lease string
	cmd := &cobra.Command{
		Use: use + " <number>", Short: short, Long: long,
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
			card, err := op(cmd.Context(), c, slug, n, apitypes.ClaimCard{Lease: lease})
			if err != nil {
				return err
			}
			return a.emitCard(cmd.Context(), card)
		},
	}
	if use != "release" {
		cmd.Flags().StringVar(&lease, "for", "", forHelp)
	}
	return cmd
}

// forHelp describes --for on claim, start and heartbeat.
const forHelp = "How long the claim lasts without a renewal, such as 1h (1m to 24h); default the project's lease"

func newClaim(a *app) *cobra.Command {
	return leaseCmd(a, "claim", "Take a lease on a card before working on it",
		`Claim a card so nobody else works on it. The lease lasts 30 minutes by
default and is renewed by any request you make once half of it has passed, and by 'heartbeat'.
--for states a longer or shorter lease for this one claim, to cover a step
that makes no request for a while; later renewals keep that length.
Fails with exit 4 if another actor holds it.

Examples:
  lorvik claim 42
  lorvik claim 42 --for 1h`,
		func(ctx context.Context, c *client.Client, s string, n int64, in apitypes.ClaimCard) (apitypes.Card, error) {
			return c.ClaimCard(ctx, s, n, in)
		})
}

func newHeartbeat(a *app) *cobra.Command {
	return leaseCmd(a, "heartbeat", "Renew your lease on a card",
		`Renew the lease on a card you hold. Rarely needed: most commands and
'lorvik inbox' renew it implicitly. --for states a new length for the claim:
the lease then ends exactly that long from now, and later renewals keep it.
Use it before one step that will make no request for longer than the lease.

Examples:
  lorvik heartbeat 42
  lorvik heartbeat 42 --for 1h   (before a 40-minute build)`,
		func(ctx context.Context, c *client.Client, s string, n int64, in apitypes.ClaimCard) (apitypes.Card, error) {
			return c.RenewCard(ctx, s, n, in)
		})
}

func newRelease(a *app) *cobra.Command {
	return leaseCmd(a, "release", "Give up your lease on a card",
		`Release a card you hold so someone else can take it. Leave a comment
first ('lorvik comment N -m ...') if you made progress.

Example:
  lorvik release 42`,
		func(ctx context.Context, c *client.Client, s string, n int64, in apitypes.ClaimCard) (apitypes.Card, error) {
			return c.ReleaseCard(ctx, s, n, in)
		})
}
