#!/usr/bin/env python3
"""Check the workspace documents.

1. No hard-wrapped markdown (the same rule as .claude/hooks/no-hard-wrap.py).
2. Every relative markdown link reaches a file, and every #anchor reaches a
   heading in that file (GitHub's heading slugs).

Exit 1 with one line per problem.
"""

import importlib.util
import re
import sys

from pathlib import Path

# Importing the hook must not leave __pycache__ in .claude/.
sys.dont_write_bytecode = True

ROOT = Path(__file__).resolve().parent.parent
DOCS = [
    ROOT / "ARCHITECTURE.md",
    ROOT / "README.md",
    ROOT / "CLAUDE.md",
    *sorted((ROOT / "docs").glob("**/*.md")),
]

LINK = re.compile(r"\]\(([^)\s]+)\)")
FENCE = re.compile(r"^```.*?^```", re.M | re.S)


def load_hard_wrap_check():
    spec = importlib.util.spec_from_file_location(
        "no_hard_wrap", ROOT / ".claude/hooks/no-hard-wrap.py"
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.offending_lines


def slug(heading: str) -> str:
    text = re.sub(r"[^\w\s-]", "", heading.lower()).strip()
    return re.sub(r"\s", "-", text)


def anchors(path: Path) -> set[str]:
    text = FENCE.sub("", path.read_text(encoding="utf-8"))
    seen: dict[str, int] = {}
    out = set()
    for heading in re.findall(r"^#+ (.*)$", text, re.M):
        s = slug(heading)
        n = seen.get(s, -1) + 1
        seen[s] = n
        out.add(s if n == 0 else f"{s}-{n}")
    return out


def main() -> int:
    offending_lines = load_hard_wrap_check()
    problems = []
    for doc in DOCS:
        text = doc.read_text(encoding="utf-8")
        rel = doc.relative_to(ROOT)
        for line, fragment in offending_lines(text):
            problems.append(f"{rel}:{line}: hard-wrapped line: {fragment}")
        for target in LINK.findall(FENCE.sub("", text)):
            if re.match(r"^[a-z]+:", target):
                continue
            file_part, _, anchor = target.partition("#")
            path = (doc.parent / file_part).resolve() if file_part else doc
            if not path.exists():
                problems.append(f"{rel}: link to a missing file: {target}")
            elif anchor and path.suffix == ".md" and anchor not in anchors(path):
                problems.append(f"{rel}: link to a missing heading: {target}")
    for p in problems:
        print(p)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
