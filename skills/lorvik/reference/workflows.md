# Worked sessions

Transcripts of the story-workflow CLI against a lorvikd server. `$` lines are commands; everything else is output. Times are relative (`now`, `3m ago`) and names are resolved from actor ids.

## A. Pick up work and finish it

```
$ lorvik status
lorvik  Lorvik
session witty-lynx-3f9a (claude-code)
visibility org  lease 30m0s  status active  rights work
backlog 1  ready 3  working 0  waiting 0  done 0  dropped 0
open questions: 0
my cards (this session): none
sessions holding cards: none

$ lorvik inbox
now  Ana created task #1 "Return problem+json hint on 409 claim conflicts" (ready, high)
now  Ana created note errors "Error handling"
now  Ana accepted decision #2 "Errors are RFC 9457 problem+json with a hint field"

$ lorvik next
#1  ready  high  Return problem+json hint on 409 claim conflicts
created now by Ana  updated now  version 1

Claim conflicts return a bare 409 today.

$ lorvik start 1
#1  working  high  Return problem+json hint on 409 claim conflicts  @gemini-bot

$ lorvik show 1
#1  working  high  Return problem+json hint on 409 claim conflicts  @gemini-bot
created now by Ana  updated now  version 2  lease in 29m

Claim conflicts return a bare 409 today.

Recent events:
  now  gemini-bot claimed #1 "Return problem+json hint on 409 claim conflicts"
  now  Ana created task #1 "Return problem+json hint on 409 claim conflicts" (ready, high)
```

Read the code, check for prior choices, implement.

```
$ lorvik decisions search "error format"
#2  done  normal  D Errors are RFC 9457 problem+json with a hint field

$ go test ./internal/server/...
ok

$ lorvik comment 1 -m "Done: hint names the claimant and lorvik next. Left: nothing. Gotcha: golden file regenerated."
comment added to #1

$ lorvik done 1 -m "hint added, tests green" --into docs/requirements/errors.md
#1  done  high  Return problem+json hint on 409 claim conflicts

$ lorvik note search "error handling"
errors  Error handling  @Ana  now  (lorvik)
$ lorvik note edit errors -b "..."   # only for a runbook or gotcha

$ lorvik next
```

`next` only shows the card; `start` is what claims it. A child task has no documentation step: the story it belongs to folds the result into the requirements documents when it closes (workflow E). A standalone card is finished with the fold in the same call: `lorvik done 1 -m "..." --into <path>` or `--no-contract-change`; plain `lorvik fold` lists the ones left unfolded.

## B. Blocked, ask, resume next session

Session 1. You hold #4 and hit a choice you cannot make alone.

```
$ lorvik decisions search "webhook queue"
no decisions

$ lorvik ask "Queue backend for webhook delivery?" \
    --choice "Postgres table + SKIP LOCKED" --choice "Redis streams" \
    -b "Redis adds a self-host dependency. Postgres caps throughput near 1k/s. #4 cannot proceed without this." \
    --blocks 4
#5  ready  normal  ? Queue backend for webhook delivery?

$ lorvik comment 4 -m "Done: sender interface and retry policy. Left: delivery queue, waiting on #5. Gotcha: none."
comment added to #4

$ lorvik next
#3  ready  normal  Add HMAC signing to webhook payloads
parent #2  created now by Ana  updated now  version 1

$ lorvik start 3
#3  working  normal  Add HMAC signing to webhook payloads  @gemini-bot
```

`--blocks 4` moved #4 to `waiting` and released your claim on it. Do not wait for #5; continue with #3. The human answers on the dashboard with "Postgres table + SKIP LOCKED" and the decide option.

Session 2, later, new context.

```
$ lorvik status
lorvik  Lorvik
session witty-lynx-3f9a (claude-code)
visibility org  lease 30m0s  status active  rights work
backlog 2  ready 1  working 0  waiting 1  done 2  dropped 0
open questions: 0
my cards (this session): none
sessions holding cards: none
3 unread, run lorvik inbox

$ lorvik inbox
now  Ana answered #5 "Queue backend for webhook delivery?": Postgres table + SKIP LOCKED (recorded as a decision)
now  Ana recorded decision #6 "Queue backend for webhook delivery?" from #5
now  Ana moved #4 "Webhook delivery queue" from waiting to ready, unblocked by #5 "Queue backend for webhook delivery?"

$ lorvik decisions show 6
#6  done  normal  D Queue backend for webhook delivery?
relates #5  accepted by Ana now  version 1

Context:
  Redis adds a self-host dependency. Postgres caps throughput near 1k/s.

Decision:
  Postgres table + SKIP LOCKED

Links:
  relates #5  Queue backend for webhook delivery?  (done)

$ lorvik start 4
#4  working  normal  Webhook delivery queue  @gemini-bot

$ lorvik show 4
#4  working  normal  Webhook delivery queue  @gemini-bot
parent #2  created 1m ago by Ana  updated now  version 5  lease in 29m

Links:
  blocked by #5  Queue backend for webhook delivery?  (done)

Comment (latest): @gemini-bot now
  Done: sender interface and retry policy. Left: delivery queue, waiting on #5. Gotcha: none.

Recent events:
  now  gemini-bot claimed #4 "Webhook delivery queue"
  now  Ana moved #4 "Webhook delivery queue" from waiting to ready, unblocked by #5 "Queue backend for webhook delivery?"
  now  gemini-bot commented on #4 "Webhook delivery queue"
  now  gemini-bot moved #4 "Webhook delivery queue" from working to waiting, blocked by #5 "Queue backend for webhook delivery?"
  now  gemini-bot claimed #4 "Webhook delivery queue"
```

