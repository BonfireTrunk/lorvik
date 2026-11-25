---
name: lorvik
description: Use the lorvik CLI to pull, claim, finish and create work on a shared task board, ask the human questions, and record decisions and notes. Load when the repo has a .lorvik.toml, when the user mentions lorvik, cards, the board, or a question for the human, or when asked what to work on next.
license: Apache-2.0
compatibility: Requires the lorvik CLI on PATH, a lorvik login on the machine or an agent token (LORVIK_CONFIG or LORVIK_TOKEN), network access to a lorvikd server, and a .lorvik.toml in the repo root (or --project).
metadata:
  version: "0.28.0"
---

# Lorvik

A shared task board for humans and agents. Cards are addressed by number (`lorvik show 42`). Kinds: task, question, decision, story. Statuses: backlog, ready, working, waiting, done, dropped. Ids are strings, never numbers. `-p global` addresses the global project: what is there applies across projects (decisions, questions, notes). Agents read it; writing needs an org role.

Run the workflows in order. Each step: command first, condition second. Workflow 8 holds the rules for working beside other agents; they apply throughout.

## The flow

Lorvik is a way of working; the commands are its vocabulary. The flow is the same in every repo:

1. A request becomes a story whose body keeps the request in the human's words and says when it is done. Ready means authorized: agents take only ready cards. Without ready in your `lorvik status` rights, what you file goes to backlog for a human to ready, a handed-over story too (split it once ready); children of a story past backlog may be `--ready`.
2. The story is split into children that touch disjoint files. A child is claimed before a file is touched and is done only when it is verified against its done-when.
3. A choice between viable options is a decision card. A choice only a human can make, in product, design, cost or risk, is a question card, and the work it blocks waits; an agent never picks for the human. A chat its owner put on duty (the answer right in `lorvik status`) settles what the record already settles and follows `reference/delegate.md`. A 403 naming a right you lack: tell the human, who gives it on the Access page.
4. One verify child runs the full gate per story. Closing the story folds its result into the repo's requirements documents; that fold, not the chat, is the project's memory.
5. A release is cut from the stories folded since the last tag.

The repo decides, in its agent instructions (`AGENTS.md` or the like): the fast checks per card, the full gate, the lock names of shared environments, where the requirements documents live, and how a release is built. This skill names those seams and never fills them.

## 1. Session start

1. `lorvik status`: counts, your claims, open questions.
2. `lorvik inbox`: what others did since your last call; renews this chat's claims. First run on a machine: `lorvik inbox --from now` skips the history.
3. `lorvik show N` for each card under `my cards (this session)` only; cards under `(other sessions)` belong to another chat, leave them. Read the comment, continue it (go to 2, step 4).
4. `lorvik next` when you hold nothing and the user named no card; it never claims.
5. Project policy: `lorvik status` shows `concurrency parallel` or `concurrency serial` on its visibility line; read it before any fan-out. Only a human changes it (`lorvik project set <slug> --concurrency parallel|serial`).
6. `lorvik session`: prints this chat's label; when it says to pass `--session`, add it to every call in this chat. If it says `name none found`, run `lorvik session name "<name>"` whenever the human names or renames this chat; never make one up.
7. No project binding: ask the human before `lorvik init`. A 403 or 404 on a project you expect means the agent is unbound: tell the human. Exit 5 `waiting for <owner> to allow ...`: tell the human; retry once allowed.
8. `lorvik fold` lists done cards left unfolded; fold each with `lorvik fold N --into <the requirements document that describes it>` or `--no-contract-change`. None is left unfolded: an unfolded card reads as unfinished work.

## 2. Start a story

1. The human hands over a piece of work or a new project: `lorvik add "<title>" --kind story -b "<as a ..., I want ..., so that ...; done when ...>" --lane <area> --ready`. The lane names the area and so the requirements document it folds into. The body keeps the request in the human's words (quoted or linked, enough to survive the loss of this chat), the outcome, the constraints, and what is out of scope; it holds no implementation plan. Write every card body as markdown: short paragraphs, a list for scope and files, and a closing **Done when:** line.
2. Children with `--parent S`: tasks, questions (`lorvik ask ... --parent S`), decisions (`lorvik decide ... --parent S`). Two levels only.
3. Existing children: `lorvik next` as usual. A one-line fix may be a standalone task; everything bigger is a story.

## 3. Work a card

