# Lorvik command reference

Describes the CLI as built for skill 0.10; the contract is `docs/requirements/board.md`. JSON field names come from internal/apitypes/types.go. If `lorvik <cmd> --help` disagrees, trust `--help`.

Global flags: `--json` (raw API JSON, any command), `-p`/`--project <slug>` (`-p global` is the org's global project), `--api-url`, `-Q`/`--quiet` (suppress the stderr unread notice and an empty inbox). Environment: `LORVIK_TOKEN`, `LORVIK_CONFIG` (config file path, for an unrecognised harness), `LORVIK_SESSION` (override only: up to 40 lowercase letters, digits and hyphens; `--session <label>` is equivalent; invalid values warn on stderr and leave the label empty). Normally both are unset because the CLI derives the session label itself, in this order: `--session`; `LORVIK_SESSION`; the harness session variable (`CLAUDE_CODE_SESSION_ID`, `OPENCODE_SESSION_ID`, `CODEX_THREAD_ID`, `CODEX_SESSION_ID`, `PI_SESSION_ID`, `QWEN_CODE_SESSION_ID`, `COPILOT_AGENT_SESSION_ID`, `DSH_SESSION_ID`, Goose's `AGENT_SESSION_ID`); the launching process, the first ancestor of the CLI that is not a shell, by pid, start time and hostname; a terminal tab or pane id, checked only when the process walk fails, first set of `TMUX_PANE`, `KITTY_WINDOW_ID`, `WEZTERM_PANE`, `ITERM_SESSION_ID`, `WT_SESSION` (a tab, not a chat; Windows Terminal forwards `WT_SESSION` into WSL); then the repo root (the `.lorvik.toml` directory, else the cwd), user name and hostname, so chats in one repo on one machine share it. A boundary process (Relay(, SessionLeader, init, systemd, sshd, container shims, wslhost) or pid 1 is never hashed. If the process lookup itself fails, the CLI's parent pid and hostname are used. The derived label looks like `witty-lynx-3f9a`, is stable for the life of the chat and distinct across chats and machines, is never saved to a file, and is sent as `X-Lorvik-Session` and recorded on each claim you create. A `session` key in the config file is ignored. A subagent sets `LORVIK_SESSION=<lead label>-c<child number>` (or `--session`), `LORVIK_API_URL`, `LORVIK_PROJECT`; `.lorvik.toml` in the repo root binds the project. stdout is data, stderr is diagnostics. Lists are capped at 10; raise with `-L`/`--limit`, continue with `--cursor`. Exit codes: 0 ok, 1 error, 2 usage, 3 not found, 4 conflict (version, illegal transition, claimed by another), 5 unauthorized or forbidden. A `cannot reach` error inside a harness sandbox that blocks the network (Codex sets `CODEX_SANDBOX_NETWORK_DISABLED=1`) carries a second hint naming the sandbox setting to change; stop and tell the human, do not retry.

Ids are strings. Cards, decisions and stories are all cards, addressed by number (`42`, `#42`); notes by slug or id; agents, tokens and events by id. Actors are `{kind, id}` with no name: human output shows names, `--json` shows ids, `lorvik actors <id>...` resolves them.

Every list ends with `remaining 37, next: lorvik ls --cursor <token>` when more rows exist (`100+` at the cap). The next command repeats your filters; copy it as printed. `comment`, `history` and `note history` have no count: a full page ends with `older, next: <command> --cursor <id>`.

## Orientation

| Command | Example | Notes |
|---|---|---|
| `status` | `lorvik status` | Project line, then `session <label> (<source>)` (source is `--session`, `LORVIK_SESSION`, `claude-code`, `opencode`, `codex`, `process <name>`, `terminal <VAR>`, `repo, no chat process found` or `parent process`), then visibility, `concurrency parallel|serial`, lease, role, counts per status, open questions, then `my cards (this session):` and, when any exist, `my cards (other sessions):` (same agent, claimed under another session label), then `sessions holding cards: witty-lynx-3f9a (2), keen-otter-a0f7 (1)` (every session holding a working card, most cards first, then by label) or `sessions holding cards: none`. Read-only. JSON: `{project, session, session_source, mine, mine_other_sessions, sessions: [{label, cards}]}`; `project.counts` has backlog, ready, working, waiting, done, dropped, open_questions. Ends with a `locks:` block, one live lock per line, when any lock is held (`--json`: `locks`, always an array). |
| `session` | `lorvik session` | Prints `session <label> (<source>)`, then `nothing to pass: this label is derived for this chat`. When the source is the repo fallback (chats in one repo would share a label) it instead generates a fresh random label and prints `session <label> (generated)` then `pass --session <label> on every lorvik call in this chat`; each call generates a new one, so run it once per chat; with `--session` it answers `keep passing --session <label> on every lorvik call in this chat`. A last line is the chat's name, shown on the board before the label: `name <name> (<source>)` when the CLI reads one (`LORVIK_SESSION_NAME`, or Claude Code's chat name) and sends it with every command, `name none found` when it reads none, `name not sent` when the human's config has `session_names = "off"`. Nothing is saved and no server is contacted. Other commands print a named chat as `<name> (<label>)`. JSON: `{session, session_source, generated, session_name, session_name_source}`. |
| `session name` | `lorvik session name "<text>"`, `--clear` | Only after `name none found`: stores the name the human gave this chat, never one you made up; rerun when they rename it. Exit 4: the CLI reads the name elsewhere or names are off; the message says where. JSON: `{session, session_name}`. |
| `inbox [--from now\|oldest\|<event id>] [--peek] [--follow [--once] [--exit-on-eof]] [-L n]` | `lorvik inbox -Q -L 5` | Events by others (your other chats too) since your cursor, as `time  sentence`, under `on your cards:` (held, assigned, created) then `others:`; then this chat's claims (renewed), other chats' (not renewed), open questions. Advances the cursor past everything examined; `--peek` does not. `--from now` stores the newest id, lists nothing; `oldest` the beginning; `<id>` after it. `-p`: one project, cursor unmoved. `-Q` and nothing new: no output, else `no new events`. `--follow` runs until interrupted (exit 0), one line per event concerning this chat (`--json`: one `Event`), then the `holding` lines (Claims), cursor unmoved, renewing no claim; no `--from`; `--once` exits after the first batch. JSON: `Inbox` (`claims`, `claims_other_sessions`). `locks:` lists live org locks (`--json`: `locks`). |
| `ls [--status s,s] [--mine] [--open] [--kind k] [--parent n] [--story n] [-q text] [--created-after t] [--created-before t] [--updated-after t] [--updated-before t] [--closed-after t] [--closed-before t] [--assignee a] [--priority p,p] [--sort s] [--desc] [-L n] [--cursor c]` | `lorvik ls --status ready --assignee none --priority high,urgent` | 10 cards per page in board order unless `--sort`. Line format `#N  status  priority  <marker> title`, marker `S` for a story, `D` for a decision, `?` for a question, none for a task. `--story N` lists a story's children (same as `--parent N`). JSON: `CardList`. There is no tag filter. Times `t`: `YYYY-MM-DD` (start of that day, machine zone), `7d`, `24h`, `30m` back from now, or RFC 3339; `--*-after` includes the instant, `--*-before` excludes it (`--created-before 2026-10-20` is before the start of that day). `--closed-*` match done and dropped cards only. `--assignee` is an actor id, `me` or `none` and matches assigned or claimed. `--sort created\|updated\|priority\|number` is ascending, `--desc` reverses it (`--sort priority --desc` puts urgent first); not with `-q`. A cursor only continues the sort it came from, and the printed next-page command carries relative times as absolute ones. More: `ls --status backlog --created-after 7d`, `ls --updated-after 24h --sort updated --desc`, `ls --closed-after 2026-10-20 --closed-before 2026-10-21`. |
| `next` | `lorvik next` | One ready, unclaimed, unblocked card by priority. Tasks only: never a question, decision or story, never a card assigned to another actor, never a card whose lane has a card in `working`. Does not claim it. Exit 3 when none; when cards exist but wait only on busy lanes, stderr says `lorvik: N cards waiting on busy lanes: <lane>, <lane>`. |
| `search <query> [--all] [-L n] [--cursor c]` | `lorvik search "session expiry"` | Cards (decisions and stories included) and notes, as `card #42  done  Title`, `note slug  Title`; the status shows whether a hit is still in force. Searches the current project; `-p <slug>` another you can read (`-p global` included), `--all` every one. |
| `show <n> [--page]` | `lorvik show 42` | Header, meta line (parent, creator, version, lease), body, then the non-empty sections `Links:` (`blocked by #M`, `relates #M`, ...), `Children:` (on a story: its non-decision children, one line each; the meta line carries `progress done/total`), `Blocked by:` (open blockers only), `Decisions:` (on a story: its decision children and decisions that apply to it, with state), `Comment (latest):`, `Recent events:`. Renews your lease if you hold it. Text output marks an agent's name with ` (agent)`; an unmarked name is a person. A card with an HTML page adds `Page: <size>, open <link>`; `--page` prints the page's HTML (exit 3 if none; `--json`: `HTMLPage`). JSON: `Card` (`page_bytes`). |
| `actors <id>...` | `lorvik actors 106666050296328877` | Resolve user and agent ids to names: `kind  id  name`. Unknown ids are omitted. |

