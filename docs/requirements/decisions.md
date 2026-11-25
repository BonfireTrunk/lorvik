# Requirements: questions, answers and decisions

## 1. Purpose

Agents ask humans questions on the board and propose decisions; humans answer and accept them. This area covers question cards, answering (optionally recording the answer as a decision), and what is specific to decision cards: who proposes and accepts, the body template, supersession, `applies` links, and how the CLI and dashboard word and surface them. A decision is a card of kind decision: the state machine, the refusals and the cross-project rules are in `board.md` (sections 2.3 and 2.5) and are not repeated here. Roles, versioning, the error format and reference syntax: `docs/api.md` and `domain.minRole`.

Everything below is implemented unless marked "unverified" or listed under Known defects. Intended changes: none.

## 2. Behaviour

### 2.1 Question cards

- A question is a card with `kind=question`, created by `POST /projects/{slug}/cards` (CLI `ask` or `add --kind question`; the dashboard add form). It defaults to `ready` and accepts `backlog` or `ready` at creation. It may sit under a story and is not a parent.
- `choices`: none, or at least two (422 on `choices` otherwise). Choices on any other kind are 422. Choices are suggestions: any non-blank text is a valid answer. They cannot be edited after creation.
- `blocks` (questions only; other kinds 422 on `blocks`): each number must exist in the project (422). Each target gets a `blocks` link from the question. A target in `ready` or `working` moves to `waiting` and loses its claim; a target in another status, or of kind decision, only gets the link (`TestQuestionWithBlocksParksClaimedTargetInWaiting`; `blockCard`). The link can be added with `link-cards` and removed later.
- The body template is a convention (`skills/lorvik/reference/commands.md`, "Question template"): Context, one `### A: label` section per option with Good and Bad, then Recommendation. `next` never returns a question.

### 2.2 Answering

`POST /projects/{slug}/cards/{n}/answer` with `{version, answer, decide?}`. Checks run in this order:

| Step | Refusal | Status |
|---|---|---|
| Project not visible | not found | 404 |
| Caller is an agent without the answer right that is not relaying (`board.md` 2.10) | forbidden, naming the right | 403 |
| Role below reviewer, or read-only token | forbidden | 403 |
| `decide` and role below decision-accept (the same role as answering, so never reached on its own) | forbidden | 403 |
| Stale `version` | `current_version` in the body | 409 |
| Card is not a question | field `kind` | 422 |
| Blank answer | field `answer` | 422 |
| Card already `done` or `dropped` | invalid transition | 409 |

Success: the question becomes `done` with the answer, `answered_by`, `answered_at` and `closed_at`; its claim is deleted; event `card.answered` with `answer` and `decided`. Every `waiting` card this question blocks and that has no other open blocker returns to `ready` (`card.status_changed` with `unblocked_by`); one with another open blocker stays waiting (`TestAnswerKeepsCardWaitingWhileAnotherBlockerIsOpen`). The same unblocking runs when any blocker is done or dropped, or a `blocks` link is removed. An empty `answer` string is rejected by the schema (`minLength`); its status is unverified, expected 422.

`decide=true` also creates a decision card in the same transaction (`decideFromAnswer`, `TestAnswerWithDecideCreatesAcceptedDecisionCardAndUnblocks`):

- Same project, status `done`, accepted by the answerer, priority normal, title = the question's title.
- Body = `## Context` with the question body, `## Decision` with the answer.
- A `relates` link from the decision to the question. The decision sits under the question's story while that story is open; with no story, or a closed one, it has no parent. No test pins the parent rule (unverified by test, read from code).
- Events: `card.created` (with `from_question`) and `card.linked` for the decision.
- The response carries `Location: /api/v1/projects/{slug}/cards/{n}` for the new card; the CLI prints it on stderr.

An agent answers a question only with the answer right in that project, and never one its own chat asked (`refuseOwnQuestion`); without the right it is 403 naming the right (`TestAgentsAndMembersCannotAnswerQuestions`), and `decide` is 422 from any agent. The rights are in `docs/requirements/agents.md` 2.6; relaying a person's answer is in `docs/requirements/board.md` 2.10.

### 2.3 Decision cards: who and what

