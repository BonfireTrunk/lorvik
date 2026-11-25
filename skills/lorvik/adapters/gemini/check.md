# Gemini next-human-prompt fallback checks

Requires **Gemini CLI 0.67.0 installed by the human** for the actual harness
path. **Nothing in this checklist was run.** The separate
[check-offline.sh](check-offline.sh) fixture was run without Gemini or a service;
its outcome and syntax checks are recorded in [README.md](README.md).

There is no listener in this next-turn fallback. Listener startup, 250 ms
batching and listener termination are not applicable.

## Installed harness, before a model login

Use the configuration in [README.md](README.md). Choose a label reserved for
this chat and export it before starting Gemini. The hook and ordinary shell
tools must use the same values:

```sh
export LORVIK_CONFIG=/absolute/path/to/agent.toml
export LORVIK_SESSION=gemini-lorvik-1
lorvik --session "$LORVIK_SESSION" session
```

The printed label must be `gemini-lorvik-1`. For a direct test of the real hook
JSON boundary, with the human's existing Lorvik service and config available:

```sh
node -e 'process.stdout.write(JSON.stringify({cwd:process.cwd(),hook_event_name:"BeforeAgent",session_id:"boundary-check",prompt:"continue"}))' |
  node skills/lorvik/adapters/gemini/lorvik-before-agent.mjs
```

Ask another board participant to create an event concerning that label before
this check. Expect its `wake: true` JSON in
`hookSpecificOutput.additionalContext`. A `systemMessage` is a failure or an
inactive configuration, not successful delivery. With nothing pending, expect
`{}`. This path does not call a model and does not advance the stored cursor.

## Extra step with a model login

After the human has logged in, launch `gemini` with the same exported variables
and installed `BeforeAgent` entry. Create a fresh board event for the label.
Leave the chat idle: **no turn should start**. Submit an ordinary prompt asking
the agent to report the Lorvik event context it received. Confirm the event
arrives on that turn and that normal `lorvik inbox` consumes it. Also submit a
prompt with no pending wake events and confirm no board context is added.

This final step tests actual hook loading and model-visible context. Never
use headless session resume as a substitute. There is no automatic idle wake
to validate for Gemini; use the documented waiting fallback when idle.