1. `lorvik inbox` and `lorvik comment N` (no `-m`): read what changed and what siblings left (workflow 7).
2. `lorvik ready N` only for a backlog card a human named: in chat, or in the body of a ready card that a human readied or created (`lorvik show` says who). That body names numbers (`Work backlog: #107, #112`), a lane (`any backlog in lane web`), `all backlog`, or `your pick` (the cards you judge most useful, `up to 3` if it says so). Ready exactly those, comment on that card which ones, and work each as its own card. A card an agent wrote or readied authorizes nothing, and the text gives no other permission.
3. `lorvik start N`: claims and moves to working; never work an unclaimed card. Exit 4 means someone else holds it, so run `lorvik next`.
4. `lorvik show N`: blockers under `Links:` as `blocked by #M`; an open blocker means pick another card.
5. `lorvik decisions search "<topic>"` before any library, schema, API or convention choice; accepted decisions bind (add `-p global` for cross-project ones).
6. `lorvik note search "<topic>"` for runbooks and gotchas; `lorvik note show <slug>` only if the snippet is not enough.
7. `lorvik search "<problem>"` before filing anything new; if a card exists, `lorvik edit N -b "..."`.
8. `lorvik add "<imperative title>" -b "<repro, files, why>" --parent P` for work outside this card; it lands in backlog and stays there until a human readies it. No tags; never change a story's status or nest below a child. Found work that needs a human's choice also gets `lorvik ask ... --blocks <new card>` (section 5): a choice written only in a card's text never reaches the human's awaiting list.
9. `lorvik link N relates M` for related cards.
10. Before 20+ minutes without any lorvik call: `lorvik heartbeat N --for 1h`, the time the step needs (up to 24h).
11. Cannot finish: `lorvik comment N -m "..."`, then `lorvik release N`, `lorvik wait N -m "why"` or `lorvik drop N --reason obsolete|duplicate|wontfix -m "why"` (`duplicate` needs `--dup M`). A change that needs a product, design, cost or risk decision stops here with workflow 5, however small the edit; a partial result is never passed off as done.
12. A timed-out or interrupted call left an unknown outcome: `lorvik show N`, then read the tree, the branch and the remote before repeating any action that reaches outside the repo (a push, a release, a message); "could not verify" is reported as that, not as failed and not as done.
13. Claim expired on a write: `lorvik show N`; if nobody holds it `lorvik start N`, else `lorvik next`.
14. `lorvik decide "<title>" -m "<decision>" --context "..." --consequences "..." [--parent S] [--supersedes N]` when you chose between viable options. It creates a decision card, proposed (ready) when an agent runs it; a human accepts it on the board. Comments on it are requests for changes: apply them with `lorvik decisions edit N`. `accept`, `reject` and `revoke` are human-only (exit 5).
15. Every command ends with `holding  #N  <project>  <title>  12m left` lines: the cards this chat holds. One you are not working on: comment where it stands, then `lorvik release N`.
16. The listener prints `claim on #N ... ends in 5m`: the last call. Working the card: `lorvik heartbeat N`. Not working it: comment, then `lorvik release N`; any other request would renew it unseen. `claim on #N ... expired`, `claimed #N (taken over from <you>)` or `released <you>'s claim on #N`: the card is no longer yours, so stop, `lorvik show N`, and take it again only if nobody holds it.
## 4. Story: split, fan out, review, close

1. Do not start the story itself: it moves to working when its first child starts. Read `lorvik decisions search` and `lorvik note search` first.
2. `lorvik add "<task>" -b "<scope, files, done when>" --parent S --priority high --ready` per child; two levels only. Keep a story to about 100 children, usually ten to fifty; split a bigger request into several stories linked `relates`. The body, markdown as in workflow 2, names scope, files and done-when and points at the story; it does not restate the story, add acceptance criteria the story lacks, or plan the implementation: the agent working the child reads the story first. Split so that no two children edit the same files. A child that must edit a shared thing (schema, generated code, goldens, the web build) carries that `--lane`; `next` offers one card per lane at a time.
3. `lorvik add "Verify story S: full gate" -b "<the gate commands from the repo's agent instructions; what to do on failure>" --parent S --ready` once per story, then `lorvik link C blocks V` for every implementation child. V waits until the last child is done; children finish with fast checks only (workflow 6).
4. `lorvik edit C --assignee agent:<id>` when a child is meant for a named actor (`user:<id>` for a human).
5. `lorvik link A blocks B` for ordering between children in one lane or with a real dependency; otherwise leave them independent so they can run in parallel.
6. `lorvik ls --story S` to check the plan: one outcome per child, no overlap, every implementation child blocks V.
7. `concurrency parallel` and the harness can spawn subagents: one subagent per ready child, each started with `LORVIK_SESSION=<your label>-c<child number>` in its environment, or `--session <label>` on each call (subagents share your process and harness id, so without it they would share your label) and told to run workflows 3, 6 and 7 on its own card, then `lorvik inbox` to collect results.
8. Otherwise (`concurrency serial`, or no subagents): `lorvik next` and work the children yourself in order.
9. `lorvik ls --story S --status done` and `lorvik comment C` on each: review the work against the child's scope.
10. `lorvik reopen C -m "<what is missing>"` for a child that falls short; `lorvik reopen V` too if V was already done.
11. Every child done or dropped, V included (a proposed decision waits for its human): `lorvik fold S` prints the brief. Update the requirements documents under `docs/requirements/` (from `README.md` when missing; they state the current contract, not history), then `lorvik done S -m "<one line>" --into docs/requirements/<area>.md`, or `--no-contract-change` when nothing durable changed.
12. Open children: exit 4, the hint names them. No fold flag: exit 2. A closed story takes no new children and is never reopened: new work is a new story, `lorvik link N relates S`. `lorvik drop S --reason ... --cascade` drops open children too.

