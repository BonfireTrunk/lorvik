# SPDX-License-Identifier: Apache-2.0
"""Offline unit tests; no harness, Lorvik service or model is started."""

import contextlib
import io
import os
from pathlib import Path
import runpy
import selectors
import sys
import subprocess
import tempfile
import unittest


wake = runpy.run_path(str(Path(__file__).with_name('lorvik-wake.py')))
Delivery = wake['Delivery']


class Child:
    def __init__(self):
        self.result = None

    def poll(self):
        return self.result


class Queue:
    def __init__(self):
        self.calls = []
        self.children = []

    def __call__(self, arguments, **options):
        self.calls.append((arguments, options))
        child = Child()
        self.children.append(child)
        return child


class DeliveryTests(unittest.TestCase):
    def test_one_literal_batch_after_coalescing_deadline(self):
        queue = Queue()
        delivery = Delivery('fake-codex', 'unix://', 'thread', queue)
        delivery.add('first $(never-execute)', 0)
        delivery.add('second', wake['BATCH_SECONDS'] / 2)
        delivery.tick(wake['BATCH_SECONDS'] / 2)
        self.assertEqual(queue.calls, [])
        delivery.tick(wake['BATCH_SECONDS'])
        args, options = queue.calls[0]
        self.assertEqual(args[:6], ['fake-codex', 'queue', '--remote', 'unix://', '--thread', 'thread'])
        self.assertEqual(len(args), 8)
        self.assertEqual(args[6], '--message')
        self.assertIn('first $(never-execute)\nsecond', args[7])
        self.assertNotIn('--resume', args)
        self.assertFalse(options['shell'])

    def test_slow_delivery_keeps_newest_pending_lines_with_bounded_bytes(self):
        queue = Queue()
        delivery = Delivery('fake-codex', 'unix://', 'thread', queue)
        delivery.add('already sending', 0)
        delivery.tick(wake['BATCH_SECONDS'])
        line = 'x' * (wake['MAX_LINE_BYTES'] - 16)
        count = 20
        for number in range(count):
            delivery.add(f'{number:02d}:{line}', 1)
            delivery.tick(1)
        self.assertEqual(len(queue.calls), 1)
        self.assertLessEqual(delivery.bytes, wake['MAX_BATCH_BYTES'])
        retained = len(delivery.lines)
        self.assertEqual(delivery.dropped, count - retained)
        self.assertTrue(delivery.lines[-1][0].startswith('19:'))
        queue.children[0].result = 0
        with contextlib.redirect_stderr(io.StringIO()) as diagnostics:
            delivery.tick(2)
        self.assertEqual(len(queue.calls), 2)
        message = queue.calls[1][0][-1]
        self.assertIn(f'Dropped {count-retained} notification lines', message)
        self.assertIn('run lorvik inbox', message)
        self.assertIn(f'dropped {count-retained}', diagnostics.getvalue())
        self.assertNotIn('00:', message)

    def test_line_limit_and_queue_failure_do_not_retry(self):
        queue = Queue()
        delivery = Delivery('fake-codex', 'unix://', 'thread', queue)
        for number in range(wake['MAX_BATCH_LINES'] + 5):
            delivery.add(str(number), 0)
        self.assertEqual(len(delivery.lines), wake['MAX_BATCH_LINES'])
        self.assertEqual(delivery.dropped, 5)
        with contextlib.redirect_stderr(io.StringIO()):
            delivery.tick(1)
            queue.children[0].result = 9
            with self.assertRaises(SystemExit):
                delivery.tick(2)
        self.assertEqual(len(queue.calls), 1)

    def test_queue_timeout_is_checked_without_blocking_reader(self):
        queue = Queue()
        delivery = Delivery('fake-codex', 'unix://', 'thread', queue)
        delivery.add('event', 0)
        delivery.tick(1)
        delivery.add('new event while queue is blocked', 2)
        self.assertEqual(len(delivery.lines), 1)
        with contextlib.redirect_stderr(io.StringIO()) as diagnostic:
            with self.assertRaises(SystemExit):
                delivery.tick(1 + wake['QUEUE_SECONDS'])
        self.assertIn('delivery is unknown', diagnostic.getvalue())
        self.assertEqual(len(queue.calls), 1)

    def test_fragmented_oversized_line_is_counted_once_and_reader_resynchronizes(self):
        queue = Queue()
        delivery = Delivery('fake-codex', 'unix://', 'thread', queue)
        scanner = wake['LineScanner']()
        scanner.feed(b'x' * wake['MAX_LINE_BYTES'], delivery, 0)
        scanner.feed(b'more', delivery, 0)
        scanner.feed(b'even more', delivery, 0)
        self.assertEqual(delivery.dropped, 1)
        self.assertEqual(scanner.partial, b'')
        scanner.feed(b'\nvalid \xe2', delivery, 0)
        scanner.feed(b'\x82\xac\n', delivery, 0)
        scanner.feed(b'y' * (wake['MAX_LINE_BYTES'] + 1) + b'\nafter\n', delivery, 0)
        self.assertEqual(delivery.dropped, 2)
        self.assertEqual([line for line, _ in delivery.lines], ['valid €', 'after'])
        with contextlib.redirect_stderr(io.StringIO()):
            delivery.tick(wake['BATCH_SECONDS'])
        message = queue.calls[0][0][-1]
        self.assertIn('Dropped 2 notification lines', message)
        self.assertIn('valid €\nafter', message)

    def test_dropped_only_batch_sends_recovery_notice_with_bounded_overhead(self):
        queue = Queue()
        delivery = Delivery('fake-codex', 'unix://', 'thread', queue)
        wake['LineScanner']().feed(b'x' * (wake['MAX_LINE_BYTES'] + 1), delivery, 0)
        self.assertEqual(len(delivery.lines), 0)
        with contextlib.redirect_stderr(io.StringIO()):
            delivery.tick(wake['BATCH_SECONDS'])
        self.assertEqual(len(queue.calls), 1)
        self.assertIn('Dropped 1 notification lines', queue.calls[0][0][-1])
        self.assertIn('run lorvik inbox', queue.calls[0][0][-1])
        notice = wake['command_message']([], wake['MAX_DROPPED'])
        self.assertLessEqual(len(notice.encode()), 512)

    def test_listener_uses_open_unwritten_eof_pipe(self):
        read_fd, write_fd = os.pipe()
        stdin = os.fdopen(write_fd, 'wb')
        calls = []
        class Listener:
            pass
        listener = Listener()
        listener.stdin = stdin
        def spawn(arguments, **options):
            calls.append((arguments, options))
            return listener
        try:
            self.assertIs(wake['start_listener']('fake-lorvik', spawn), listener)
            self.assertEqual(calls[0][0], ['fake-lorvik', 'inbox', '--follow', '--exit-on-eof'])
            self.assertEqual(calls[0][1]['stdin'], subprocess.PIPE)
            os.set_blocking(read_fd, False)
            with self.assertRaises(BlockingIOError):
                os.read(read_fd, 1)
            stdin.close()
            self.assertEqual(os.read(read_fd, 1), b'')
        finally:
            stdin.close()
            os.close(read_fd)


