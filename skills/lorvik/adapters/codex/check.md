# Codex bridge acceptance checks

The harness checks require **Codex CLI 0.166.0 installed by the human**.
**Nothing in this checklist was run.** Offline validation is reported
separately. Do not run this checklist without a human's authorization. These steps use no database or server setup.
The real-listener stage needs an existing Lorvik service, an approved test card,
and the newer Lorvik CLI containing `inbox --follow --exit-on-eof`. Its lifetime
pipe must remain open and unwritten while armed. Each live `codex queue` call
places the batch text in process arguments; it may be visible in process listings.

## 1. Capture the injection boundary without a model

Run in a separate test shell. This stage uses fake executables, not Codex.
It verifies exactly one message argument, batching and literal board text.

```sh
adapter_root="$PWD/skills/lorvik/adapters/codex"
check_root="$(mktemp -d)"
mkdir "$check_root/project" "$check_root/runtime"
chmod 700 "$check_root/runtime"
touch "$check_root/project/.lorvik.toml"
cat > "$check_root/listener.py" <<'PY'
#!/usr/bin/env python3
import os, time
from pathlib import Path
Path(os.environ['CHECK_ROOT'], 'identity').write_text(
    os.environ['CODEX_THREAD_ID'] + '\n' + os.environ['LORVIK_CONFIG'])
print('now ana commented on #42 "Fix login; $(not-a-command)"', flush=True)
print('now ana unblocked #42 "Fix login"', flush=True)
time.sleep(1)
PY
cat > "$check_root/capture.py" <<'PY'
#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
with Path(os.environ['CHECK_ROOT'], 'queue.jsonl').open('a') as output:
    output.write(json.dumps(sys.argv[1:]) + '\n')
PY
chmod 700 "$check_root/listener.py" "$check_root/capture.py"
export CHECK_ROOT="$check_root"
export ADAPTER_ROOT="$adapter_root"
env -u CLAUDE_CODE_SESSION_ID -u OPENCODE_SESSION_ID -u LORVIK_SESSION \
  XDG_RUNTIME_DIR="$check_root/runtime" \
  CODEX_THREAD_ID=11111111-1111-1111-1111-111111111111 \
  LORVIK_CONFIG="$check_root/agent.toml" \
  sh -c 'cd "$1" && exec python3 "$2/lorvik-wake.py" --lorvik "$3/listener.py" --codex "$3/capture.py"' \
  sh "$check_root/project" "$adapter_root" "$check_root"
python3 - <<'PY'
import json, os
from pathlib import Path
root = Path(os.environ['CHECK_ROOT'])
calls = [json.loads(line) for line in (root / 'queue.jsonl').read_text().splitlines()]
assert len(calls) == 1, calls
args = calls[0]
assert args[:6] == ['queue', '--remote', 'unix://', '--thread',
    '11111111-1111-1111-1111-111111111111', '--message'], args
assert len(args) == 7 and '--resume' not in args, args
assert '$(not-a-command)' in args[6] and 'unblocked #42' in args[6]
assert (root / 'identity').read_text().splitlines() == [args[4], str(root / 'agent.toml')]
print('PASS: one batch reached the queue invocation boundary as one literal argument')
PY
```

Expect the bridge command to exit **1** after the fake listener exits, with one
`listener stopped` diagnostic and no restart. This deliberate exit is followed
by the assertions, so run commands individually if your shell has `set -e`.
The fake captures the adapter's native invocation; it does not prove admission
inside Codex. Retain the exact temporary path printed by `mktemp` for inspection.

## 2. Real listener and identity, still no model invocation

After installing the harness, open a disposable target chat and use its shell
tool for these commands. Use an existing bound project and absolute test agent
config. Never print the config or credentials. In a short setup turn collect:

```sh
printf 'native thread: %s\n' "$CODEX_THREAD_ID"
LORVIK_CONFIG=/absolute/path/to/agent.toml lorvik session
```

Record the Lorvik label. If an intentional `LORVIK_SESSION` is used, preserve it
for every command. Ensure the target thread and config match the intended card.
Create the following executable capture wrapper in a private temporary directory
visible to the shell tool, substituting a fixed absolute output path:

```python
#!/usr/bin/env python3
import json, sys
with open('/absolute/private/check-queue.jsonl', 'a') as output:
    output.write(json.dumps(sys.argv[1:]) + '\n')
```

Use `chmod 700 /absolute/private/capture.py`. Ask the agent to start, through
unified exec with `yield_time_ms: 1000` and `tty: false`:

```sh
LORVIK_CONFIG=/absolute/path/to/agent.toml python3 skills/lorvik/adapters/codex/lorvik-wake.py --codex /absolute/private/capture.py
```

Require `lorvik-wake: armed` and retain its managed exec ID. From another actor,
comment once on an approved card held or created by that chat. An unrelated card
must produce nothing. Inspect the capture file in a later human-requested tool
call. It must show the correct native thread, real board line and single message
argument. It must contain no `--resume`. This reaches the exact injection-call
boundary without invoking a model for the event.

Run `python3 skills/lorvik/adapters/codex/lorvik-wake.py --stop` in that chat,
then collect the managed command's completion. Repeat arming twice to confirm
that the second bridge refuses while the first remains alive. Verify a listener
self-exit produces one diagnostic without restart, and stopping ends the child.
Run `--stop` again after completion; it must report `no active bridge`, even
though the reusable lock file remains. A stop request is not completion evidence.
The top-level wrapper must not be replaced by `nohup`, `setsid` or `&`.
With a capture delivery intentionally held open, send enough fake short lines
to exceed both pending caps. Verify listener output keeps being consumed, only
one delivery exists, and the next batch retains newest lines and reports its
dropped count with `run lorvik inbox`. Include one line over 16 KiB delivered in
several fragments, followed by a valid line: expect exactly one counted drop and
normal delivery of that valid line. Repeat with only an oversized line: expect
a recovery-notice-only message. Event data is capped at 48 KiB plus at most
512 bytes of context/recovery text. Stop during that held delivery, then
verify both children exit. Separately kill only the bridge and confirm the
new CLI sees stdin EOF and exits, without a guardian process.

Review runtime permissions and the resolved directory before arming. Preserve
`XDG_RUNTIME_DIR` across shell tools; otherwise confirm both calls select the
same `/tmp/lorvik-codex-wake-<uid>` fallback. Lock and generation marker files
remain after shutdown, but must not retain an active lock or board data.

## 3. Extra step with a model login

This stage can spend tokens and durably changes the target queue. Accept the
README's post-close gap first. Use a disposable loaded chat, the owning existing
app server and approved test card. During one setup turn instruct the agent to
answer the next notification with `ACK`, run `lorvik inbox`, and stop its bridge.
Have it arm the README command using the real `codex`, then finish its turn.

Emit one relevant board event from another actor. Without composer input, expect
one queued message and a new turn **in the same native thread**. Quiet waiting
must produce no model turns. During a separate already-planned busy turn, emit
two lines within 250 ms and confirm one follow-up batch after that turn completes,
with no steering or interruption. An interrupted thread should remain paused.

Disarm and verify managed completion. Separately test navigation, close during
the roughly 60-second unload-delay window, and closure just after queue
admission. **Every admitted batch** can still run or drain on later reopen.
There is no one-turn ceiling: new relevant events can keep causing turns while
the bridge remains alive and the thread stays loaded, until disarmed or unloaded.
Do not treat a successful ordinary wake as resolving that limitation. No acceptance step should
use `--resume`, start an independent headless session or blindly retry uncertain
admission. Finish by confirming the real listener is gone.
