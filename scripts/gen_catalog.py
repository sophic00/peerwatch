#!/usr/bin/env python3
"""Render docs/schema/messages.yaml into the catalog tables in
docs/spec/protocol.md §7, and (optionally) verify they are current.

    python3 scripts/gen_catalog.py            # rewrite the tables
    python3 scripts/gen_catalog.py --check    # exit 1 if the tables are stale
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


# Explicit ID sets per section so non-contiguous additions (e.g. CONTROL_REQ
# in 7.5 alongside 0x28..0x2F) remain crystal clear and deterministic.
SECTIONS: list[tuple[str, set[int] | None]] = [
    ("7.1", set(range(0x01, 0x10))),
    ("7.2", set(range(0x10, 0x18))),
    ("7.3", set(range(0x18, 0x20))),
    ("7.4", set(range(0x20, 0x28))),
    ("7.5", set(range(0x28, 0x30)) | {0x35}),
    ("7.6", {0x30, 0x31, 0x32, 0x33, 0x34, 0x36, 0x37, 0x38}),
    ("7.7", None),  # reserved ranges
]


def load_yaml(path: Path) -> dict:
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
    buf = ""
    in_item = False
    for raw_line in text.splitlines():
        s = raw_line.strip()
        if not s or s.startswith("#"):
            continue
        if s.startswith("- {"):
            buf = s
            in_item = True
        elif in_item:
            buf += " " + s
        else:
            continue

        if in_item and buf.endswith("}"):
            in_item = False
            body = buf[3:-1]
            entry = {}
            for part in re.split(r",\s*(?=[a-z_]+:)", body):
                if ":" not in part:
                    continue
                k, v = part.split(":", 1)
                k = k.strip()
                v = v.strip()
                if v.startswith('"') and v.endswith('"'):
                    v = v[1:-1]
                entry[k] = v
            if "id" in entry:
                out["messages"].append(entry)
            elif "range" in entry:
                out["reserved"].append(entry)
    return out


def _sig_cell(schema: dict, m: dict) -> str:
    legend = schema.get("signed_legend", {})
    sig = m.get("signed", False)
    key = ("true" if sig else "false") if isinstance(sig, bool) else str(sig)
    return legend.get(key, {"true": "✍", "false": "—", "host": "✍(host)"}.get(key, "—"))


def render_section(schema: dict, ids: set[int] | None) -> str:
    if ids is None:
        lines = ["| Range | Use |", "|-------|-----|"]
        for r in schema.get("reserved", []):
            lines.append(f"| {r['range']} | {r['use']} |")
        return "\n".join(lines)

    rows = []
    for m in schema["messages"]:
        m = dict(m)
        mid = int(m["id"], 16) if isinstance(m.get("id"), str) else m["id"]
        if mid in ids:
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
    for sec, ids in SECTIONS:
        b, e = begin(sec), end(sec)
        if b not in out or e not in out:
            raise SystemExit(
                f"markers {b!r} / {e!r} not found in {PROTOCOL}.\n"
                "Add them around the table for that section once."
            )
        head, rest = out.split(b, 1)
        _, tail = rest.split(e, 1)
        out = f"{head}{b}\n{render_section(schema, ids)}\n{e}{tail}"
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