class StopTests(unittest.TestCase):
    def test_stale_stopper_cannot_erase_new_generation_stop(self):
        with tempfile.TemporaryDirectory(prefix='lorvik-stop-unit-') as temporary:
            previous = os.environ.get('XDG_RUNTIME_DIR')
            os.environ['XDG_RUNTIME_DIR'] = temporary
            lock_a = lock_b = None
            try:
                lock_a, generation_a = wake['acquire_lock']('thread')
                # Stop A reads generation A, then pauses before writing its marker.
                observed_a = os.pread(lock_a, 32, 0)
                os.close(lock_a)
                lock_a = None
                lock_b, generation_b = wake['acquire_lock']('thread')
                with contextlib.redirect_stderr(io.StringIO()):
                    wake['stop_bridge']('thread')
                marker_b = Path(wake['stop_path']('thread', generation_b))
                self.assertTrue(marker_b.exists())
                # Stop A resumes after stop B. Both independent markers must survive.
                wake['write_stop']('thread', observed_a)
                self.assertTrue(marker_b.exists())
                self.assertTrue(Path(wake['stop_path']('thread', generation_a)).exists())
                self.assertNotEqual(generation_a, generation_b)
                os.close(lock_b)
                lock_b = None
                with contextlib.redirect_stderr(io.StringIO()) as diagnostic:
                    self.assertEqual(wake['stop_bridge']('thread'), 0)
                self.assertIn('no active bridge', diagnostic.getvalue())
            finally:
                for descriptor in (lock_a, lock_b):
                    if descriptor is not None:
                        os.close(descriptor)
                if previous is None:
                    os.environ.pop('XDG_RUNTIME_DIR', None)
                else:
                    os.environ['XDG_RUNTIME_DIR'] = previous


