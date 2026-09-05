"""Private, bounded subprocess calls with cleanup of the owned POSIX session."""
from __future__ import annotations

import os
import signal
import subprocess
import tempfile
import threading
import time
from contextlib import contextmanager


class InvocationInterrupted(RuntimeError):
    """A termination request that must unwind through owned-session cleanup."""


def _signal_group(group, sig):
    try:
        os.killpg(group, sig)
    except ProcessLookupError:
        pass  # The process/group exited between inventory and the signal.


def _session_groups(session):
    # Inspect numeric identifiers only: never collect command lines or environments.
    try:
        inventory = subprocess.run(["ps", "-axo", "pid="], capture_output=True,
                                   text=True, timeout=2, check=True)
        pids = [int(value) for value in inventory.stdout.split()]
    except (OSError, ValueError, subprocess.SubprocessError) as exc:
        raise RuntimeError("process inventory failed; descendant cleanup cannot be verified") from exc
    if not pids or any(pid <= 0 for pid in pids):
        raise RuntimeError("invalid process inventory; descendant cleanup cannot be verified")
    groups = set()
    for pid in pids:
        try:
            if os.getsid(pid) == session:
                groups.add(os.getpgid(pid))
        except ProcessLookupError:
            continue
        except OSError as exc:
            raise RuntimeError("process session inspection failed; descendant cleanup cannot be verified") from exc
    return groups


@contextmanager
def _cleanup_on_termination():
    previous = None
    if threading.current_thread() is threading.main_thread():
        def interrupted(signum, frame):
            # InterruptedError is swallowed/retried by selector implementations.
            raise InvocationInterrupted("invocation interrupted by SIGTERM")
        previous = signal.signal(signal.SIGTERM, interrupted)
    try:
        yield
    finally:
        if previous is not None:
            signal.signal(signal.SIGTERM, previous)


@contextmanager
def _ignore_cleanup_interrupts():
    # A second Ctrl-C must not interrupt cleanup halfway through. signal.signal
    # is only available on the main thread; background callers retain handlers.
    previous = {}
    if threading.current_thread() is threading.main_thread():
        for sig in (signal.SIGINT, signal.SIGTERM):
            previous[sig] = signal.signal(sig, signal.SIG_IGN)
    try:
        yield
    finally:
        for sig, handler in previous.items():
            signal.signal(sig, handler)


def _reap(process):
    try:
        return process.communicate(timeout=3)
    except subprocess.TimeoutExpired as exc:
        # Avoid an unbounded communicate if a process outside the owned session
        # inherited a pipe. That unexpected situation must remain a visible error.
        process.stdout.close()
        process.stderr.close()
        process.wait(timeout=1)
        raise RuntimeError("process pipes remained open after owned-session cleanup") from exc


def _terminate_session(process, remaining):
    session = process.pid  # start_new_session=True makes this our session ID.
    frozen = {session}
    with _ignore_cleanup_interrupts():
        _signal_group(session, signal.SIGSTOP)
        try:
            # Freeze new groups before another inventory. Hollis's Setpgid child
            # stays in this session, and no frozen process can launch another one.
            for _ in range(5):
                groups = _session_groups(session)
                new_groups = groups - frozen
                for group in new_groups:
                    _signal_group(group, signal.SIGSTOP)
                frozen.update(new_groups)
                if not new_groups:
                    break
            else:
                raise RuntimeError("owned process session did not stabilize during cleanup")
        except BaseException:
            # If inventory fails, retain Hollis's chance to run its own deadline
            # cleanup. Never pretend killing just its group proves child cleanup.
            for group in frozen:
                _signal_group(group, signal.SIGCONT)
            try:
                process.communicate(timeout=max(0, remaining) + 3)
            except subprocess.TimeoutExpired:
                _signal_group(session, signal.SIGKILL)
                try:
                    _reap(process)
                except RuntimeError:
                    pass  # Preserve the inventory failure and unverified cleanup.
            raise
        for group in frozen - {session}:
            _signal_group(group, signal.SIGKILL)
        _signal_group(session, signal.SIGKILL)
        return _reap(process)


def invoke(args, env=None, timeout=40):
    start = time.monotonic()
    # Fail before starting work if sandbox/host permissions prohibit the
    # inventory needed to stop this invocation safely.
    _session_groups(os.getsid(0))
    # Forced parent termination skips Go defers. Keeping its prompt files in
    # this private directory makes cleanup independent of Hollis's exit path.
    with _cleanup_on_termination(), tempfile.TemporaryDirectory(prefix="hollis-image-invoke-") as temp:
        child_env = dict(os.environ if env is None else env, TMPDIR=temp)
        process = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, env=child_env, stdin=subprocess.DEVNULL,
                                   start_new_session=True)
        timed_out = False
        try:
            stdout, stderr = process.communicate(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
            stdout, stderr = _terminate_session(process, 0)
        except BaseException:
            _terminate_session(process, timeout - (time.monotonic() - start))
            raise
        return {"argv": [str(a) for a in args], "exit": process.returncode,
                "seconds": round(time.monotonic() - start, 3), "stdout": stdout,
                "stderr": stderr, "timed_out": timed_out}
