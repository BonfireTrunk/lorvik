# Goose delivery-boundary check

**This acceptance check requires a human-installed Goose 1.57.0 harness.
Nothing in this file was run while building the adapter.** Do
not use a harness binary until you have decided to install and run it.

First verify the installed release:

```sh
goose --version
goose acp --help
```

Expected: version `1.57.0` and ACP help that includes `--with-builtin`. Keep the
source clone at the same tag when comparing behavior.

For the no-model boundary, run this independent recorder from the Lorvik
repository root. It launches the production Bun proxy, with temporary Python peers
named `goose` and `lorvik` ahead of all other executables. It does not invoke
the installed Goose, contact a board, or call a provider. Offline tests
already run are recorded separately in the README.

```sh
python3 - <<'PY'
import json, os, pathlib, selectors, shutil, subprocess, sys, tempfile, time

adapter = pathlib.Path('skills/lorvik/adapters/goose/lorvik-wake.ts').resolve()
bun = shutil.which('bun')
assert bun and adapter.is_file()
root = pathlib.Path(tempfile.mkdtemp(prefix='lorvik-goose-acceptance-'))
(root / '.lorvik.toml').write_text('')
label = 'goose-acceptance-01'
peer = '''import json, os, sys
def log(value):
    with open(os.environ['WAKE_PROBE_LOG'], 'a') as output:
        output.write(json.dumps(value) + '\\n')
log({'kind':'peer', 'pid':os.getpid()})
for line in sys.stdin:
    rpc = json.loads(line)
    log({'kind':'rpc', 'rpc':rpc})
    method = rpc.get('method')
    if method == 'initialize': result = {'protocolVersion':1}
    elif method == 'session/new': result = {'sessionId':'native-probe-chat'}
    elif method == 'session/prompt': result = {'stopReason':'end_turn'}
    else: continue
    print(json.dumps({'jsonrpc':'2.0','id':rpc['id'],'result':result}), flush=True)
'''
listener = '''import json, os, sys
with open(os.environ['WAKE_PROBE_LISTENER'], 'w') as output:
    json.dump({'pid':os.getpid(), 'argv':__import__('sys').argv[1:],
               'label':os.environ['LORVIK_SESSION'], 'config':os.environ['LORVIK_CONFIG']}, output)
print('now checker commented on #42 "literal $(never-execute)"', flush=True)
for _ in sys.stdin:
    pass
'''
for name, source in [('goose', peer), ('lorvik', listener)]:
    path = root / name
    path.write_text('#!' + sys.executable + '\n' + source)
    path.chmod(0o700)
env = dict(os.environ, PATH=str(root), LORVIK_SESSION=label,
           LORVIK_CONFIG=str(root / 'unused-agent.toml'),
           WAKE_PROBE_LOG=str(root / 'rpc.jsonl'),
           WAKE_PROBE_LISTENER=str(root / 'listener.json'))
proxy = subprocess.Popen([bun, str(adapter), '--arm'], cwd=root, env=env,
                         stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                         stderr=open(root / 'stderr.log', 'w'), text=True)
selector = selectors.DefaultSelector()
selector.register(proxy.stdout, selectors.EVENT_READ)
def request(request_id, method, params):
    proxy.stdin.write(json.dumps({'jsonrpc':'2.0','id':request_id,'method':method,'params':params}) + '\n')
    proxy.stdin.flush()
    assert selector.select(5), 'No ACP response; inspect stderr.log'
    reply = json.loads(proxy.stdout.readline())
    assert reply.get('id') == request_id and 'error' not in reply, reply
try:
    request(1, 'initialize', {'protocolVersion':1,'clientCapabilities':{}})
    request(2, 'session/new', {'cwd':str(root),'mcpServers':[]})
    deadline = time.monotonic() + 5
    while True:
        records = [json.loads(line) for line in (root / 'rpc.jsonl').read_text().splitlines()]
        prompts = [item['rpc'] for item in records if item.get('rpc', {}).get('method') == 'session/prompt']
        if prompts: break
        assert time.monotonic() < deadline, 'No injection; inspect probe files'
        time.sleep(0.02)
    observed = json.loads((root / 'listener.json').read_text())
    assert observed['argv'] == ['--session', label, 'inbox', '--follow', '--exit-on-eof'], observed
    assert observed['label'] == label and observed['config'] == env['LORVIK_CONFIG']
    assert len(prompts) == 1 and prompts[0]['params']['sessionId'] == 'native-probe-chat'
    assert 'literal $(never-execute)' in prompts[0]['params']['prompt'][0]['text']
    print(json.dumps({'listener':observed,'injection':prompts[0]}, indent=2))
finally:
    proxy.stdin.close()
    try: proxy.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proxy.terminate()
        proxy.wait(timeout=5)
    selector.close()
for pid in [observed['pid']] + [item['pid'] for item in records if item.get('kind') == 'peer']:
    try: os.kill(pid, 0)
    except ProcessLookupError: pass
    else: raise AssertionError(f'Probe child still exists: {pid}')
print('Inspect retained evidence in', root)
PY
```

Expected: the printed listener record has the explicit label/config and exactly
`--session goose-acceptance-01 inbox --follow --exit-on-eof`; the injection is one
`session/prompt` addressed to `native-probe-chat` with the literal event. The
proxy receives EOF during teardown, which stops both peers. Inspect the recorded
PIDs and confirm they no longer exist. This records the delivery boundary;
it does not prove real Goose admission or a client's rendering.

After the human configures/logs in to a Goose provider and provides a running
Lorvik board, set up the ACP subprocess exactly as README describes, including
`--arm`, a unique `LORVIK_SESSION`, and `LORVIK_CONFIG`. Create one new visible
chat. From its shell tool run `lorvik session` and confirm the explicit label,
then claim a test card and read `lorvik --session LABEL inbox` once to establish
its cursor. While idle, have the human comment on that card. Confirm a new
visible turn receives the exact line, uses `lorvik --session LABEL inbox`, and
acts. Repeat while a turn is busy: the wake must wait until it ends. Inspect the
client's subprocess log for the delivery diagnostic. Cancel a turn and check
that later board events can still wake it: cancellation ends a turn, it does not
disarm the proxy.

Finally close the chat and verify its ACP subprocess really terminates (or
sends session/close), both children exit, and a later comment creates no turn.
Closing revokes queued data but cannot retract an already admitted board prompt:
every admitted batch may execute, so do not treat close as a one-turn ceiling.
Test hiding a chat tab separately. If the client leaves its connection alive,
the README close gap applies: do not use this adapter in that client unless the
human accepts the explicit opt-in risk. Do not run `goose run --resume` for this
check. Actual provider/board/client checks remain **UNTESTED** until performed.