The answer returned #4 to `ready`, not `working`, so `start` re-claims it. A closed blocker stays listed under `Links:` with its status. Implement per #6, then finish as in session A.

## B2. Propose a decision and apply feedback

You chose a pgx pool while working #4 and record it.

```
$ lorvik decide "Use a pgx pool for Postgres access" -m "pgxpool, no database/sql." --context "Webhook queue needs SKIP LOCKED." --consequences "No driver portability." --parent 2
#7  ready  normal  D Use a pgx pool for Postgres access  @gemini-bot

$ lorvik next
```

The decision is a card under story #2, proposed (`ready`), and a human reviewer accepts it on the board. `next` never returns it and it cannot be claimed. Move on; do not poll. Later, in the same or a new session:

```
$ lorvik inbox
now  Ana commented on #7 "Use a pgx pool for Postgres access"

$ lorvik comment 7
Ana  now
Say why not database/sql.

$ lorvik decisions edit 7 --consequences "No driver portability; database/sql cannot use SKIP LOCKED batching."
```

A comment is a request for changes, so edit the decision; the old text stays in `lorvik history 7`. Never accept or reject it yourself (exit 5).

```
$ lorvik inbox
now  Ana accepted decision #7 "Use a pgx pool for Postgres access"
```

Accepted means #7 is done and in force. Story #2 cannot close while #7 is proposed. Event wording here is illustrative; the exact sentences come from the server.

## C. Discover a bug mid-task and file it

You hold #3 (parent #2) and notice the sweeper compares with `<` where it needs `<=`.

```
$ lorvik ls --status backlog
#2  backlog  normal  Webhook delivery

$ lorvik add "Fix off-by-one in claim sweeper expiry comparison" \
    -b "internal/card/sweeper.go:88 uses < now, so a claim expiring exactly now survives one extra tick. Found while on #3. Repro: unit test with expires_at == now." \
    --parent 2
#6  backlog  normal  Fix off-by-one in claim sweeper expiry comparison

$ lorvik link 3 relates 6
#3 relates #6
```

No duplicate existed, so the bug is filed under the same story and linked. Keep working #3. Do not fix #6 inside #3 unless it is a one-line change that blocks you. If it does block you:

```
$ lorvik link 6 blocks 3
#6 blocks #3
$ lorvik start 6
lorvik: Conflict: invalid status transition: card #6 is still in the backlog
hint: move it to ready first with `lorvik ready <n>`
$ lorvik ready 6
#6  ready  normal  Fix off-by-one in claim sweeper expiry comparison
$ lorvik start 6
#6  working  normal  Fix off-by-one in claim sweeper expiry comparison  @gemini-bot
```

A card you just filed is in `backlog`, and `start` only takes `ready` or `waiting` cards, hence the `ready` step. Handle #6 as its own card: fix, comment, done. Never change story #2's status.

## D. What errors look like

```
$ lorvik heartbeat 3
lorvik: Forbidden: caller does not hold the claim: #3
hint: claim the card first with `lorvik claim <n>`

$ lorvik answer 5 "Postgres"
lorvik: Forbidden: forbidden: agent gemini-bot lacks the answer right in lorvik
hint: its owner can give it on the Access page, to the agent or to this chat (witty-lynx-3f9a); or relay the human's answer: lorvik answer 5 "<answer>" --relayed -m "<where they said it>"

$ lorvik show 99
lorvik: Not Found: card #99: not found
hint: check the number or slug; `lorvik ls` shows what exists
```

Errors go to stderr with a `hint:` line that names the fixing command.

## E. Story: split, fan out, review, close

The human hands you a piece of work: webhook delivery. You open a story, split it, run the children, fold the result into the requirements documents and close it. Event wording below is illustrative; the exact sentences come from the server.

```
$ lorvik decisions search "webhook"
#3  done  normal  D Postgres table with SKIP LOCKED for the delivery queue

$ lorvik add "Webhook delivery" --kind story -b "As an integrator, I want signed, retried webhook deliveries, so that I can trust and replay events; done when payloads carry X-Signature, failed sends retry, and the runbook says how to replay." --lane webhooks --ready
#20  ready  normal  S Webhook delivery

$ lorvik start 20
#20  working  normal  S Webhook delivery  @gemini-bot
```

Children go under it with `--parent`; two levels only. `add` has no assignee flag, so assign with `edit`.

