# API implementation notes

Deviations and assumptions made while implementing `internal/server` against the contract files. None of the contract files were changed.

## Contract gaps worked around

- `apitypes.ListCardsParams.Parent` and the SSE `after` parameter: huma cannot bind pointer query parameters (it panics at registration). The server declares its own input struct with `parent` as a plain number (0 means unset) and `after` as a number where `-1` (the default) means "start at the current head". The wire format is unchanged for clients that omit the parameters.
- `domain` has no "already exists" sentinel. The service pre-checks slug and name uniqueness (project slug, note slug, agent name) and returns 409 through a server-local `errExists`. A race still surfaces the store's unique-violation, which `internal/store` maps to a `ValidationError` (422). Suggest adding `domain.ErrAlreadyExists` mapped to 409 in a later pass.
- `store.RenewClaims` is called on every authenticated request with `now + domain.DefaultLeaseDuration`. It must only extend a lease (never shorten one on a project with a longer lease). Project-specific lease lengths are applied on claim and explicit renew only.
- `Store` implementations are assumed to generate IDs when the incoming entity has an empty ID (the Postgres store does).
- Event `Seq` is treated as per org (as `store.go` documents), although plan 3.3 says per project. The unread header and cursors use the org-wide `LatestSeq`.
- `POST /cards/{n}/answer` returns the full card (with `decisions` filled) and sets `Location: /api/v1/decisions/{n}` when `decide=true`. There is no combined Card+Decision body type in `apitypes`.
- Claim renewal (`/renew`) writes the card but emits no event, to keep the inbox quiet. The sweeper attributes `card.claim_expired` to the former claimant so it appears in that actor's inbox.
- Errors raised by huma itself (malformed JSON, schema validation of request bodies) use huma's default problem body: `errors[]` entries carry `message` and `location`, not `field`. Domain validation errors use `field` and `message`.
- huma's `$schema` body field and `Link` header are disabled (`CreateHooks = nil`) so response bodies match `apitypes` exactly.
