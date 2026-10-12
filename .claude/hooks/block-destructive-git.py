#!/usr/bin/env python3
"""PreToolUse(Bash) guard — deny destructive/irreversible git commands.

Reads the Claude Code hook JSON on stdin and emits a permissionDecision:"deny"
when a command runs a history- or data-loss git operation.

Why a tokenizer and not a regex: the destructive subcommand must be git's actual
subcommand at a real command boundary — not a phrase living inside a quoted
argument (e.g. a `git commit -m "...reset --hard..."` message). shlex parses the
command quote-aware, then we split on shell operators (`&&`, `||`, `|`, `;`, `&`,
subshell parens) so each segment's first word is the command that actually runs.

Allowed on purpose: --force-with-lease (the safe force), `branch -d` (deletes
only merged branches), `commit --amend`, `rebase` (local, worktree-isolated).
"""

import json
import shlex
import sys

# git global flags that consume the following token as their value, so the
# subcommand isn't mistaken for that value (e.g. `git -C path reset --hard`).
GLOBAL_VALUE_FLAGS = {
    "-C",
    "-c",
    "--git-dir",
    "--work-tree",
    "--namespace",
    "--exec-path",
    "--super-prefix",
}
OPERATORS = {"&&", "||", "|", ";", "&", "(", ")", "\n"}


def _push(rest):
    if any(
        a == "--force-with-lease" or a.startswith("--force-with-lease=") for a in rest
    ):
        return None
    if "--force" in rest or "-f" in rest:
        return "force-push rewrites remote history (use --force-with-lease)"
    return None


def _reset(rest):
    return "discards working-tree and staged changes" if "--hard" in rest else None


def _clean(rest):
    short_f = any(
        a.startswith("-") and not a.startswith("--") and "f" in a for a in rest
    )
    if short_f or "--force" in rest:
        return "permanently deletes untracked files"
    return None


def _checkout(rest):
    discards = "-f" in rest or "--force" in rest or "--" in rest or "." in rest
    return "discards uncommitted changes" if discards else None


def _restore(rest):
    staged_only = ("--staged" in rest or "-S" in rest) and not (
        "--worktree" in rest or "-W" in rest
    )
    if staged_only:
        return None
    touches_tree = (
        "--worktree" in rest or "-W" in rest or any(not a.startswith("-") for a in rest)
    )
    return "discards uncommitted changes" if touches_tree else None


def _branch(rest):
    forced = "-D" in rest or (
        "--delete" in rest and ("--force" in rest or "-f" in rest)
    )
    return "force-deletes an unmerged branch" if forced else None


def _stash(rest):
    dropping = rest[:1] and rest[0] in ("drop", "clear")
    return "permanently drops stashed work" if dropping else None


def _reflog(rest):
    return "destroys the recovery reflog" if rest[:1] == ["expire"] else None


def _update_ref(rest):
    return "deletes a ref" if "-d" in rest else None


def _gc(rest):
    return (
        "prunes unreachable objects, killing recovery"
        if any(a.startswith("--prune") for a in rest)
        else None
    )


def _rewrite(_rest):
    return "rewrites entire history"


RULES = {
    "push": _push,
    "reset": _reset,
    "clean": _clean,
    "checkout": _checkout,
    "restore": _restore,
    "branch": _branch,
    "stash": _stash,
    "reflog": _reflog,
    "update-ref": _update_ref,
    "gc": _gc,
    "filter-branch": _rewrite,
    "filter-repo": _rewrite,
}


def tokenize(command):
    """Quote-aware shell tokens; operators come out as their own tokens."""
    lex = shlex.shlex(command, posix=True, punctuation_chars=True)
    lex.whitespace_split = True
    return list(lex)


def segments(tokens):
    """Split a token stream on shell operators into per-command segments."""
    seg, out = [], []
    for tok in tokens:
        if tok in OPERATORS:
            if seg:
                out.append(seg)
                seg = []
        else:
            seg.append(tok)
    if seg:
        out.append(seg)
    return out


def subcommand(seg):
    """For a `git ...` segment, return (subcommand, remaining-args)."""
    i = 1
    while i < len(seg):
        tok = seg[i]
        if tok in GLOBAL_VALUE_FLAGS:
            i += 2
        elif tok.startswith("-"):
            i += 1
        else:
            return tok, seg[i + 1 :]
    return None, []


def reason_to_deny(command):
    try:
        tokens = tokenize(command)
    except ValueError:
        # unbalanced quotes etc. — let it fail on its own, don't false-block
        return None
    for seg in segments(tokens):
        if not seg or seg[0] != "git":
            continue
        sub, rest = subcommand(seg)
        rule = RULES.get(sub)
        if rule and (why := rule(rest)):
            return why
    return None


def main():
    try:
        command = json.load(sys.stdin).get("tool_input", {}).get("command", "")
    except (json.JSONDecodeError, AttributeError):
        return
    why = reason_to_deny(command)
    if not why:
        return
    print(
        json.dumps(
            {
                "hookSpecificOutput": {
                    "hookEventName": "PreToolUse",
                    "permissionDecision": "deny",
                    "permissionDecisionReason": (
                        f"Blocked destructive git ({why}). "
                        "If truly intended, run it yourself outside the agent."
                    ),
                }
            }
        )
    )


if __name__ == "__main__":
    main()
