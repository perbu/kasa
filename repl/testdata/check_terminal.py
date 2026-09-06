"""Replay the real renderer's output to check scrollback and prompt integrity.

Requires pyte (python -m pip install pyte). From the repository root, run:
    python repl/testdata/check_terminal.py

No Kubernetes cluster or model API is used. The legacy fixture deliberately
uses the original, oversized tea.Println call to prove the reproduction fails.
"""

import os
from pathlib import Path
import re
import subprocess
import tempfile

import pyte


def replay(path):
    screen = pyte.HistoryScreen(80, 24, history=20000)
    data = path.read_bytes().decode()
    # pyte doesn't implement Kitty's keyboard protocol. These sequences
    # negotiate input handling and have no effect on terminal output cells.
    data = re.sub(r"\x1b\[[?>=][0-9;]*u", "", data)
    pyte.Stream(screen).feed(data)
    history = [
        "".join(row[x].data for x in range(80)).rstrip()
        for row in screen.history.top
    ]
    return history + [row.rstrip() for row in screen.display]


def verify(rows):
    start = rows.index("Logs: default/test")
    expected = ["Logs: default/test"]
    for i in range(150):
        line = f"row-{i:04d} " + "x" * 151
        expected.extend(line[j:j + 79] for j in range(0, len(line), 79))
    expected.extend([
        "git: commit", "Committed: test", "Push with /push when ready.",
        "─" * 64 + " SAFE | no plan", "> Type a message...",
    ])
    actual = rows[start:start + len(expected)]
    assert actual == expected, "lost, reordered or corrupted output, or damaged prompt"
    assert all(not row for row in rows[start + len(expected):]), "stale rows below prompt"


with tempfile.TemporaryDirectory(prefix="kasa-terminal-") as directory:
    subprocess.run(
        ["go", "test", "./repl", "-run", "^TestTerminalOutput$", "-count=1"],
        cwd=Path(__file__).resolve().parents[2],
        env={**os.environ, "KASA_TERMINAL_CAPTURE": directory},
        check=True,
    )
    verify(replay(Path(directory) / "queued.ansi"))
    try:
        verify(replay(Path(directory) / "legacy.ansi"))
    except (AssertionError, ValueError):
        print("PASS: queued output preserves every row and the prompt; legacy output corrupts.")
    else:
        raise AssertionError("legacy fixture no longer reproduces the corruption")
