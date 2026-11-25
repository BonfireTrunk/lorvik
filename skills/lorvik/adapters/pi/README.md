# Lorvik wake for Pi

For Pi 1.5.0. Copy or symlink `lorvik-wake.ts` into
`~/.pi/agent/extensions/`, or test it with:

```sh
LORVIK_CONFIG=/absolute/path/to/agent.toml pi -e /absolute/path/to/lorvik-wake.ts
```

Install it once; loading both the global copy and `-e` creates two listeners.
Put this revision's `lorvik` CLI (with `PI_SESSION_ID` detection) on Pi's PATH.
Start in a directory with a `.lorvik.toml` in it or an ancestor.
Export `LORVIK_CONFIG` **before starting Pi**. Setting it only
inside a bash tool call cannot configure the extension. Missing config produces
one UI warning per extension instance; outside a bound repo or without the CLI,
the extension does nothing.

It runs `lorvik inbox --follow` with Pi's current `PI_SESSION_ID`, batches lines
for 250 ms, and sends a user message with `deliverAs: "followUp"`. Idle chats
start a turn; busy chats receive the message after their current work finishes.
The message asks the agent to run `lorvik inbox` and follow the Lorvik skill.
Load that skill separately. No tools or skill instructions are installed here.

The listener reads from the stored cursor without advancing it. There is no
model polling, periodic model wake, or shell evaluation of event text. Normal
inbox reads still advance the cursor and renew claims. Existing session overrides
and higher-priority harness variables retain the CLI's normal precedence; avoid
inheriting another harness's session variables when launching Pi.

Pi emits `session_shutdown` then `session_start` for new, resumed, forked, and
reloaded sessions. The old listener and pending batch are stopped before the new
one starts. The child stays in Pi's process group. On an abrupt Pi death, its
next stdout write encounters a closed pipe. If the listener exits independently,
its last nonempty stderr line is notified once and it stays stopped. Fix the
cause and reload the extension or reopen the session to re-arm it. Transient
network retries belong to the CLI.

Local checks cover Bun transpilation and a fake CLI with Node: startup gates,
fragmented-line batching, session replacement, shutdown, errors, and idle silence.
End to end with Pi 1.5.0 in RPC mode against a local Lorvik server: a comment on
another chat's card started no turn; a comment on a card the Pi chat created
started exactly one turn within a second; the listener exited with Pi. Full
TypeScript type checking and Windows behavior are **untested**.