class BridgeLifetimeTests(unittest.TestCase):
    def test_actual_bridge_sigkill_closes_fake_listener_stdin(self):
        with tempfile.TemporaryDirectory(prefix='lorvik-eof-unit-') as temporary:
            root = Path(temporary)
            (root / '.lorvik.toml').touch()
            (root / 'runtime').mkdir(mode=0o700)
            ready_path, eof_path = root / 'ready', root / 'eof'
            os.mkfifo(ready_path, mode=0o600)
            os.mkfifo(eof_path, mode=0o600)
            ready_fd = os.open(ready_path, os.O_RDWR | os.O_NONBLOCK)
            eof_fd = os.open(eof_path, os.O_RDWR | os.O_NONBLOCK)
            listener = root / 'fake-lorvik.py'
            listener.write_text(
                '#!/usr/bin/env python3\nimport os,sys,signal\nsignal.alarm(5)\n'
                'from pathlib import Path\n'
                'assert sys.argv[1:] == ["inbox","--follow","--exit-on-eof"]\n'
                'Path(os.environ["READY_FIFO"]).write_text("ready")\n'
                'assert sys.stdin.buffer.read() == b""\n'
                'Path(os.environ["EOF_FIFO"]).write_text("EOF")\n'
            )
            listener.chmod(0o700)
            env = {key: value for key, value in os.environ.items()
                   if key not in ('CLAUDE_CODE_SESSION_ID', 'OPENCODE_SESSION_ID', 'LORVIK_SESSION')}
            env.update(CODEX_THREAD_ID='eof-test', LORVIK_CONFIG=str(root/'agent.toml'),
                       XDG_RUNTIME_DIR=str(root/'runtime'), READY_FIFO=str(ready_path), EOF_FIFO=str(eof_path))
            adapter = Path(__file__).with_name('lorvik-wake.py').resolve()
            child = subprocess.Popen([sys.executable, '-B', str(adapter), '--lorvik', str(listener),
                                      '--codex', str(root/'never-run-codex')],
                                     cwd=root, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            try:
                with selectors.DefaultSelector() as selector:
                    selector.register(ready_fd, selectors.EVENT_READ)
                    self.assertTrue(selector.select(5), 'fake listener did not become ready')
                    self.assertEqual(os.read(ready_fd, 5), b'ready')
                    selector.unregister(ready_fd)
                    selector.register(eof_fd, selectors.EVENT_READ)
                    child.kill()
                    self.assertEqual(child.wait(timeout=5), -9)
                    self.assertTrue(selector.select(5), 'bridge death did not close listener stdin')
                    self.assertEqual(os.read(eof_fd, 3), b'EOF')
            finally:
                if child.poll() is None:
                    child.kill()
                    child.wait(timeout=5)
                child.stderr.close()
                os.close(ready_fd)
                os.close(eof_fd)


if __name__ == '__main__':
    unittest.main()