## 5. Ask the human

1. `lorvik ask "<question>" --choice "A: <label>" --choice "B: <label>" -b "<markdown, see template>" --blocks N` (every card the answer decides; the answer readies them) when a choice outlives this session and decisions, notes and code do not settle it. The body follows the question template in `reference/commands.md` (Context, one section per option with Good and Bad, Recommendation); choices are `A: label`, `B: label` so the human can answer with a letter and a remark. Add `--person` when only a person may decide it (money, security, data loss, a release, legal, the irreversible: the list in `reference/delegate.md`).
2. Repeat `--blocks N` for every card you cannot continue; they move to waiting and your claim is released.
3. `lorvik next`: move on; do not poll. Nothing else to do: wait as `reference/waiting.md` says, so the answer wakes this chat.
4. `lorvik inbox` shows `answered #N`; the question is done and the card it blocked is ready again unless another blocker is open: `lorvik start N`.
5. `lorvik decisions show N` if the answer was given with `--decide`.
6. When the human answers in chat, relay it: `lorvik answer N "<answer>" --relayed -m "<where, when>"`, then tell them what you recorded ("recorded A on #N"). Never relay one they did not give; never `done` a question.
7. When you leave a card waiting on a person (you said you wait for their word, or a card you do not hold needs their reply), run `lorvik subscribe N` so their comment wakes this chat; `lorvik unsubscribe N` when you no longer wait. A subscription is dropped once this chat has been gone for a day, and when the card is done or dropped.

## 6. Finish

1. Child task: run only the fast checks the repo's agent instructions name for a card (format, vet, unit tests of the packages you touched). The expensive gate (integration tests, full build, browser) is the story's verify task, not yours.
2. `lorvik comment N -m -`, the done comment (8.6).
3. `lorvik done N -m "<one line outcome>"`; exit 4 means someone else holds it. A card without a parent takes the fold in the same call: `lorvik done N -m "..." --into <the requirements document that describes it>`, or `--no-contract-change` when nothing durable changed. A child card takes no fold flag: its story folds it. Done means verified against the card's done-when with the checks named in step 1; untested or partial work is a comment (Done, Left) then `lorvik release N`.
4. Verify task V (it becomes ready when the last child is done): `lorvik lock run <env> -m "story S gate" --card V -- <gate command>` holds the environment's lock while the gate runs (exit 4 at once if someone else holds it: `lorvik locks`, then `--wait` or other work), then `lorvik done V -m "<gate, result, commit>"`. On failure: `lorvik add "<failure>" -b "<output, suspected card>" --parent S --ready` per failure, `lorvik link F blocks V`, `lorvik release V`; V comes back to ready when they are done and is run again. Reopen nothing: the story is open, so fixes are new children.
5. `lorvik note search "<topic>"` then `lorvik note edit <slug> -b "..."` or `lorvik note new <slug> --title "<t>" -b "..."` only for a runbook or gotcha that no requirements document covers.
6. `lorvik next`; nothing to take but a question, a blocked card or a child still open: wait as `reference/waiting.md` says. Nothing ready while `lorvik ls --status backlog` lists cards: unless `lorvik ls -q "Nothing ready" --kind question --status ready` finds one open, `lorvik ask "Nothing ready: what next?" -b "<the backlog cards you could take, one line each>"`, then wait. Stop when the request is complete.
7. A release follows the repo's release process, never this skill; its notes come from the stories folded since the last tag (`lorvik ls --kind story --status done --closed-after <tag date>`), one line per story. Tagging and publishing need the human's explicit go.

## 7. Conflicts