## Working a card

| Command | Example | Notes |
|---|---|---|
| `claim <n> [--for d]` | `lorvik claim 42` | Lease (the project's, 30 minutes by default, or `--for 1h`: 1m to 24h, kept by renewals) on a `ready` or `waiting` card and move it to `working`, as `start` does; a `backlog` card needs `ready` first. Exit 4 if another actor, or another chat of your agent, holds it; the error names the holding session. |
| `heartbeat <n> [--for d]` | `lorvik heartbeat 42 --for 1h` | Renew lease; `--for` ends it exactly that far from now. Rarely needed: any request from the chat that took the claim renews it past its half. Exit 5 from another chat. |
| `start <n> [--branch b] [--for d]` | `lorvik start 42 --branch card-42-retry` | Claim and move to `working` in one call. Same preconditions and `--for` as `claim`. `--branch` records the git branch on the card; `show` prints `branch <b>`. |
| `release <n>` | `lorvik release 42` | Give up your lease. Write a comment first. |
| `wait <n> [-m]` | `lorvik wait 42 -m "needs API key"` | Mark `waiting` (`block` still works as an alias). Does not pause your session. For a human's answer prefer `ask --blocks`. |
| `watch <n> [--timeout 30m]` | `lorvik watch 42 --timeout 10m` | Block on the event stream until the first event on card n (answer, status change, comment, claim), print it as one sentence and exit 0; exit 1 `lorvik: timed out waiting on #n` on timeout, 130 on interrupt. Exits after the first event. The one sanctioned way to block: never poll with `show` or `inbox`, and use it only when you hold nothing else to do. Not `wait`, which marks a card waiting. JSON: `Event`. |
| `subscribe <n>` | `lorvik subscribe 42` | Subscribe this chat to card n: any event on it, such as a person's comment, wakes the listener though you do not hold the card. For a card left waiting on a person. Agents only, at most 50 open cards; dropped once the chat has been gone for a day, and when the card is done or dropped. `status` lists them. `unsubscribe <n>` ends it, also when none. JSON: `{project, number, title, subscribed}`. Not `watch`, which blocks. |
| `done <n> [-m] [--into path\|--no-contract-change]` | `lorvik done 42 -m "merged in abc123"` | Terminal. Write a comment first. `--into <path>` (the requirements document that describes the card) or `--no-contract-change` records the fold in the same call: required on a story (missing: exit 2), optional but preferred on any card without a parent, refused on a card with a parent (its story folds it). A story also needs every child must be done or dropped (open children: exit 4 with a hint naming them). A closed story is never reopened and takes no new children. Cards under a closed story cannot be reopened. Claims the card first if it is ready, waiting or unclaimed (stderr: `lorvik: claimed #N before marking it done`); never takes over a card held by someone else. |
| `drop <n> --reason r [--dup m] [--cascade] [-m]` | `lorvik drop 43 --reason duplicate --dup 42` | `r` is obsolete, duplicate or wontfix. `duplicate` needs `--dup`. Dropping a story with open children needs `--cascade`, which drops them too with the same reason and releases their claims; without it exit 4 lists them. |
| `fold` | `lorvik fold` | No argument: lists the project's done cards with no parent and no fold mark (loose tasks, questions and decisions), newest closed first. Run it with `-p global` for the global project. |
| `fold <n>` | `lorvik fold 20` | On a story: prints the brief (children with done summaries and `Done:` comments, decisions in full, lanes touched, suggested documents). Read-only; composed by the CLI. |
| `fold <n> --into path\|--no-contract-change` | `lorvik fold 31 --into docs/requirements/errors.md` | For leftovers: records the fold of a done card with no parent that `done` did not fold, in any project. Refused for a card with a parent, one not done, or one already folded. Stories and new work fold through `done --into`. |
| `comment <n> [-m note] [-L n] [--cursor c]` | `lorvik comment 42 -m - <<'EOF'` | With `-m` (`-m -` reads stdin) adds a comment in a comment template's shape; without it prints them, newest first. The latest also appears in `show`. JSON: `Comment` (`id`, `card`, `actor`, `body`, `created_at`). |
| `history <n> [--version v] [--page] [-L n] [--cursor c]` | `lorvik history 42` | Past titles and bodies, newest first, one per line: `v<version>  @<who>  <when>  <title>`, e.g. `v3  @claude-code  1h ago  Fix login`; `no history` when the card was never edited. `--version 2` prints one past version: `#42  v2  @Ana Souza  5h ago`, the title, a blank line, then the body. A version with a page adds `Page: <size>, print it with <command>`; `--version 2 --page` prints that HTML (exit 3 if none). Unknown card or version exits 3. JSON: list of `CardRevision` (`id`, `version`, `updated_by`, `title`, `created_at`; `body` only with `--version`). |
| `sync [--list] [--drop id]` | `lorvik sync` | Sends the writes queued while the server was unreachable (`comment -m`, `add`, `done`), in file order, under the session that queued each. Prints `sent     <what>` or `refused  <what>: <why>` per entry; a refused entry stays, marked, and is never sent again. `--list` prints `<id>  <ago>  <session>  <what>` (`(other)` for entries of another owner, server or project, `refused: <status> <why>`); `--drop <id>` removes one (`dropped  ...`, exit 3 if unknown). Server unreachable again, 401, 429 or another error: stops, exit 1 or that error's code, the rest stay. JSON: `sent`, `refused`, `left`, `others`. |
| `ready <n> [-m]` | `lorvik ready 42` | Move a backlog or waiting card to `ready`. Backlog: only a card a human named (SKILL.md 3.2). |
| `reopen <n> [-m]` | `lorvik reopen 42 -m "regressed"` | Move a done or dropped card back to `ready`. Refused for a story or a child of a closed story (exit 4) and for a decision other than a rejected one (exit 2). |
| `lock <name> -m <purpose> [--for 10m] [--card n] [--wait]` | `lorvik lock schema -m "regenerating sqlc" --for 15m --card 42` | Take an org-wide named lock on a shared resource; one line `lock schema held until 12:10:30 (15m) for "regenerating sqlc"`. `--for` is 1m to 1h (default 10m). Run it again from the same session to renew. Exit 4 if another actor or session holds it; `--wait` polls every 5 seconds for up to 30 minutes and says who holds it on stderr. A lock named like a card lane holds back the ready cards in that lane. JSON: `Lock`. |
| `unlock <name> [--force]` | `lorvik unlock schema` | Release your lock: `released schema`. `--force` releases another actor's lock (reviewer role, users only). JSON: `{name, released}`. |
| `locks [-L n] [--cursor c]` | `lorvik locks` | Every live lock of the org, one per line: `name  holder  session  Nm left  "purpose"  #card`. `status` and `inbox` print the same lines under `locks:`. JSON: list of `Lock`. |
| `who` | `lorvik who` | Every live claim of the org on cards you can see, org-wide (`-p` does not narrow it), grouped by holder and session: `name  agent\|person  session label`, then `  #n  project  title  claimed 10m ago  28m left` per card, then `3 chats of 1 agent and 1 person working in 2 projects` (a chat is a holder's session); `nobody is working on anything` when none. Follows every page. JSON: `{items: [{card: {project, number, title, kind, status}, holder, session, claimed_at, expires_at, mine}]}`. |
| `lock run <name> -m <purpose> [--for 10m] [--card n] [--wait] -- <command> [args...]` | `lorvik lock run schema -m "regenerating sqlc" -- task generate` | Hold the lock for the command's life: acquire, run the command with your stdin, stdout and stderr, renew every half lease, release when it ends, and exit with its exit code. If a renewal fails the command is killed and lorvik exits 4 with `lock <name> lost: ...; command killed`. The `--` is required (exit 2 without it). |

## Creating and editing

| Command | Example | Notes |
|---|---|---|
| `add <title> [-b body] [--kind task\|question\|story\|decision] [--parent n] [--priority p] [--assignee me\|user:id\|agent:id] [--lane l] [--ready] [--page-file f]` | `lorvik add "Add retry to webhook sender" -b "..." --parent 40 --assignee me` | Tasks and stories start in `backlog`; `--ready` makes them pullable. `--parent` must be an open story in the same project; a story never has a parent. `-b -` reads stdin. `--page-file f` attaches an HTML page (`-` stdin, not with `-b -`; body may then be empty). Extra words join into the title. `--lane` sets the lane (see `edit`). No tags. |
| `add --kind story` | `lorvik add "Webhook delivery" --kind story -b "As an integrator, I want ..., so that ...; done when ..." --lane webhooks --ready` | The body carries the user story and `done when`; the lane names the area (and so the requirements document). Children go under it with `--parent`. |
| `add --kind decision` | (use `decide`) | Same card as `decide`. |
| `add --kind question --choice ... [--blocks n]` | `lorvik add "Pick queue" --kind question --choice a --choice b` | Prefer `ask`. |
| `ask <title> [-b body] [--choice c] [--blocks n] [--parent s] [--page-file f] [--person]` | `lorvik ask "Redis or Postgres?" --choice Redis --choice Postgres --blocks 57` | Question card, ready at once. Writing to the global project (`-p global`) needs an org role. `--blocks` (repeatable) moves card n to `waiting` and releases its claim; once it is answered, card n returns to `ready`. |
| `edit <n> [--title] [-b/--body] [--priority] [--parent n] [--assignee a] [--branch b] [--lane l] [--page-file f]` | `lorvik edit 42 --priority high` | Sends the version it read; on a conflict it prints the hint and exits 4. `--parent 0` removes the parent. `--assignee` takes an actor id, `user:<id>` or `agent:<id>`; `""` clears. `--page-file f` sets the HTML page, `""` removes it. `--branch` and `--lane` set the git branch and the lane (`""` clears). A lane is a short tag such as `schema`, `generated`, `goldens` or `web` for files that cannot take two concurrent edits: `next` skips a card whose lane has a card in `working`. `show` prints `branch <b>` and `lane <l>` on the metadata line. Every title, body or page edit keeps the previous version, visible with `lorvik history <n>`. |
| `link <n> blocks\|relates\|duplicates\|applies <m> [--to-project slug]` | `lorvik link 42 relates 50` | "N blocks M" means M cannot proceed until N is done. `duplicates` points a dropped duplicate at its survivor. |
| `unlink <n> blocks\|relates\|duplicates\|applies <m> [--to-project slug]` | `lorvik unlink 42 blocks 43` | Remove a link. `--to-project` names the other card's project when it is the global project or the link came from it. JSON: `{number, kind, to, unlinked}`. |
| `answer <n> <text> [--decide] [--relayed -m note]` | `lorvik answer 17 "postgres" --relayed -m "Ana in chat, 14:05"` | Humans: reviewer or above; agents: the answer right, not on their own chat's questions. `--decide` creates a done decision card, linked `relates` (humans only, exit 5 for an agent). Without the answer right an agent only relays a human's answer: `--relayed` and a non-blank `-m` (where, when; `-` reads stdin), else exit 2; plain `answer` is exit 5. |
| `escalate <n> -m why` | `lorvik escalate 17 -m "about spending; no decision covers it"` | Hands an open question to a person: no agent answers it from then on (a relay stays allowed). For its asker, or an agent with the answer right that will not answer. `ask --person` asks it that way from the start. Only a person clears the mark. |

## Decisions and notes

A decision is a card of kind decision. Lifecycle: proposed (ready), accepted (done, in force), rejected, revoked or superseded (dropped, with that reason). A rejected decision can be reopened to proposed; accepted text is frozen.

| Command | Example | Notes |
|---|---|---|
| `decide <title> -m <decision> [--context] [--consequences] [--parent s] [--supersedes n] [--proposed]` | `lorvik decide "Use pgx" -m "pgx pool" --context "..." --consequences "..." --parent 20` | Creates a decision card. `-m` is the decision text (required). An agent's decision is always proposed (ready) and a human reviewer accepts it on the board; `--proposed` is for humans who want review rather than recording it done. `--supersedes N` names a done decision in the same project; when the new one is accepted, N is dropped as superseded. `-p global` for a cross-project decision. Body is `## Context`, `## Decision`, `## Consequences`. `next` never returns it and it cannot be claimed. |
| `decisions ls [--status s,s] [--in-force] [--all] [-q text] [--created-after t] [--created-before t] [--updated-after t] [--updated-before t] [-L n] [--cursor c]` | `lorvik decisions ls --in-force` | Decision cards, newest first, in the bound project (`-p <slug>`, `-p global` for the global project), or across visible projects with `--all` or when no project is bound. Line format as `ls` with the `D` marker. JSON: `CardList`. |
| `decisions show <n>` | `lorvik decisions show 7` | The card as `show` prints it, plus who accepted it and when, `supersedes` and `superseded by`. JSON: `Card`. |
| `decisions search <q> [--all] [-L n] [--cursor c]` | `lorvik decisions search "queue backend"` | Same scope and line format as `decisions ls`; the status shows whether a decision is still in force. `decisions show|accept|reject|revoke|edit` on a card that is not a decision exit 2. |
| `decisions accept\|reject\|revoke <n>` | `lorvik decisions accept 8` | A person's; an agent only `accept` or `reject` with `-m <basis>` under the decide right (else exit 5). Accepting a decision that supersedes another drops the old one as superseded. |
| `decisions edit <n> [--title] [--context] [--decision] [--consequences]` | `lorvik decisions edit 8 --context "..."` | The proposer, or a reviewer, and only while proposed; accepted or dropped text is frozen (exit 4, hint says to supersede). The old text stays in `lorvik history N`. |
| `decisions link\|unlink <n> <card> [--to-project slug]` | `lorvik decisions link 7 42` | Apply or remove a decision on a card (link kind `applies`). A global decision may apply to a card in any project: name it with `--to-project`. `unlink` JSON as for `unlink`. |
| `note ls [--archived] [-q text] [--created-after t] [--created-before t] [--updated-after t] [--updated-before t] [--author id\|me] [-L n] [--cursor c]` | `lorvik note ls --updated-after 24h` | `notes` is an alias of `note`. Archived notes are hidden by default. |
| `note show <slug\|id> [--version n] [--page]` | `lorvik note show architecture` | An id needs no project. `--version n` prints that past version; with `--page` it prints that version's HTML. A page adds `Page: <size>, open <link>`; `--page` prints its HTML (exit 3 if none). JSON: `Note` or `NoteRevision`. |
| `note history <slug\|id> [-L n] [--cursor c]` | `lorvik note history runbook` | Versions newest first, no bodies: `vN  title  @editor  time`. |
| `note new <slug> --title t -b body [--page-file f]` | `lorvik note new runbook --title "Runbook" -b "..."` | `-b -` reads stdin. A page may replace the body; `-` reads it from stdin, not with `-b -`. |
| `note edit <slug\|id> [--title t] [-b body] [--page-file f]` | `lorvik note edit runbook -b "..."` | Sends the version it read. The old text stays in `history`. `--page-file ""` removes the page. |
| `note archive <slug\|id> [--undo]` | `lorvik note archive old-runbook` | Hides the note; `--undo` restores. |
| `note search <q> [-L n] [--cursor c]` | `lorvik note search "lease sweeper"` | Same line format as `note ls`. |

References in bodies: `[[card:42]]`, `[[card:web/42]]`, `[[note:slug]]`, `[[note:web/slug]]`, or any with a snowflake id. A bare reference resolves in the card's own project first, then in the global project; decisions are cards, so `[[card:N]]` covers them.

## Setup and admin (usually human)

Roles, lowest first: viewer, member, reviewer, admin, owner. Agents hold rights instead, per project and per chat: work (claim, work, file, comment), ready (backlog to ready), triage (edit or drop others' cards), answer (answer questions); `status` lists yours, never beyond your owner's. An agent acts only in the projects it is bound to.

| Command | Example |
|---|---|
| `init [--create] [--name N] [--visibility org\|private]` | `lorvik init -p payments --create --name Payments` writes `.lorvik.toml`; never prompts for an agent, a pipe, CI or `--json`; no token exits 5 before any request; a missing project exits 2 and lists the projects you can use |
| `login [--org id] [--create-org name] [--token-name n]` | human only: GitHub device flow; prints a code and address on stderr, waits for the browser step, then stores the new secret in the config file without printing it (`--json` prints the token object, secret included); exits 5 while org access awaits an admin. With several orgs or an invitation it asks on a terminal, else exits 4 listing ids (pass `--org id`); in none it exits 5. `--create-org name` makes an org you own and signs in to it |
| `login <login> [--org id] [--create-org name]` | password login: password from `LORVIK_PASSWORD`, else prompted on a terminal; prints the new secret on stdout and stores it in the config file; org choice as above |
| `login --token <secret>` | human only: stores the secret and API URL in the config file (mode 0600, honours `LORVIK_CONFIG`) without a password or a request. Any login to a server only `.lorvik.toml` names asks first; exit 5 without a terminal |
| `me` | actor, org, org role, project roles, token prefix and expiry |
| `me set [--name n] [--timezone zone] [--locale tag]` | `lorvik me set --timezone Asia/Kolkata` | Edit your own user profile (users only; agent tokens are refused). Only flags you pass change; an empty value clears the field. Timezone must be an IANA zone and is checked before sending (exit 2). JSON: `User`. |
| `ping` | checks the server is reachable |
| `version` | works offline |
| `project ls [-L n] [--cursor c]`, `project show [slug]` | visibility, concurrency, lease, role, counts |
| `project create <slug> [--name] [--description] [--visibility org\|private] [--lease 30m]` | admin |
| `project edit [slug] [--name] [--description] [--visibility] [--concurrency parallel\|serial] [--lease]` (also `project set`; `--concurrency` is the policy the skill reads: parallel fans child cards out to subagents, serial works them in order), `project archive [slug] [--undo]` | admin |
| `project member ls [-L n] [--cursor c]`, `project member add <actor> [--role r \| --rights r,...]`, `project member remove <actor>` | admin; `--rights` (default work) is for agents; `<actor>` is `user:<id>`, `agent:<id>` or a bare id (a user); remove JSON: `{kind, id, project, removed}` |
| `member ls [--all] [-L n] [--cursor c]`, `member add <actor> [--role r \| --rights r,...]`, `member role <actor> <role>`, `member remove <actor>` | admin; cannot grant above your own role; remove revokes their tokens; JSON: `{kind, id, removed}` |
| `agent register [name] [--name n] [--harness h] [--machine m] [--rights r,...] [--project slug ...] [--org-wide] [--expires-in d]` | prints ONLY the token on stdout, valid 1h; pending until approved when the org requires it |
| `agent ls [--all] [-L n] [--cursor c]`, `agent show <id>`, `agent approve <id>`, `agent revoke <id>`, `agent restore <id>`, `agent remove <id>` | `approve` needs admin; the rest admin or the agent's creator, never an agent. `restore` undoes `revoke`; `remove` is for good. JSON: revoke `{id, revoked}`, remove `{kind, id, removed}` |
| `agent bind <id> --project slug [--rights r,...]`, `agent unbind <id> --project slug` | per-project rights (work, ready, triage, answer, decide), within your own; unbind JSON: `{kind, id, project, removed}` |
| `token ls [-L n] [--cursor c]`, `token create [--name n] [--scope read\|write] [--expires-in d] [--agent id]`, `token revoke <id>` | `create` prints ONLY the secret; revoke JSON: `{id, revoked}`; `tokens` is an alias |
| `org show`, `org set [--name] [--agent-approval auto\|admin] [--token-ttl-default d] [--token-ttl-max d] [--project-visibility org\|private]` | `set` needs owner; unspecified settings are kept |

## Key --json fields

`Actor`: `kind` (`user` or `agent`), `id`. There is never a name; use `lorvik actors`.

`CardBrief` (list items): `id`, `number`, `kind`, `status`, `title`, `priority`, `parent`, `claim` (`actor`, `session`, `expires_at`), `assignee`, `decision` (on a decision card: `accepted_by`, `accepted_at`, `supersedes`, `superseded_by`), `drop_reason`, `closed_at`, `relayed` (answer relayed by an agent, not yet confirmed), `fold` (once folded: `into`, `at`, `by`; `into` empty for no contract change), `project` (on cross-project rows), `updated_at`. Kind is `task`, `question`, `decision` or `story`.

`CardList`: `items` (CardBrief), `next`, `prev`, `remaining`.

`Card` (adds to CardBrief): `body`, `created_by`, `version`, `question` (`choices`, `answer`, `answered_by`, `answered_at`), `progress` (on a story: `done`, `total` over its children, dropped counting as done), `created_at`. On `show` only: `links` (`kind`, `number`, `title`, `status`, `direction`), `children` (a story's), `comment`, `events` (newest first), `blocked_by`. The `Decisions:` section is derived by the CLI from decision children and `applies` links.

`Inbox`: `events`, `cursor`, `remaining`, `claims` (CardBrief), `open_questions`. Each `Event`: `id`, `type` (such as `card.status_changed`), `project`, `actor`, `subject` (`type`, `id`, `number`), `payload` (always has `title` for card, note and project subjects; `card.created` has `kind`; `card.folded` has `into`), `created_at`. There is no summary field; the CLI composes the sentence from `type` and `payload`.

`SearchHit`: `type` (card, note), `id`, `number` (cards), `slug` (notes), `project`, `title`, `status` (cards, decisions included; absent for notes), `kind` (cards), `snippet`, `rank`.

`Note`: `id`, `project`, `slug`, `title`, `body`, `status` (`active|archived`), `version`, `created_by`, `updated_by`, `backlinks`. `NoteRevision`: `id`, `version`, `updated_by`, `title`, `body`, `created_at`.

`Comment`: `id`, `card`, `actor`, `body`, `created_at`. `Queued` (a write that waits, in place of the card): `queued` true, `id`, `command`, `card`, `title`.

`Project`: `slug`, `name`, `visibility`, `lease`, `status`, `role`, `counts`. `Agent`: `id`, `name`, `harness`, `status` (`active|pending|revoked|removed`), `role`, `projects`.

Errors (`Problem`): `title`, `status`, `detail`, `hint` (how to recover), `current_version` (on version conflicts). A `--cursor` that is malformed or from another list gives 400 `invalid cursor`.

Enums: status `backlog|ready|working|waiting|done|dropped`; priority `low|normal|high|urgent`; kind `task|question|decision|story`; link `blocks|relates|duplicates|supersedes|applies`; role `viewer|member|reviewer|admin|owner`.

## Concepts

- Card: a task, question, decision or story with a number (`#42`), a status, an optional parent and an optional claim. A story groups the cards of one piece of work (its children) and closes through a fold into the requirements documents.
- Decision: a card of kind decision with context, decision and consequences, shown with a `D` marker in lists. Note: a short markdown page in a project (the global project for cross-project ones), by slug or id; every edit keeps the old version. Comment: a message on a card for whoever continues. Event: a change record, read through `lorvik inbox`.
- A card is `waiting` while an open question blocks it; the answer readies it, an unanswered end returns a backlog card to `backlog`.
- Ids are decimal strings; never treat one as a number.

## Claims

- `lorvik start N` claims and moves to `working`; `lorvik claim N` takes the lease only. Both need a `ready` or `waiting` card (`lorvik ready N` first for `backlog`).
- Claims last the project's lease (30 minutes by default), or the `--for` stated on `start`, `claim` or `heartbeat`. Any request past half the lease renews it, and `lorvik inbox` renews all of this chat's claims. Before 20+ minutes of silence: `heartbeat N --for <time needed>`.
- An agent's claim belongs to the chat (session label) that took it. Only that chat's requests renew it; `watch`, the event stream and the `inbox --follow` listener renew nothing, so a closed chat's claims lapse. While the lease is live, `start`, `claim`, `done` and `drop` from another chat of the same agent are exit 4 and `heartbeat` and `release` exit 5; once it lapses, take it over with a comment (workflow 7). A human's claim is the human's from any session.
- An expired claim returns the card to `ready` via the sweeper.
- Last call: 5 minutes before a claim ends (a quarter of a lease under 20 minutes) the listener prints `<you>'s claim on #N ... ends in 5m`. Working the card: `heartbeat N`. Not: comment, `release N`. Any request after it renews every claim of the chat, forgotten ones too.
- A lost claim wakes its chat: `claim on #N ... expired`, `claimed #N (taken over from <you>)`, `released <you>'s claim on #N`. Stop work, `show N`. `done` on a card whose claim ran out says so on stderr before it claims again.
- Held cards: after its output every command prints `holding  #N  <project>  <title>  12m left` per card this chat holds, soonest first, `holding  3 more` past 8; drop them with `grep -v '^holding  '`. `--json`: objects gain `holding` (`cards`: `project`, `number`, `title`, `expires_at`; `more`), arrays nothing. None after `--page`, a secret (`login`, `token create`, `agent register`), `session`, `lock run`, `watch` or `inbox`, whose claim lines show `12m left`, as `status` does.
- Agents act only in projects they are bound to; the human runs `lorvik agent bind <your agent id> --project <slug>`.
- `inbox` keeps its cursor in the CLI config file, per token. On a machine that never ran it, the first run starts at the log's beginning, ten events at a time. `--peek` looks without moving the cursor.

## Creating work

- Search first (`lorvik search`, `lorvik ls --status backlog`, `--status ready`). Titles are imperative and specific ("Reject expired tokens in auth middleware"). Reproduction steps, paths and the reason go in `-b`.
- Two levels only: a story and its children (tasks, questions, decisions). Anything bigger than a one-line fix is a story. Child agents leave the story's status alone; the one who runs the story closes it with the fold.
- Cross-project work goes in the global project (`-p global`), which needs an org role to write.
- No tags: say the kind of work in title and body. New tasks start in `backlog`; `--ready` or `lorvik ready N` makes them pullable (agents: the ready right, unless a child of a story past backlog).

## Question template

Bodies render as markdown on the dashboard. Every `lorvik ask` body uses this shape; choices carry the same letters.

```
Context: what is being decided, what constrains it, what happens if nothing is chosen.

### A: <label>
Good: <what this gets right>
Bad: <what it costs, a real consequence>

### B: <label>
Good: ...
Bad: ...

Recommendation: A, because <one sentence>.
```

Rules: a Bad line names a failure mode, risk, behaviour difference or operational cost, never the amount of work. Leave out an option that another option makes pointless. The human may answer with a letter alone, a letter plus a remark, or free text; read the answer with `lorvik show N`, not only the inbox line.

## Comment templates

A person reads every comment on the board, so none past two sentences is one paragraph: the point in one sentence, then the parts that apply, a blank line between them. Done or released work:

```
**Done:**
- <change>, in `path/file`

**Checks:** `<command>` passed

**Left:**
- <what remains, who owns it>

**Gotcha:** <what would trip the next reader>
```

Relaying the human, or reporting where things stand:

```
Relayed from the owner's chat, not a board answer: "<their words>"

**Next:** <what happens now, who does it>

**Not covered:** <what still needs their go>
```

Correcting an earlier comment, with each fact's source:

```
Correction to the comment above: <what was wrong>.

- <fact>, checked in <where>
- <claim>: as relayed, not verified
```

A closing note (`done -m`, `drop -m`) is one sentence.

## Questions and decisions

- Put the tradeoff in the question body so the human can answer without opening code. Humans at reviewer or above, and agents with the answer right, answer; any other agent only relays (`answer N --relayed -m`); plain `answer` exits 5.
- Agents create decisions as proposed (ready decision cards); a human reviewer accepts, rejects or revokes them on the board. A comment on the card means the human wants changes: apply them with `lorvik decisions edit N`. Proposing a rejected decision again reopens it. Fill `## Context` (situation, constraints), `## Decision` (what, briefly) and `## Consequences` (easier, harder, given up).
- A decision is in force when it is done. Done or dropped text is frozen: change it with a new decision using `--supersedes N`. Edit only your own proposals (a reviewer may edit any), only while proposed.
- Cross-project choices go in the global project (`-p global`).

## Notes

- Project note: how this codebase works, runbooks, gotchas. A note in the global project (`-p global`) applies across projects.
- Keep notes short, one topic each. Link with `[[card:42]]` (decisions included) and `[[note:slug]]`; from a global note write `[[card:project/42]]`, a bare number resolves in the global project.
- Archive an obsolete note (`lorvik note archive <slug>`) instead of emptying it. Do not duplicate what code or a decision already says.

## Context economy

- Prefer `lorvik next` over `lorvik ls`; lists show ten rows and print the exact next-page command. Prefer `decisions search` and `note search` over `show`.
- Use `--json` only for a field you act on. Do not re-run `show` on a card you just changed.
- The stderr line `N unread, run lorvik inbox` (`100+` above 100) appears on every command except `inbox`; `-Q` silences it. Run `lorvik inbox` before your next card action.

## Tokens

In a recognised harness (session variables above) every command acts as its own agent `<harness>@<machine>`, registered and renewed from the human's `lorvik login`; leave `LORVIK_CONFIG` and `LORVIK_TOKEN` unset, as either turns this off. Elsewhere `LORVIK_CONFIG` must name your own agent's file; if it names another's (the CLI warns) or you have none, stop and ask the human to run `lorvik agent register`. The CLI saves a rotated successor in the config file silently. With `LORVIK_TOKEN` it cannot, and prints `lorvik: the server rotated your token; LORVIK_TOKEN stops working in 24h; ...`: tell the human once and carry on. Never ask for or relay a token; never put one in a card, note, decision or commit.

## Errors

Every error names the fixing command in its `hint:` line; run it instead of improvising.

| Signal | Meaning | Do |
|---|---|---|
| exit 4, version conflict | Someone changed the card since you read it. | `lorvik show N`, reapply your change, retry once. |
| exit 4, claimed by other | Another actor, or another chat of your agent (the message names its session), holds the card. | Do not retry. `lorvik next`. |
| exit 4, invalid transition | Status change not allowed from here. | Follow the hint. |
| exit 4, story close refused | Open children. | `lorvik ls --story S`, settle the children named in the hint, retry. |
| exit 2, fold required | `done` on a story without `--into` or `--no-contract-change`. | Update the requirements document, retry with `--into <path>`, or `--no-contract-change` when nothing durable changed. |
| exit 4, closed story | New child, reopen or move under a closed story. | Start a new story and `lorvik link N relates S`. |
| exit 4, lock held | <name> is held by another actor or session. | `lorvik locks`; wait with `--wait`, or pick other work. |
| exit 5, user-only action or a missing right | `answer` without `--relayed` or the answer right, `ready` on a backlog card, or `add --ready` outside a ready story, without the ready right, `decisions accept/reject/revoke`, writes to the global project without an org role, `agent`, `member`, `project` and `org set` commands. | Stop. Ask the human. |
| exit 5, not the claimant | You do not hold the card, or another chat of your agent does. | `lorvik start N` first; if another chat holds it, `lorvik next`. |
| exit 5, missing or bad token | Token unset, revoked or wrong; nobody signed in. | Tell the human. Do not mint one or log in. |
| exit 4 or 5, agent revoked | Your owner stopped this agent. | Stop board work and tell the human. Restoring or removing it is theirs: never run `agent restore`, `agent remove` or `agent register` for it. |
| exit 5, waiting to be allowed | Your agent's first command in a project. | Tell the human; retry once allowed. |
| exit 2, invalid cursor | A `--cursor` from another list or a mangled one. | Run the list again without `--cursor`, or copy the `next:` command exactly. |
| Server unreachable: `cannot reach <server>`, or `queued  <what>  id <id>` with exit 0 | No answer, or 502, 503, 504. `comment -m`, `add` and `done` are queued; all else fails. | Keep working the card you hold; `done` it. Do not `start`, `claim` or `ask`. Server back: the next card command (not `inbox`) sends this chat's entries; stderr `queued writes: N waiting, M refused`: `lorvik sync` sends, `sync --list` shows. |
| exit 3 | Card or note not found. | Check the number and `--project`; search by title. |
| exit 1 | Validation or other error. | Fix the named field and retry. |

## Do not

- Work on a card you have not claimed; mark a card done without a comment after more than a trivial change.
- File duplicate cards; change a story's status (unless you run it and are closing it with the fold); nest deeper than story and child; add children to a closed story or try to reopen one.
- Edit accepted decisions or decisions you did not make; propose a new one with `--supersedes`.
- Poll `inbox` in a loop: run it at session start, before claiming, and when the unread notice appears.
- Run `answer` without `--relayed -m`, `login`, `agent`, `token`, `member`, `project` or `org set` yourself; they exit 5 for an agent.
- Paste tokens anywhere, or treat ids as numbers.
