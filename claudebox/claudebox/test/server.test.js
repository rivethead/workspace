const assert = require("node:assert/strict");
const test = require("node:test");

const { buildClaudeArgs } = require("../server");

test("separates allowedTools values from the positional prompt", () => {
  const args = buildClaudeArgs("explain DNS", {
    allowedTools: ["Read", "Bash(git status:*)"],
  });

  assert.deepEqual(args.slice(-5), [
    "--allowedTools",
    "Read",
    "Bash(git status:*)",
    "--",
    "explain DNS",
  ]);
});

test("treats a leading-dash prompt as positional input", () => {
  const args = buildClaudeArgs("--help");

  assert.deepEqual(args.slice(-2), ["--", "--help"]);
});
