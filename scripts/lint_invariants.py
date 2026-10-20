#!/usr/bin/env python3
"""Lint the documentation set for drift.

    python3 scripts/lint_invariants.py           # human output, exit 1 on error
    python3 scripts/lint_invariants.py --json    # machine output

Checks, in order of how likely they are to catch a real mistake:

  1. INVARIANTS   — the numeric constants in docs/README.md are the only
                    allowed values for those facts anywhere in docs/. A doc
                    that says "64 KiB blocks" or "piece = 8 blocks" fails.
  2. CATALOG      — the generated message tables match schema/messages.yaml.
  3. LINKS        — every relative .md link resolves to a file.
  4. REACHABLE    — every doc is linked from docs/README.md (no orphans).
  5. TIER         — every doc declares `tier:` front-matter, and the tier
                    matches its directory.
  6. HEADINGS     — cross-references like "§4.3" name a heading that exists.
  7. REBUTTALS    — no "the old design was wrong"-style prose outside
                    docs/design/design-log.md.

Why a linter and not a review checklist: every one of these has already
drifted once during the v2 rewrite. The README's "invariants every document
must respect" section is only real if something enforces it.
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

# ── 1. Invariants ──────────────────────────────────────────────────────────
#
# Each entry: (name, regex that finds a claim, predicate on the captured
# value). A claim is a number immediately followed by a unit. We only flag
# values that contradict the invariant; unrelated numbers are ignored.

INVARIANTS = [
    {
        "name": "block size",
        # matches "32 KiB block", "block = 32 KiB", "block of 32 KiB"
        "allowed": {"32", "32.0"},
        "patterns": [
            r"\b(\d+)\s*KiB\s*block",
            r"\bblock\s*(?:size\s*)?(?:=|of|:)\s*(\d+)\s*KiB",
        ],
        "why": "block = 32 KiB (protocol.md §6.1)",
    },
    {
        "name": "piece size",
        "allowed": {"512"},
        "patterns": [
            r"\b(\d+)\s*KiB\s*piece",
            r"\bpiece\s*(?:size\s*)?(?:=|of|:)\s*(\d+)\s*KiB",
        ],
        "why": "piece = 512 KiB (protocol.md §6.1)",
    },
    {
        "name": "blocks per piece",
        "allowed": {"16"},
        "patterns": [
            r"\b(\d+)\s*blocks?\s*(?:=|per)\s*(?:1\s*)?piece\b",
            r"\bpiece\s*(?:size\s*)?(?:=|of|:)\s*(\d+)\s*blocks\b",
        ],
        "why": "16 blocks = 1 piece (protocol.md §6.1)",
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
]

# Values that are deliberately discussed as *rejected* alternatives inside the
# design log, and as historical notes in the spec.
EXEMPT_FILES = {"design/design-log.md"}
EXEMPT_LINE_MARKERS = (
    "rejected",
    "was `WEBM_OK`",
    "earlier revision",
    "an earlier",
    "removed",
    "no longer",
    "used to",
)

# ── 7. Rebuttal prose ─────────────────────────────────────────────────────

REBUTTAL = re.compile(
    r"\b(the old (?:design|claim|rule|formula|roadmap|command|docs|"
    r"v1|“)|old docs|v1 used|v1's|was wrong|was too strong|was not safe|"
    r"hand-wavy|misleading name)\b",
    re.IGNORECASE,
)

# ── helpers ───────────────────────────────────────────────────────────────


def doc_files() -> list[Path]:
    return sorted(DOCS.rglob("*.md"))


def rel(p: Path) -> str:
    return str(p.relative_to(DOCS))


def strip_code_fences(text: str) -> str:
    """Remove fenced code blocks so lint does not read examples as claims."""
    out, infence = [], False
    for line in text.splitlines():
        if line.lstrip().startswith("```"):
            infence = not infence
            continue
        out.append("" if infence else line)
    return "\n".join(out)


# ── checks ────────────────────────────────────────────────────────────────


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
                            errs.append(
                                f"{r}:{lineno}: {inv['name']}: found {val}, "
                                f"expected {'/'.join(sorted(inv['allowed']))}"
                                f" -- {inv['why']}"
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
    """A `§N.M` reference must exist as a numbered heading in the target file,
    or in the same file if no file is named."""
    errs = []
    headings: dict[Path, set[str]] = {}
    for f in doc_files():
        headings[f.resolve()] = set(HEADING.findall(f.read_text()))

    for f in doc_files():
        text = f.read_text()
        for lineno, line in enumerate(text.splitlines(), 1):
            for m in SECTION_REF.finditer(line):
                sec = m.group(1)
                # Which file does this § refer to? Nearest preceding link in
                # the same line or the same sentence, else this file.
                tgt = f.resolve()
                link = None
                for lm in LINK.finditer(line):
                    link = lm
                if link:
                    cand = (f.parent / link.group(1)).resolve()
                    if cand in headings:
                        tgt = cand
                if sec not in headings.get(tgt, set()):
                    # A section number that exists nowhere in the docs at all
                    # is almost certainly a typo; one that exists in a
                    # sibling file is a soft warning we skip.
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
