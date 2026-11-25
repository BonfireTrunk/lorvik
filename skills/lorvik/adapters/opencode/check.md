# OpenCode checks

**The human checks below need OpenCode 2.1.4 installed. Nothing in this document
was run.** Use a disposable local chat and card. Source review and syntax checks
are separate from these unperformed runtime checks.

## Listener and injection boundary without a model login

1. Follow the README's local `tui.ts` installation. Put the newer main-checkout
   `lorvik` supporting `--exit-on-eof` on `PATH`, export
   the same absolute `LORVIK_CONFIG` for the TUI and server, and open an existing
   disposable chat. Do not submit a prompt. Loading the plugin must start no
   listener. The slash commands themselves execute locally and need no model.
2. To observe the exact injection boundary without sending a real prompt, use a
   **temporary copy** of `tui.ts` in that installation. Replace only its
   `await ctx.client.session.prompt(...)` statement with:

   ```ts
   ctx.ui.toast.show({
     message: JSON.stringify({
       sessionID: s.id, delivery: "queue", resume: true, lines,
     }),
     variant: "info",
   });
   ```

   This explicitly stubs the delivery boundary; it does not prove HTTP admission
   or start a model. Inspect the copy to ensure no real `prompt` call remains.
3. Start OpenCode from the bound directory:

   ```sh
   export LORVIK_CONFIG=/absolute/path/to/agent.toml
   opencode
   ```

   Open the disposable chat and enter `/lorvik-listen` twice. Expect an armed
   toast followed by "already armed". From another terminal inspect the process
   tree with `ps -eo pid,ppid,args`; expect one direct
   `lorvik inbox --follow --exit-on-eof` child of the TUI. There is no guardian.
   Leave the board quiet; expect no boundary toast or model turn.
4. If the chat already has a shell tool available, run the following there and
   compare its label to the card's recorded session before triggering an event:

   ```sh
   printf 'native session: %s\n' "$OPENCODE_SESSION_ID"
   lorvik session
   ```

   Invoking a shell tool through a new model turn requires login; if none is
   available, defer that check to the next section. Do not invent a native ID
   or substitute it for a Lorvik label.
5. From another authorized actor's shell, using its own config and label, create
   a comment on a disposable card that concerns the target chat:

   ```sh
   LORVIK_CONFIG=/absolute/path/to/other-actor.toml \
     lorvik comment 42 -m 'boundary check'
   ```

   Expect a boundary toast naming the commenting actor and card #42 with its
   current title, the selected native session ID, `delivery: "queue"`, and
   `resume: true`. The comment body is not in plain follow output
   (`internal/cli/events.go`). Arbitrary literal payloads are tested only
   with the fake CLI. Several lines arriving together should appear in one batch.
   An unrelated card must cause none.
6. Enter `/lorvik-unlisten`; confirm the listener disappears. Repeat
   for navigation away, ordinary exit and abrupt terminal-process termination.
   No new boundary toast should appear. A listener authentication failure should
   produce one stop toast and no restart. Inspect the process tree while the
   board is quiet too, so a surviving child cannot hide behind lack of output.
   After any self-exit or uncertain receipt, inspect pending input and handled
   messages before rearming: restarting the follower can replay unacknowledged
   events and create a duplicate turn.
7. Leave and return immediately to the same chat; it must stay disarmed.
   While an old listener is retiring, invoke arm in chat B then navigate to C;
   C must receive no listener without its own explicit arm. During a deliberately
   held admission, flood fake listener lines; expect one coalesced pending batch
   keeping the newest lines, with one exact dropped-count notice. No prompt
   backlog should accumulate locally.

The separate offline `check.mjs` uses hand-written API/reactivity fakes, a fake
CLI, and temporary files. It uses no socket or server and imports no OpenCode
implementation. Its results are recorded separately from this unrun human checklist.

## Extra step with model login

Restore the unmodified adapter in the installation and restart OpenCode. Log in
normally, select a disposable chat and have the agent report `lorvik session`
and its `OPENCODE_SESSION_ID`. Confirm they identify the card used above.
Ask it to acknowledge the next Lorvik notification and run `lorvik inbox`.
Enter `/lorvik-listen`, let the current turn finish, then create one comment
from the other actor. Expect one new turn in the **same** chat without composer
input. During a separate planned busy turn, send two comments; expect queued
delivery after that turn, without steering it. Disarm afterward.

Separately test closing immediately before/after prompt admission and restarting
the managed server with admitted input. A post-close or recovered turn exposes
the documented durable-admission gap; ordinary wake success cannot disprove it.
Every already-admitted batch can still execute after closure or recovery. While
armed, new batches can keep scheduling turns until departure/disarm, so there
is no one-turn ceiling. Use disposable work and account for those model turns.
