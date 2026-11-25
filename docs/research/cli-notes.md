# CLI notes: wire contract gaps and workarounds

Written by the CLI/client implementation. The server side may want to close these; none block the CLI.

1. `GET /inbox` has no `since` parameter. `lorvik inbox --since N` calls `GET /events?after=N` instead and never acks. If the server adds `since` to `/inbox`, the CLI can drop the workaround in `internal/cli/inbox.go`.
2. `POST .../answer` is documented as returning "Card, and Decision when decide=true (header Location of decision)". The client decodes the body as a Card and reads the `Location` header into `client.AnsweredCard.DecisionLocation`. If the body instead becomes `{card, decision}`, update `Client.AnswerCard`.
3. Lease calls (`claim`, `renew`, `release`) are sent without `version`; it is optional on the wire and a stale value would only cause spurious 409s. Version-gated calls (`PATCH`, `transition`, `answer`, note and decision `PATCH`) refetch first, as specified.
4. `lorvik start N` claims, then sends a `working` transition if the claim response is not already `working`. If the server's claim moves the card to `working` itself, the second call is skipped. Please confirm which behaviour the server implements.
5. `reopen` transitions a done or dropped card to `ready`. The CLI refuses non-closed cards client-side. The server state machine must allow done/dropped to ready.
6. Status codes drive exit codes: 404 is 3, 409 is 4 (version conflict and claim held by another actor), 401/403 is 5, 400/422 is 2. A claim conflict must therefore be a 409 with a `hint`.
7. Every authenticated response, including errors, should carry `X-Lorvik-Unread` and `X-Lorvik-Cursor`. A missing header is read as 0. `inbox` suppresses the notice because it reports the events itself.
8. `GET /search` returns no `remaining`, so `lorvik search` cannot print "... and N more". It prints at most `--limit` hits.
9. `GET /decisions` and `GET /notes` treat `project=org` as org-level only (per `apitypes` field docs); the CLI sends that literal for `--org`. For `GET /notes/{slug}` and `PATCH` the CLI omits `?project=` for org-level notes and sends the slug otherwise.
10. `-q` is the query shorthand on `ls` and `decisions ls`, so the global quiet flag uses `-Q` (`--quiet` unchanged). cobra panics on duplicate shorthands.
11. `LORVIK_API_URL` defaults to `http://localhost:8080` when nothing else sets it, matching `lorvikd`'s default listen address.