| Action | Needs |
|---|---|
| Propose (create as ready) | member |
| Edit the text of a proposal you wrote | member |
| Edit another actor's proposal text | reviewer; an agent with the triage right may (`decisionAuthorPerm`) |
| Record as accepted at creation (`status: done`) | reviewer, human (403 for an agent or a member) |
| Accept, reject | reviewer, human; or an agent holding the decide right, for its owner (An agent's verdict, below) |
| Revoke | reviewer, human; any agent gets 403 (`requireHuman`) |
| Re-propose a rejected decision | its proposer (member) or a reviewer |

(`TestAgentWithTriageCanEditAProposalAndNoneAcceptsWithoutTheDecideRight`, `TestDecisionRejectRevokeAndReproposeFollowTheTable`.) Priority, assignee, lane, branch and parent follow the ordinary card rules.

**Body template.** `## Context`, `## Decision`, `## Consequences`, in that order, empty sections omitted. It is a convention; the server stores any text. `lorvik decide` builds it from `-m` (the Decision section, required), `--context` and `--consequences`. `lorvik decisions edit` parses the body back into the three sections and refuses (usage error) a body that has other text or headings, so an edit never drops text it cannot place. Title is at most 200 characters.

**Who proposes, who accepts.** Agents propose; a human with decision-accept moves a decision out of ready or out of done, and an agent only under the decide right (An agent's verdict, below). `lorvik decide` run by a human creates the decision as accepted (`done`) unless `--proposed` is passed; run by an agent it is always proposed. A human who is only a member and omits `--proposed` gets 403, because `done` needs decision-accept. Changes a reviewer wants travel as comments; the proposer applies them with `decisions edit`.

**An agent's verdict (the decide right, decision #384).** An agent holding the decide right in the project (`docs/requirements/agents.md` 2.6) accepts or rejects a proposed decision for the person who owns it, through the same transition a person uses (`agentVerdict`; `internal/server/decide_test.go`).

| Case | Answer |
|---|---|
| The agent lacks the decide right there | 403 naming the right |
| No `note`, or a blank one | 422 on `note`: the note says what the verdict rests on |
| The proposal was made from the same chat (base label) or one of its subagents | 403; another chat of the same agent may give the verdict |
| The proposal supersedes a decision that is still accepted | 403, for accept and for reject; once the old decision is revoked nothing in force is replaced and the verdict is allowed |
| The decision is in the global project | 403 |
| Revoke; record a decision as accepted at creation; `decide` on an answer | 403, 403 and 422 as before: a person's |

- The verdict takes effect at once. `accepted_by` is the agent, the note is the closing note, and the card carries `behalf` (the owner, the agent, the reason, the chat) exactly as an answer for a person does (`board.md` 2.10); `card.status_changed` carries `for`.
- It waits for the owner's look: the `answered_for` list filter returns it until the owner keeps it (`POST .../keep`, `card.kept` with `kind` `decision`). A person who revokes an accepted one or proposes a rejected one again clears `behalf`; while it was not kept, that event carries `settled_by` and `settled_by_session`, and the chat that gave the verdict is woken (`judged`). A successor's acceptance, which drops it as superseded, clears `behalf` as well, and so does an agent that proposes a rejected decision again: the proposal is open once more and gets a new verdict, so nothing of the old one waits for a look. That move carries no `settled_by`; the chat on duty is woken by the proposal itself when it may give the verdict.
- A chat holding the right is woken by a decision it may give the verdict on (`board.md` 3, `wakeToSettle`).
- The bars are checked by the server (`verdictBarred`), so they do not depend on the model. The same-chat bar rests on the session label the chat sends, so it catches mistakes, not a chat that lies. The chat that made a card is read from its `card.created` event; a card of the agent that has none counts as the calling chat's own (`askedByChat`), for a verdict and for an answer alike, so the unknown case goes to another chat or a person.

**Supersession.**

- `supersedes: N` on a new decision needs N to be a done decision of the same project (422 on `supersedes` otherwise, including N a task, missing, or still proposed). The new card gets a `supersedes` link (new to old) and the event `card.created` carries `supersedes`.
- A decision already superseded by a done successor takes no second one (422 on `supersedes`). Two proposals may name the same predecessor at the same time.
- When the successor becomes done, whether created done or accepted later, the old decision is dropped with reason `superseded` in the same transaction, with `card.status_changed` carrying `superseded_by` and `kind`. If the old decision is no longer done at that moment (revoked meanwhile, or retired by a rival), it is left as it is and no event is written (`TestSupersedeLeavesAnOldDecisionThatIsNoLongerDone`); the successor keeps its `supersedes` link.
- The `supersedes` link cannot be created or removed through the link routes (422 on `kind`).
- `Card.decision` carries `accepted_by`, `accepted_at`, `supersedes` and `superseded_by`. The acceptor is kept after a revoke or supersession.

**Applies.** A decision links to cards it applies to with link kind `applies` (decision to card; any other source kind is 422). A decision in the global project may apply to cards in any project; the card shows an incoming `applies` link naming the global project (`TestAppliesLinksFromADecisionAcrossProjectsThroughGlobal`). The link does not change either card's status. Provenance of a decision made by `decide=true` is the `relates` link, not `applies`.

**References.** The body is scanned for `[[...]]` on create, edit and `decide=true`, so backlinks work for decisions as for other cards.

### 2.4 How the decision words are shown

The words proposed, accepted, rejected, revoked and superseded are derived from status and `drop_reason`; no field stores them.

| Surface | Behaviour |
|---|---|
| CLI lists (`ls`, `decisions ls\|search`) | `D ` title prefix and the raw status (`ready`, `done`, `dropped`); `--in-force` is `--status done`; `--all` spans projects (project in parentheses); `-p global` lists the global project |
| `show`, `decisions show` | meta line adds `dropped: <reason>`, `accepted by <who> <when>`, `supersedes #N`, `superseded by #N`; a story's `Decisions:` section lists its decision children and decisions applied to it |
| Dashboard | `decisionWord` (`web/src/lib/cards.ts`) words every decision badge; without a `drop_reason` it falls back on the decision block (a successor means superseded, an acceptor means revoked, else rejected) |
| Decisions page (`DecisionsView.vue`) | scope All projects, This project or Global; state Any, proposed, accepted, or rejected-revoked-superseded; rows newest first with acceptor and project; New decision form |
| Card panel | `TransitionBar.vue`: Accept and Reject on a proposal, Revoke on an accepted decision, Re-propose on a rejected one, shown to human reviewers only |

**Awaiting-you strip** (`AwaitingYou.vue`, on every board and map; on the Map a row of the project moves to its card, map.md 2.2). It lists the global project's `ready` questions and decisions, and for a human reviewer of the board's project also that project's (leaving out cards assigned to someone else), at most 20 of each per project, newest update first; questions marked for a person (`for_person`) come first, before every other question and decision, and carry a "for a person" mark. Global rows carry a "global" badge on another project's board. It is hidden when empty; a project the caller cannot read adds nothing (403 or 404). A human reviewer sees Accept and Reject on a decision and Answer (which opens the panel) on a question; Reject sends `drop_reason: rejected`. It reloads on events.

### 2.5 Events

All carry `title`. Keys beyond that:

| Event | When | Keys | CLI wording |
|---|---|---|---|
| `card.created` | any create | `kind`, `status`, `priority`; `supersedes`; `from_question` | `proposed decision`, or `recorded decision` when `status` is done; `, supersedes #N` |
| `card.status_changed` | accept, reject, revoke, re-propose, supersede, cascade | `kind`, `from`, `to`, `drop_reason`, `note`, `superseded_by`, `cascade_of`; `for` on an agent's verdict; `settled_by`, `settled_by_session` when a person undoes one nobody had kept | `accepted decision`, `rejected decision`, `revoked decision`, `superseded decision ... with #N`; ` for <name>` on an agent's verdict; `, dropped with story #N` |
| `card.kept` | the owner keeps an agent's verdict | `kind`, `settled_by`, `settled_by_session` | `kept the verdict on` |
| `card.updated` | text or field edit | `fields` | `edited` |
| `card.linked`, `card.unlinked` | `applies` and `relates` links | `kind`, `to`, `to_title`, `to_project` | `linked #N applies #M` |
| `card.answered` | answer | `answer`, `decided` | `answered ...`, plus `(recorded as a decision)` |

The subject number is the card's. A re-proposal has no decision wording and no `drop_reason`, so it reads `moved #N from dropped to ready` in both the CLI and the dashboard.

## 3. Rules

- Authorization: `docs/api.md` Roles; the project role is the higher of org and project role, except in the global project (`board.md` 2.1).
- Versioning: every PATCH, answer and transition carries `version`; mismatch is 409 with `current_version`.
- Atomicity: answering with `decide`, unblocking, supersession and the events of each run in one transaction.
- Audit: every state change appends an event as the caller; title and body edits keep the old text as a card revision (`lorvik history N`).
- Errors: problem+json with a `hint` and `errors[].field`; there are no machine error codes.

## 4. Data

Authoritative columns: `internal/store/migrations/00001_init.sql`. There is no decisions table.

| Where | Meaning |
|---|---|
| `cards.kind` | 2 question, 4 decision (see `board.md`) |
| `cards.question_choices`, `answer` | question payload; `answer` null until answered |
| `cards.answered_by_*`, `answered_at` | the answer's author and time; on a decision, who accepted it and when |
| `cards.status`, `cards.drop_reason` | decision state: ready, done, or dropped with 4 rejected, 5 revoked, 6 superseded |
| `card_links.kind` | 1 blocks, 2 relates, 3 duplicates, 4 supersedes, 5 applies |
| `refs` | resolved `[[...]]` references, for backlinks |

## 5. API

Wire types: `internal/apitypes`; routes: `docs/api.md`.

| Route | Permission | Used by |
|---|---|---|
| `POST /projects/{slug}/cards` (kind question or decision) | question ask; decision propose | CLI `ask`, `decide`, `add`; dashboard add form (kind picker, always `ready`, no `supersedes`, no template) |
| `POST .../cards/{n}/answer` | question answer, user | CLI `answer [--decide]` (agents exit 5); `AnswerForm.vue` (choice buttons, free text, "Record as decision" box) for human reviewers |
| `POST .../cards/{n}/transition` | see 2.3 | CLI `decisions accept\|reject\|revoke`, `reopen`; `TransitionBar.vue`; `AwaitingYou.vue` |
| `POST .../cards/{n}/keep` | the person the verdict was given for | Keep on the "Answered for you" list and on the card (dashboard.md 2.2, 2.15) |
| `PATCH .../cards/{n}` | see 2.3 | CLI `decisions edit`; the dashboard edits only branch and lane |
| `POST .../links`, `DELETE .../links/applies/{to}` | card write | CLI `decisions link\|unlink [--to-project]` |
| `GET /cards?kind=decision` | read | CLI `decisions ls\|search`; Decisions page. The Awaiting-you strip uses the per-project list instead |

## 6. Jobs

None. Unblocking and supersession are part of the request.

## 7. Why it is this way

1. Decisions are cards, not a table, so one numbering, one link model and one queue serve them (`docs/adr/0008-stories-decisions-as-cards-global-project.md`, which amends `0006`). Rejected: keeping the table with a proposal-card mirror.
2. Accepted text is frozen and changed by supersession, so the log keeps what was true when. Rejected: in-place edits (the 409 hint says to supersede).
3. A proposal is the decision card itself, ready, so a human sees it in the lane and the strip with no second card. `next` returns tasks only, so agents never take it. Rejected: a separate question card that mirrors the decision.
4. An agent accepts or rejects a proposal only under the decide right: a right of its own that is off unless a person gives it, used for that person, with a reason, and never on its own chat's proposal, on a replacement of a decision in force, or in the global project. It never revokes, and its verdict waits for its owner's look. So an unattended chat cannot ratify what it proposed or change what a person put in force. Rejected: no agent verdict at all, the rule until decision #384 (the owner chose "decisions too, as a separate right I give or not" on #380); and folding it into the answer right, because a decision binds later work and a person must be able to give one without the other. Accepted risk (#380): every chat on a machine is the same agent, so the chat that accepts and the chat that proposed may both be unattended; the bars and the not-looked-at record limit what follows from that and do not remove it.
5. Choices are suggestions, so a human can answer "neither, explain". Rejected: enforcing choices.
6. Org-wide decisions are cards in the global project, so card numbers, links and permissions work unchanged. Rejected: a nullable-project decision table.
7. Unblocking happens when a blocker closes, not only when it is answered, so a dropped question also frees its targets. Rejected: unblocking on answer only.

## 8. Known defects

1. Dashboard event rows (`web/src/lib/events.ts`) never say accepted, rejected or revoked: a decision move reads `moved #N from ready to done`. The CLI words them (`internal/cli/events.go`, `decisionStatusSentence`).
2. `from_question` on `card.created` is rendered by neither the CLI nor the dashboard.
3. The CLI list line prints the raw status, so a rejected, a revoked and a superseded decision all read `dropped` in `ls` and `decisions ls`; the reason shows only in `show`. The dashboard shows the word.
4. The dashboard cannot edit a decision's title or body, set `supersedes`, record a decision as accepted, or add `applies` links; those are CLI only.
5. A decision made by `decide=true` from a question with no story has no parent and appears in the unfolded list (`board.md` 2.6).
6. No test pins: the parent given to a `decide=true` decision, the event order of `decide=true`, the schema status of an empty `answer`, or an `applies` link from a decision to a card in a closed story.