```
$ lorvik add "Add HMAC signing to webhook payloads" -b "internal/webhook/sign.go. Done when payloads carry X-Signature and a unit test pins it." --parent 20 --priority high --ready
#21  ready  high  Add HMAC signing to webhook payloads
$ lorvik add "Add retry policy to webhook sender" -b "internal/webhook/send.go. Exponential backoff, five tries. Done when a table test pins the schedule." --parent 20 --ready
#22  ready  normal  Add retry policy to webhook sender
$ lorvik add "Document webhook delivery in the runbook note" -b "Runs after #21 and #22 land." --parent 20
#23  backlog  normal  Document webhook delivery in the runbook note
$ lorvik link 21 blocks 23
#21 blocks #23
$ lorvik link 22 blocks 23
#22 blocks #23
$ lorvik edit 23 --assignee user:106666050296328877
#23  backlog  normal  Document webhook delivery in the runbook note

$ lorvik ls --story 20
#21  ready  high  Add HMAC signing to webhook payloads
#22  ready  normal  Add retry policy to webhook sender
#23  backlog  normal  Document webhook delivery in the runbook note
```

Fan out. `lorvik status` shows `concurrency parallel` and the harness can spawn subagents, so start one per ready child. Each subagent runs the Work a card and Finish workflows on its own card and you do not touch their branches. Each subagent prompt is: "Run `lorvik start 21`, read `lorvik comment 21` and `lorvik inbox`, work on branch `card-21-hmac-signing`, rebase, then `lorvik comment 21 -m ...` and `lorvik done 21 -m ...`."

If `concurrency` is `serial` or no subagents can be spawned, work the children yourself in order with `lorvik next` and `lorvik start`.

While they work, do not poll. Run `lorvik inbox` when the unread notice appears.

```
$ lorvik inbox
now  claude-sub-1 claimed #21 "Add HMAC signing to webhook payloads"
now  claude-sub-2 claimed #22 "Add retry policy to webhook sender"
now  claude-sub-1 commented on #21
now  claude-sub-1 marked #21 done
now  claude-sub-2 commented on #22
now  claude-sub-2 marked #22 done
```

Review each child against its scope.

```
$ lorvik comment 22
claude-sub-2  now
Done: five tries, 1s to 16s backoff. Left: jitter. Gotcha: the table test fakes the clock.

$ lorvik reopen 22 -m "jitter is in the scope; add it"
#22  ready  normal  Add retry policy to webhook sender
```

Jitter was in #22's scope, so the card goes back to `ready` for a subagent or for you. After the fix, #23 is unblocked. Do it. The story closes only when every child is `done` or `dropped`; a try before that is refused.

```
$ lorvik done 20 -m "webhook delivery shipped" --into docs/requirements/webhooks.md
lorvik: Conflict: story #20 has open children: #22
hint: finish or drop them, then run `lorvik fold 20`
```

All children settled. Fold prints the brief; read it, then update the documents.

```
$ lorvik fold 20
#20  working  normal  S Webhook delivery  progress 3/3
lane webhooks

Children:
  #21  done  high  Add HMAC signing to webhook payloads
       hmac signing, X-Signature header, tests green
       Done: signing in sign.go. Left: nothing. Gotcha: header is hex, lowercase.
  #22  done  normal  Add retry policy to webhook sender
       five tries with jitter
       Done: backoff 1s to 16s plus jitter. Left: nothing. Gotcha: the table test fakes the clock.
  #23  done  normal  Document webhook delivery in the runbook note
       runbook note written

Decisions:
  #3  done  normal  D Postgres table with SKIP LOCKED for the delivery queue
       Context: Redis adds a self-host dependency. Decision: Postgres table + SKIP LOCKED. Consequences: throughput caps near 1k/s.

Suggested documents:
  docs/requirements/webhooks.md  (missing, create from docs/requirements/README.md)

$ $EDITOR docs/requirements/webhooks.md   # signing, retry schedule, queue choice and why

$ lorvik done 20 -m "webhook delivery shipped across #21 to #23" --into docs/requirements/webhooks.md
#20  done  normal  S Webhook delivery
```

The fold is recorded on the story and on every child. Use `--no-contract-change` instead of `--into` when nothing durable changed. A closed story takes no new children and is never reopened: a later fix is a new story, linked `relates`.

```
$ lorvik add "Webhook replay" --kind story -b "As an integrator, I want to replay a failed delivery, so that ...; done when ..." --lane webhooks --ready
#30  ready  normal  S Webhook replay
$ lorvik link 30 relates 20
#30 relates #20
```

If the work is abandoned, `lorvik drop 20 --reason wontfix --cascade -m "why"` drops the story and its open children; a dropped story is not folded. A standalone card normally folds in its `done` call (`lorvik done 31 -m "..." --no-contract-change`); one left unfolded is cleared on its own:

```
$ lorvik fold
#31  done  normal  Fix typo in README quickstart  (closed 2h ago)
$ lorvik fold 31 --no-contract-change
#31  done  normal  Fix typo in README quickstart  folded
```

Only the agent that runs the story closes it. A child agent never changes the story's status.
