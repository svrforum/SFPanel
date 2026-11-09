"""Disposable PTY peer for mobile history tests. No real accounts or AI tools."""
import base64
import contextlib
import fcntl
import json
import os
import pty
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import threading


def stop(*_):
    raise SystemExit(0)


signal.signal(signal.SIGTERM, stop)
# SFPANEL_TEST_TMUX_DIR puts the socket where a spec can reach the same tmux,
# to copy its history as the panel's scrollback endpoint does.
shared = os.environ.get("SFPANEL_TEST_TMUX_DIR")
with (contextlib.nullcontext(shared) if shared else tempfile.TemporaryDirectory(prefix="sfpanel-touch-")) as directory:
    base = ["tmux", "-f", "/dev/null", "-S", directory + "/socket"]
    subprocess.run(base + ["new-session", "-d", "-s", "test", "-x", "80", "-y", "24",
                          os.environ.get("SFPANEL_TEST_TMUX_CMD", "sh -c 'seq 1 300; exec sleep 120'")],
                   check=True)
    master, slave = pty.openpty()
    client = None
    try:
        subprocess.run(base + ["set", "-g", "status", "off"], check=True)
        if os.environ.get("SFPANEL_TEST_TOUCH_SCROLL"):
            # What the panel's attach binds for touch scrolling; a Go test keeps
            # this file equal to touchScrollCommands (internal/feature/ai).
            bindings = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "internal",
                                    "feature", "ai", "testdata", "touch_scroll_bindings.json")
            with open(bindings) as f:
                for command in json.load(f):
                    subprocess.run(base + command, check=True)
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
        client = subprocess.Popen(base + ["set-option", "-t", "test", "mouse",
                                  os.environ.get("SFPANEL_TEST_TMUX_MOUSE", "on"), ";",
                                  "attach-session", "-t", "test"], stdin=slave, stdout=slave,
                                  stderr=slave, env=dict(os.environ, TERM="xterm-256color"),
                                  start_new_session=True)
        os.close(slave)

        def input_loop():
            for line in sys.stdin:
                value = json.loads(line)
                if value.get("type") == "resize":
                    fcntl.ioctl(master, termios.TIOCSWINSZ,
                                struct.pack("HHHH", value["rows"], value["cols"], 0, 0))
                    # The client has no controlling terminal here (the panel's
                    # pty.Start gives it one), so no SIGWINCH comes on its own
                    # and tmux would keep drawing at the first size.
                    client.send_signal(signal.SIGWINCH)
                else:
                    os.write(master, base64.b64decode(value["input"]))

        threading.Thread(target=input_loop, daemon=True).start()
        while True:
            data = os.read(master, 65536)
            if not data:
                break
            sys.stdout.buffer.write(data)
            sys.stdout.buffer.flush()
    finally:
        os.close(master)
        if client is not None:
            client.terminate()
            client.wait(timeout=5)
        subprocess.run(base + ["kill-server"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