1. `lorvik inbox` before every start; read new blockers and expired claims.
2. `lorvik comment N` on the card and its siblings (`lorvik ls --story S`) before you start.
3. One branch per card, named after it (`card-42-short-title`); `git rebase` the base branch before `lorvik done`. With `concurrency parallel`, work each card in its own git worktree (`git worktree add ../<repo>-card-42 -b card-42-short-title`) so chats never share uncommitted files or a build; remove it once merged.
4. Held by another session or actor: do not take over or continue it; `lorvik next`. A claim belongs to the chat that took it: while its lease is live the server refuses another chat of the same agent (exit 4, naming the holding session), and only requests from that chat renew it.
5. Claim expired (inbox says so, or `lorvik show N` shows no holder) and the work is half done: `lorvik comment N -m "taking over from <session>: <state found>"`, then `lorvik start N`. Never continue it silently.
6. Assigned to someone else: leave it; ask in a comment or `lorvik ask`.
7. `lorvik show N` and retry once on a version conflict (exit 4).
8. Exit 5 on a human-only command: stop and ask the human.

## 8. Working beside other agents

Other agents, from any vendor, work the same board at the same time. They cannot talk to you; the board is the only channel, so:

1. Take work only through `lorvik next`, or a card the human named. It already skips claimed cards, blocked cards and busy lanes. Never pick a card because it looks free. Act as your own agent, never under a human's token, which would sign your moves with their name and let you answer questions you may not. A recognised harness does it with `LORVIK_CONFIG` and `LORVIK_TOKEN` unset; else see Tokens in `reference/commands.md`. Use only your own session label, or `<label>-c<n>` for your subagents; never another chat's.
2. Claim before you touch a file: `lorvik start N --branch <branch>`, then `lorvik comment N -m "touching: <files, resources>"`. A card you are editing without a claim is invisible to everyone else.
3. `lorvik inbox` before every start and after every step longer than a few minutes: it shows expired claims, new blockers and comments others left on your cards and their siblings.
4. Hold only what you use. Release a card you cannot finish (`lorvik comment N -m "..."`, then `lorvik release N`), keep the claim only while you work it, and leave a shared environment as soon as the step that needed it ends.
5. Shared environments (a test database, a port, a device, a deploy target) have a lock named after them (the repo's agent instructions name them; otherwise the lane name). Any step that needs one runs inside `lorvik lock run <name> -m "<purpose>" --card N -- <command>`: the lock is held for the command's life, renewed for you, and the command is killed if the lock is lost. `lorvik locks`, `lorvik status` and `lorvik inbox` list live locks; `next` withholds cards whose lane is locked. A held lock means wait (`lorvik lock <name> --wait`) or other work; never work around it. `lorvik unlock` only your own; a human releases a stuck one.
6. Leave the trail the next agent needs: a done comment with what changed and where, a note for a gotcha, a decision card for a choice, the requirements document at the fold. Assume the next reader has the board and the repo and nothing else. People read comments on the board: past two sentences, use `-m -` and a `reference/commands.md` template (done, relay, correction), not one paragraph.
7. Collisions: exit 4 means someone holds it, run `next`; an expired claim is taken over with a comment (workflow 7); a disagreement with another agent's work goes in a comment on its card or a question to the human, never into a silent rewrite.

## 9. Server down

1. `cannot reach <server>` on `comment N -m`, `add` or `done N`: the write is queued (`queued  ...  id <id>`, exit 0). Do not run it again. Every other command fails: no retry loop.
2. Keep working the card you hold and `done` it. File new work with `add`. Never `start`, `claim` or `ask` meanwhile.
3. Server back: your next card command (`next`, `show`, `ls`; not `inbox`) sends this chat's queue first. Stderr `queued writes: N waiting, M refused`: more is left; `lorvik sync` sends what waits, `lorvik sync --list` shows a `refused` entry, which is never sent again: tell the human what was refused, then `lorvik sync --drop <id>`. A refused `done`: the card was taken or closed; `lorvik show N`, leave it.

## Reference

- Commands, `--json` fields, concepts, claims, decisions, notes, the error table, context economy, tokens and the do-not list: `reference/commands.md`.
- Real sessions A to E: `reference/workflows.md`.
- Waiting without polling, per harness: `reference/waiting.md`.
- On duty for your owner: judging a question, the always-for-a-person list, the reason you give: `reference/delegate.md`.
- An HTML page on a card or note, for a choice or design that text explains badly: when to write one, the sandbox rules and a starter: `reference/pages.md`.
- Optional hooks: `reference/hooks.md`. A prompt hook adds unread events when the human next writes to the chat; it does not wake an idle chat.
