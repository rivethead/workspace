#!/usr/bin/env node
// claudebox MCP bridge: exposes the OpenAI client's function definitions to a Claude Code
// session as real MCP tools (named mcp__client__<function>). It is a stdio MCP server with
// no dependencies. Tool definitions are read from the JSON file named by CLAUDEBOX_TOOLS_FILE.
//
// tools/call never answers: the client of the HTTP API is the one that executes the function.
// server.js captures the tool_use blocks from the session stream, then terminates the session.

const fs = require("fs");

const toolsFile = process.env.CLAUDEBOX_TOOLS_FILE || "";
let tools = [];
try {
  tools = JSON.parse(fs.readFileSync(toolsFile, "utf8"));
} catch (err) {
  process.stderr.write(`[mcp-bridge] cannot read tools file ${toolsFile}: ${err.message}\n`);
}

function send(msg) {
  process.stdout.write(JSON.stringify(msg) + "\n");
}

function reply(id, result) {
  send({ jsonrpc: "2.0", id, result });
}

function replyError(id, code, message) {
  send({ jsonrpc: "2.0", id, error: { code, message } });
}

function handle(msg) {
  const { id, method, params } = msg;
  const isNotification = id === undefined || id === null;

  switch (method) {
    case "initialize":
      reply(id, {
        protocolVersion: (params && params.protocolVersion) || "2025-06-18",
        capabilities: { tools: {} },
        serverInfo: { name: "claudebox-client-bridge", version: "1.0.0" },
      });
      return;
    case "notifications/initialized":
    case "notifications/cancelled":
      return;
    case "ping":
      reply(id, {});
      return;
    case "tools/list":
      reply(id, {
        tools: tools.map((t) => ({
          name: t.name,
          description: t.description || "",
          inputSchema: t.parameters || { type: "object", properties: {} },
        })),
      });
      return;
    case "tools/call":
      // Intentionally never answered. The HTTP client executes the function; server.js
      // records the tool_use and ends the session.
      process.stderr.write(`[mcp-bridge] handed off call: ${params && params.name}\n`);
      return;
    default:
      if (!isNotification) replyError(id, -32601, `Method not found: ${method}`);
  }
}

let buffer = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  buffer += chunk;
  let idx;
  while ((idx = buffer.indexOf("\n")) >= 0) {
    const line = buffer.slice(0, idx).trim();
    buffer = buffer.slice(idx + 1);
    if (!line) continue;
    let msg;
    try {
      msg = JSON.parse(line);
    } catch {
      continue;
    }
    try {
      handle(msg);
    } catch (err) {
      if (msg.id !== undefined) replyError(msg.id, -32603, err.message);
    }
  }
});
process.stdin.on("end", () => process.exit(0));
process.stdin.on("close", () => process.exit(0));
