"""A full-screen, mouse-aware pager standing in for Codex in the touch tests.

Like Codex it runs in the alternate screen, asks for SGR mouse reports, and
moves its own view one row per wheel report. It shows the numbers 1-300 with
the newest at the bottom, so the top row says how far it has scrolled, and its
last row counts what it received: wheel reports, and every other byte — a
touch marker that leaked past tmux would land there.
"""
import fcntl
import os
import re
import signal
import struct
import sys
import termios
import tty

LINES = 300
WHEEL = re.compile(rb"\x1b\[<(64|65);\d+;\d+[Mm]")
PARTIAL = re.compile(rb"^(\x1b(\[(<(\d+(;\d*(;\d*)?)?)?)?)?)?$")

fd = sys.stdin.fileno()
saved = termios.tcgetattr(fd)
tty.setraw(fd)
offset = None
wheels = 0
other = b""


def rows():
    return struct.unpack("HHHH", fcntl.ioctl(1, termios.TIOCGWINSZ, b"\0" * 8))[0]


def draw(*_):
    global offset
    view = max(1, rows() - 1)
    top = LINES - view
    offset = top if offset is None else max(0, min(offset, top))
    out = ["\x1b[H\x1b[2J"]
    for i in range(view):
        out.append(f"{offset + i + 1}\r\n")
    out.append(f"wheel={wheels} other={other!r}")
    os.write(1, "".join(out).encode())


signal.signal(signal.SIGWINCH, draw)
os.write(1, b"\x1b[?1049h\x1b[?1000h\x1b[?1006h")
draw()
pending = b""
try:
    while True:
        try:
            chunk = os.read(fd, 4096)
        except InterruptedError:
            continue
        if not chunk:
            break
        pending += chunk
        while True:
            m = WHEEL.search(pending)
            if not m:
                break
            other += pending[:m.start()]
            wheels += 1
            offset += -1 if m.group(1) == b"64" else 1
            pending = pending[m.end():]
        # Keep only what could still become a wheel report.
        if not PARTIAL.match(pending):
            other += pending
            pending = b""
        draw()
finally:
    os.write(1, b"\x1b[?1006l\x1b[?1000l\x1b[?1049l")
    termios.tcsetattr(fd, termios.TCSADRAIN, saved)
