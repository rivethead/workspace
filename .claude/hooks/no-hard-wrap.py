#!/usr/bin/env python3
"""PostToolUse(Edit|Write) guard: reject hard-wrapped markdown.

Hard-wrapped prose (a paragraph broken across several ~80-column lines) is
painful to read and edit in a real editor, which soft-wraps anyway. This guard
rejects it deterministically so documents stay one-line-per-paragraph.

Detection: a CONTINUATION line — a non-blank line directly after another
non-blank line, which does not itself begin a new construct (list item,
heading, table row, blockquote, front-matter fence) — whose predecessor is at
least MIN_WRAP characters long. An unwrapped document has no such lines
outside tables and code, both of which are skipped.

Exit 2 feeds the message back to the model so it rewrites the file, matching
the repo's existing lint-guard philosophy.
"""

import json
import re
import sys

MIN_WRAP = 50  # a shorter predecessor is a deliberate break, not a wrap
MAX_REPORTED = 5

NEW_CONSTRUCT = re.compile(
    r"""^\s*(
          [-*+]\s          # bullet
        | \d+[.)]\s        # ordered item
        | \#{1,6}\s        # heading
        | >                # blockquote
        | \|               # table row
        | (-{3,}|\*{3,}|_{3,})\s*$   # rule / front-matter fence
        | :{3,}            # directive fence
    )""",
    re.VERBOSE,
)


QUOTE = re.compile(r"^\s*>+\s?")
SELF_TERMINATING = re.compile(r"^\s*(\||\#{1,6}\s|(-{3,}|\*{3,}|_{3,})\s*$)")


def offending_lines(text: str) -> list[tuple[int, str]]:
    lines = text.split("\n")
    hits: list[tuple[int, str]] = []
    in_fence = False
    fence = ""
    in_front_matter = False

    for i, line in enumerate(lines):
        stripped = line.strip()

        if i == 0 and stripped == "---":
            in_front_matter = True
            continue
        if in_front_matter:
            if stripped == "---":
                in_front_matter = False
            continue

        m = re.match(r"^\s*(`{3,}|~{3,})", line)
        if m:
            if not in_fence:
                in_fence, fence = True, m.group(1)[0]
            elif m.group(1)[0] == fence:
                in_fence = False
            continue
        if in_fence or not stripped:
            continue

        if i == 0:
            continue
        prev = lines[i - 1]
        if not prev.strip():
            continue
        # skip indented code and anything starting a new construct
        if line.startswith("    ") and not NEW_CONSTRUCT.match(prev):
            pass
        # Inside a blockquote, compare the text after the ">" markers: a quoted
        # paragraph broken across several ">" lines is still hard-wrapped.
        qp, ql = QUOTE.match(prev), QUOTE.match(line)
        if qp and ql:
            prev_body, body = prev[qp.end() :], line[ql.end() :]
            if (
                prev_body.strip()
                and body.strip()
                and not NEW_CONSTRUCT.match(body)
                and len(prev_body.rstrip()) >= MIN_WRAP
            ):
                hits.append((i + 1, line.strip()[:70]))
            continue

        # A table row, heading or rule is self-terminating: whatever follows
        # starts a new block rather than continuing that line.
        if SELF_TERMINATING.match(prev):
            continue
        if NEW_CONSTRUCT.match(line) or NEW_CONSTRUCT.match(prev):
            if not NEW_CONSTRUCT.match(line) and len(prev.rstrip()) >= MIN_WRAP:
                hits.append((i + 1, line.strip()[:70]))
            continue
        if len(prev.rstrip()) >= MIN_WRAP:
            hits.append((i + 1, line.strip()[:70]))

    return hits


def main() -> int:
    try:
        payload = json.load(sys.stdin)
    except Exception:
        return 0

    path = (payload.get("tool_input") or {}).get("file_path") or ""
    if not path.endswith((".md", ".mdx")):
        return 0
    # inputs are immutable client source; never rewrite them
    if "/docs/inputs/" in path:
        return 0

    try:
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
    except OSError:
        return 0

    hits = offending_lines(text)
    if not hits:
        return 0

    shown = hits[:MAX_REPORTED]
    more = len(hits) - len(shown)
    detail = "\n".join(f"  line {n}: {frag}…" for n, frag in shown)
    if more:
        detail += f"\n  …and {more} more"

    print(
        f"Hard-wrapped markdown in {path}.\n\n"
        f"{len(hits)} line(s) continue the previous line instead of starting a "
        f"new one. Editors soft-wrap; hard wrapping makes the file painful to "
        f"read and edit.\n\n{detail}\n\n"
        "Rewrite so each paragraph, list item and table cell is ONE line, "
        "however long. Keep blank lines between blocks. Do not reflow to any "
        "column width.",
        file=sys.stderr,
    )
    return 2


if __name__ == "__main__":
    sys.exit(main())
