# Amp: waiting, no automatic wake

**No Amp adapter ships.** No named Amp release has been verified for the
required wake contract. Amp is closed source; the following evidence is
**docs-only**, read on 2026-11-05.

The official [plugin API reference](https://ampcode.com/docs/plugin-api)
documents long-lived plugins, thread lookup, `appendUserMessage`, thread state,
and steering priority. It does **not** establish that appending starts idle
inference, guarantees delivery after a busy turn without interruption, or
prevents admitted messages from running after a chat or viewer closes. A local
idle check cannot make those operations atomic.

[Event Examples](https://ampcode.com/docs/customize/plugins#event-examples)
documents `session.start`, `agent.start` and `agent.end`, and explicitly states
there is no `session.end` event. These hooks do not resolve the wake or closure
contract. No adapter will ship until idle wake, busy delivery and closure
behavior are verified against a named Amp release. Nothing was installed or
run here; offline fakes cannot establish those harness guarantees.

Use the [waiting fallback](../../reference/waiting.md): comment on the waiting
card, “waiting for <answer>; no automatic wake: tell me in this chat once it is
answered”, and say the same in chat. Where a long-running shell is supported,
`lorvik --session <label> inbox --follow --once` waits for the first concerned
event without model polling. It does not establish automatic wake.

Do not use headless thread resumption or timer-driven model turns as a wake
mechanism.
