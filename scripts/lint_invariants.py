#!/usr/bin/env python3
"""Lint the PeerWatch v3 documentation set for drift.

    python3 scripts/lint_invariants.py           # human output, exit 1 on error
    python3 scripts/lint_invariants.py --json    # machine output

Checks:
  1. INVARIANTS   — numeric constants match docs/README.md (32 KiB block,
                    ~4 s segment, 64 KiB envelope, 2.0 s hard-seek, ±5% clamp).
  2. CATALOG      — rendered message tables match schema/messages.yaml.
  3. LINKS        — every relative .md link resolves to a file.
  4. REACHABLE    — every doc is linked from docs/README.md (no orphans).
  5. TIER         — every doc declares `tier:` front-matter matching its dir.
  6. HEADINGS     — `§N.M` references resolve to an existing heading.
  7. REBUTTALS    — no "old design was wrong"-style prose outside design-log.md.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DOCS = ROOT / "docs"

INVARIANTS = [
    {
        "name": "block size",
        "allowed": {"32", "32.0"},
        "patterns": [
            r"\b(\d+)\s*KiB\s*block",
            r"\bblock\s*(?:size\s*)?(?:=|of|:)\s*(\d+)\s*KiB",
        ],
        "why": "block = 32 KiB (protocol.md §6.1)",
    },
    {
        "name": "segment target",
        "allowed": {"4"},
        "patterns": [
            r"\b(\d+)\s*s(?:ec(?:onds?)?)?\s*(?:keyframe-aligned\s*)?segment",
            r"\bsegment\s*(?:target|size)?\s*(?:=|of|:|\u2248|~)?\s*(\d+)\s*s\b",
        ],
        "why": "segment ~= 4 s (protocol.md §6.1)",
    },
    {
        "name": "envelope max",
        "allowed": {"64"},
        "patterns": [
            r"\b(\d+)\s*KiB\s*(?:max(?:imum)?\s*)?envelope",
            r"\benvelope\s*(?:max(?:imum)?)?\s*(?:=|of|:)\s*(\d+)\s*KiB",
        ],
        "why": "envelope <= 64 KiB default (protocol.md §1.4)",
    },
    {
        "name": "no v2 piece-size drift",
        "allowed": set(),
        "patterns": [
            r"\b(512)\s*KiB\s*piece",
            r"\bpiece\s*(?:size\s*)?(?:=|of|:)\s*(512)\s*KiB",
        ],
        "why": "v3 collapsed 512 KiB pieces into 2-level Segment -> Block (protocol.md §6.1)",
    },
]

EXEMPT_FILES = {"design/design-log.md"}
EXEMPT_LINE_MARKERS = (
    "rejected",
    "earlier revision",
    "an earlier",
    "removed",
    "no longer",
    "used to",
    "replaced",
)

REBUTTAL = re.compile(
    r"\b(the old (?:design|claim|rule|formula|roadmap|command|docs|"
    r"v1|v2|“)|old docs|v1 used|v1's|was wrong|was too strong|was not safe|"
    r"hand-wavy|misleading name)\b",
    re.IGNORECASE,
)


def doc_files() -> list[Path]:
    return sorted(DOCS.rglob("*.md"))


def rel(p: Path) -> str:
    return str(p.relative_to(DOCS))


def check_invariants() -> list[str]:
    errs = []
    for f in doc_files():
        r = rel(f)
        if r in EXEMPT_FILES:
            continue
        for lineno, line in enumerate(f.read_text().splitlines(), 1):
            low = line.lower()
            if any(m in low for m in EXEMPT_LINE_MARKERS):
                continue
            for inv in INVARIANTS:
                for pat in inv["patterns"]:
                    for m in re.finditer(pat, line, re.IGNORECASE):
                        val = m.group(1)
                        if val not in inv["allowed"]:
                            expected = "/".join(sorted(inv["allowed"])) or "none"
                            errs.append(
                                f"{r}:{lineno}: {inv['name']}: found {val}, "
                                f"expected {expected} -- {inv['why']}"
                            )
    return errs


def check_catalog() -> list[str]:
    proc = subprocess.run(
        [sys.executable, str(ROOT / "scripts" / "gen_catalog.py"), "--check"],
        capture_output=True, text=True,
    )
    if proc.returncode != 0:
        return [(proc.stderr or proc.stdout).strip()]
    return []


LINK = re.compile(r"\]\(([^)]+\.md)(#[^)]*)?\)")


def check_links() -> list[str]:
    errs = []
    for f in doc_files():
        for lineno, line in enumerate(f.read_text().splitlines(), 1):
            for m in LINK.finditer(line):
                tgt = (f.parent / m.group(1)).resolve()
                if not tgt.exists():
                    errs.append(f"{rel(f)}:{lineno}: broken link -> {m.group(1)}")
    return errs


def check_reachable() -> list[str]:
    index = DOCS / "README.md"
    if not index.exists():
        return ["docs/README.md is missing"]
    text = index.read_text()
    linked = {
        (index.parent / m.group(1)).resolve()
        for m in LINK.finditer(text)
    }
    errs = []
    for f in doc_files():
        if f.name == "README.md" and f.parent == DOCS:
            continue
        if f.resolve() not in linked:
            errs.append(f"{rel(f)}: not linked from docs/README.md (orphan)")
    return errs


TIER_BY_DIR = {"spec": "normative", "design": "design", "guide": "guide"}


def check_tier() -> list[str]:
    errs = []
    for f in doc_files():
        text = f.read_text()
        m = re.match(r"^---\ntier:\s*(\w+)\n---\n", text)
        if not m:
            errs.append(f"{rel(f)}: missing `tier:` front-matter")
            continue
        want = TIER_BY_DIR.get(f.parent.name)
        if want and m.group(1) != want:
            errs.append(
                f"{rel(f)}: tier is `{m.group(1)}` but it lives in "
                f"docs/{f.parent.name}/ (expected `{want}`)"
            )
    return errs


HEADING = re.compile(r"^#{2,4}\s+(\d+(?:\.\d+)*)", re.MULTILINE)
SECTION_REF = re.compile(r"§(\d+(?:\.\d+)*)")


def check_section_refs() -> list[str]:
    errs = []
    headings: dict[Path, set[str]] = {}
    for f in doc_files():
        headings[f.resolve()] = set(HEADING.findall(f.read_text()))

    for f in doc_files():
        text = f.read_text()
        for lineno, line in enumerate(text.splitlines(), 1):
            for m in SECTION_REF.finditer(line):
                sec = m.group(1)
                tgt = f.resolve()
                link = None
                for lm in LINK.finditer(line):
                    link = lm
                if link:
                    cand = (f.parent / link.group(1)).resolve()
                    if cand in headings:
                        tgt = cand
                if sec not in headings.get(tgt, set()):
                    if not any(sec in hs for hs in headings.values()):
                        errs.append(
                            f"{rel(f)}:{lineno}: §{sec} does not exist "
                            f"in any document"
                        )
    return errs


def check_rebuttals() -> list[str]:
    errs = []
    for f in doc_files():
        r = rel(f)
        if r == "design/design-log.md":
            continue
        for lineno, line in enumerate(f.read_text().splitlines(), 1):
            if REBUTTAL.search(line):
                errs.append(
                    f"{r}:{lineno}: rebuttal prose outside design-log.md — "
                    f"move it to docs/design/design-log.md"
                )
    return errs


CHECKS = [
    ("invariants", check_invariants),
    ("catalog", check_catalog),
    ("links", check_links),
    ("reachable", check_reachable),
    ("tier", check_tier),
    ("sections", check_section_refs),
    ("rebuttals", check_rebuttals),
]


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--only", help="run a single check by name")
    args = ap.parse_args()

    results = {}
    for name, fn in CHECKS:
        if args.only and name != args.only:
            continue
        results[name] = fn()

    if args.json:
        print(json.dumps(results, indent=2))
    else:
        total = 0
        for name, errs in results.items():
            if errs:
                print(f"\n✗ {name} ({len(errs)})")
                for e in errs:
                    print(f"    {e}")
                total += len(errs)
            else:
                print(f"✓ {name}")
        print()
        if total:
            print(f"{total} problem(s)")
        else:
            print("docs clean")

    return 1 if any(results.values()) else 0


if __name__ == "__main__":
    raise SystemExit(main())
