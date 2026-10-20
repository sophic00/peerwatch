#!/usr/bin/env python3
"""Render docs/schema/messages.yaml into the catalog tables in
docs/spec/protocol.md §7, and (optionally) verify they are current.

    python3 scripts/gen_catalog.py            # rewrite the tables
    python3 scripts/gen_catalog.py --check    # exit 1 if the tables are stale

Each table in protocol.md §7 is delimited by per-section markers:

    <!-- CATALOG:7.2 -->
    ...
    <!-- /CATALOG:7.2 -->

Everything outside those markers — the legend, the room-object schemas, the
designated-responder rule, the prose notes — is hand-written and is never
touched. Per-section markers rather than one big block, because the prose is
interleaved with the tables and must survive a regeneration.

Why this exists: the 32-row message catalog used to be maintained by hand in
two files (protocol.md and a now-deleted protocol.md copy), and the
signed/unsigned split in three more. That is a guaranteed drift. The YAML is
the source of truth; the markdown is output.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

DOCS = Path(__file__).resolve().parent.parent / "docs"
SCHEMA = DOCS / "schema" / "messages.yaml"
PROTOCOL = DOCS / "spec" / "protocol.md"

def begin(sec: str) -> str:
    return f"<!-- CATALOG:{sec} -->"


def end(sec: str) -> str:
    return f"<!-- /CATALOG:{sec} -->"

# (section number, message-id range). The heading text lives in the markdown,
# not here, so that renaming a heading does not require touching this file.
SECTIONS = [
    ("7.1", 0x01, 0x0F),
    ("7.2", 0x10, 0x17),
    ("7.3", 0x18, 0x1F),
    ("7.4", 0x20, 0x27),
    ("7.5", 0x28, 0x2F),
    ("7.6", 0x30, 0x3F),
    ("7.7", None, None),      # reserved ranges
]


def load_yaml(path: Path) -> dict:
    """Minimal YAML loader for this file's subset.

    The repo has no Python dependencies by design (CI must be able to run the
    lint on a bare runner), and this file uses only: nested maps, lists of
    flow maps, and quoted strings. If the schema grows past that, switch to
    `pip install pyyaml` rather than growing this function.
    """
    try:
        import yaml  # type: ignore
    except ImportError:
        return _fallback_load(path)

    with path.open() as fh:
        return yaml.safe_load(fh)


def _fallback_load(path: Path) -> dict:
    """Parse the flow-map subset without PyYAML."""
    text = path.read_text()
    out: dict = {"messages": [], "reserved": []}
    for line in text.splitlines():
        s = line.strip()
        if s.startswith("- {") and s.endswith("}"):
            body = s[3:-1]
            entry = {}
            for part in re.split(r",\s*(?=[a-z_]+:)", body):
                if ":" not in part:
                    continue
                k, v = part.split(":", 1)
                k = k.strip()
                v = v.strip().strip('"')
                if v.startswith('"') and v.endswith('"'):
                    v = v[1:-1]
                entry[k] = v
            # route to messages or reserved by the presence of an id
            if "id" in entry:
                out["messages"].append(entry)
            elif "range" in entry:
                out["reserved"].append(entry)
    return out


def _legend(schema: dict) -> dict:
    legend = schema.get("signed_legend", {})
    return legend


def _sig_cell(schema: dict, m: dict) -> str:
    legend = _legend(schema)
    sig = m.get("signed", False)
    key = ("true" if sig else "false") if isinstance(sig, bool) else str(sig)
    return legend.get(key, {"true": "✍", "false": "—"}.get(key, "—"))


def render_section(schema: dict, sec: str, lo, hi) -> str:
    """Render one table. `lo is None` renders the reserved-range table."""
    if lo is None:
        lines = ["| Range | Use |", "|-------|-----|"]
        for r in schema.get("reserved", []):
            lines.append(f"| {r['range']} | {r['use']} |")
        return "\n".join(lines)

    rows = []
    for m in schema["messages"]:
        m = dict(m)
        mid = int(m["id"], 16) if isinstance(m.get("id"), str) else m["id"]
        if lo <= mid <= hi:
            m["_id"] = mid
            rows.append(m)

    lines = [
        "| ID | Type | ✍ | Payload | Channel | When |",
        "|---:|------|---|---------|---------|------|",
    ]
    for m in sorted(rows, key=lambda x: x["_id"]):
        payload = str(m.get("payload", "")).replace("|", "\\|")
        when = str(m.get("when", "")).replace("|", "\\|")
        lines.append(
            f"| 0x{m['_id']:02X} | `{m['name']}` | {_sig_cell(schema, m)} "
            f"| {payload} | {m.get('channel', 'control')} | {when} |"
        )
    return "\n".join(lines)


def splice(protocol_text: str, schema: dict) -> str:
    out = protocol_text
    for sec, lo, hi in SECTIONS:
        b, e = begin(sec), end(sec)
        if b not in out or e not in out:
            raise SystemExit(
                f"markers {b!r} / {e!r} not found in {PROTOCOL}.\n"
                "Add them around the table for that section once; the "
                "generator only replaces what is between them."
            )
        head, rest = out.split(b, 1)
        _, tail = rest.split(e, 1)
        out = f"{head}{b}\n{render_section(schema, sec, lo, hi)}\n{e}{tail}"
    return out


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true",
                    help="exit non-zero if the rendered catalog differs")
    args = ap.parse_args()

    schema = load_yaml(SCHEMA)
    original = PROTOCOL.read_text()
    updated = splice(original, schema)

    if args.check:
        if updated != original:
            print("catalog is STALE — run `python3 scripts/gen_catalog.py`",
                  file=sys.stderr)
            return 1
        print("catalog OK")
        return 0

    PROTOCOL.write_text(updated)
    print(f"rendered {len(schema['messages'])} messages into {PROTOCOL.name}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
