# Waiting for the board

An idle chat does not see a human's answer, a comment or an unblocked card until something wakes it. Never poll with `show`, `inbox` or a timer loop: every check is a paid model turn. Wait on `lorvik inbox --follow` instead. It costs no model turn while nothing happens and prints one line only for an event that concerns this chat: an event on a card this chat holds or created, or on a card assigned to you in this chat's project (the one bound where it runs) that no other chat of yours holds or created (the inbox marks these `wake`). It reads from your stored cursor without advancing it, so `lorvik inbox` afterwards still shows everything. A batch ends with `holding  #N ...` lines, the cards this chat holds; a line `claim on #N ... ends in 5m` is the last call (SKILL.md 3.16). The listener renews no claim.

## When to wait

- `lorvik next` finds nothing for you, and an open question, a waiting or blocked card, or a child card worked elsewhere still needs an answer or a result.
- Not when the request is complete: stop instead.

## Claude Code

1. Arm the Monitor tool with the command `lorvik inbox --follow`, a description such as `lorvik: board events for this chat`, and `timeout_ms` 1800000, the maximum (a monitor lasts at most 30 minutes). The monitor's shell carries Claude Code's session variable, so it acts as this chat's agent and its session label matches this chat's.
2. On a notification: `lorvik inbox` (it advances your cursor), then act as the workflows say: an answered question is workflow 5 step 4, a comment is read and the card continued.
3. On expiry with nothing new: arm it again. Each expiry costs one model turn, about two an hour while idle. Stop re-arming when the request is complete or the human says to stop.
4. Stop the monitor before you end the session.

## Pi

The `skills/lorvik/adapters/pi/lorvik-wake.ts` extension runs the listener inside Pi and sends each batch of lines to the chat as a follow-up message, so nothing is armed by hand. The human installs it once in `~/.pi/agent/extensions/` and starts Pi with `LORVIK_CONFIG` exported. When a board event message arrives, run `lorvik inbox` and act on it. Tested end to end: an event on this chat's card starts one turn, others start none.

## Other harnesses

Adapters live in `skills/lorvik/adapters/<harness>/`; each README says how the human installs and arms it and what is still untested. Only Pi's is tested end to end.

- Armed wake, opt-in: Codex, OpenCode, Qwen Code, Goose, Crush, Kilo, DeepSeek Harness, Copilot CLI, Cursor Agent. A batch already handed to the harness can still run after the chat closes, so the human arms it on purpose.
- Next prompt only: Gemini CLI adds pending events to the human's next message; it never wakes an idle chat.
- No adapter: Amp, and any harness not listed.

Without an armed wake, before going idle run `lorvik comment N -m "waiting for <what>; no automatic wake: tell me in this chat once it is answered"` on the card that waits, and say the same to the human, so the card never just looks stalled. Where the harness lets one shell command run for a long time, `lorvik inbox --follow --once` blocks until the first event that concerns this chat and then exits; that is waiting, not polling.
