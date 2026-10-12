// claudebox HTTP / OpenAI-compatible API server.
// Adapted from https://github.com/ArmanJR/claudebox (server.js, main branch, fetched 2026-09-25),
// extended with native OpenAI-style function calling and SSE streaming.
//
// Function calling is native: the client's `tools` are registered in the Claude Code session
// as real MCP tools (via mcp-bridge.js), the session is run with stream-json output, and the
// assistant's actual tool_use blocks are captured and returned as OpenAI tool_calls. The
// bridge never answers a call, so the session is terminated as soon as the calls are captured.

const http = require("http");
const { spawn } = require("child_process");
// The entrypoint points this at `clappa` when OpenAPPA protection is on.
const CLAUDE_BIN = process.env.CLAUDE_BIN || "claude";
const fs = require("fs");
const os = require("os");
const path = require("path");
const crypto = require("crypto");

const PORT = parseInt(process.env.PORT || "3000", 10);
const MAX_CONCURRENT = parseInt(process.env.MAX_CONCURRENT || "4", 10);
const OPENAI_COMPAT = process.env.OPENAI_COMPAT === "1";
const CLAUDEBOX_API_KEY = process.env.CLAUDEBOX_API_KEY || "";
const DEFAULT_MODEL = process.env.CLAUDEBOX_DEFAULT_MODEL || "";
// Built-in Claude Code tools available during /v1 requests: "default", "" (none) or a list.
const BUILTIN_TOOLS = process.env.CLAUDEBOX_BUILTIN_TOOLS;
const BRIDGE_PATH = process.env.CLAUDEBOX_BRIDGE || path.join(__dirname, "mcp-bridge.js");
const MCP_SERVER_NAME = "client";
const MCP_PREFIX = `mcp__${MCP_SERVER_NAME}__`;
// How long to wait for more stream events after the first client tool_use before ending the session.
const TOOL_CALL_SETTLE_MS = parseInt(process.env.CLAUDEBOX_TOOL_SETTLE_MS || "750", 10);

// Models advertised on GET /v1/models. Full ids are current Anthropic model ids (Sep 2026);
// the short names are Claude Code aliases that resolve to the latest model of that family.
const MODELS = [
  "claude-fable-5-1",
  "claude-fable-5",
  "claude-opus-5-5",
  "claude-opus-5",
  "claude-opus-4-8",
  "claude-opus-4-7",
  "claude-opus-4-6",
  "claude-sonnet-5",
  "claude-sonnet-4-6",
  "claude-haiku-4-5",
  "opus",
  "sonnet",
  "haiku",
];

let activeRequests = 0;

// ---------------------------------------------------------------------------
// Logging
// ---------------------------------------------------------------------------

function log(level, msg, meta = {}) {
  const entry = { ts: new Date().toISOString(), level, msg, ...meta };
  process.stderr.write(JSON.stringify(entry) + "\n");
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

function readBody(req) {
  return new Promise((resolve, reject) => {
    let data = "";
    req.on("data", (chunk) => (data += chunk));
    req.on("end", () => resolve(data));
    req.on("error", reject);
  });
}

function sendJSON(res, status, body) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

function openaiError(res, status, message, type) {
  sendJSON(res, status, { error: { message, type } });
}

// ---------------------------------------------------------------------------
// Claude runner (plain, used by /prompt and tool-less /v1 requests)
// ---------------------------------------------------------------------------

function buildClaudeArgs(prompt, options = {}) {
  const args = [
    "-p",
    "--output-format",
    options.outputFormat || "json",
    "--dangerously-skip-permissions",
    "--no-session-persistence",
  ];
  if (options.outputFormat === "stream-json") args.push("--verbose");

  if (options.model) args.push("--model", options.model);
  if (options.maxTurns) args.push("--max-turns", String(options.maxTurns));
  if (options.maxBudgetUsd)
    args.push("--max-budget-usd", String(options.maxBudgetUsd));
  if (options.systemPrompt)
    args.push("--system-prompt", options.systemPrompt);
  if (options.appendSystemPrompt)
    args.push("--append-system-prompt", options.appendSystemPrompt);
  if (options.jsonSchema)
    args.push("--json-schema", options.jsonSchema);
  if (options.allowedTools)
    args.push("--allowedTools", ...options.allowedTools);
  if (options.tools !== undefined && options.tools !== null)
    args.push("--tools", options.tools);
  if (options.mcpConfig)
    args.push("--mcp-config", options.mcpConfig, "--strict-mcp-config");

  args.push("--", prompt);
  return args;
}

function runClaude(prompt, options = {}) {
  return new Promise((resolve, reject) => {
    const args = buildClaudeArgs(prompt, options);

    log("info", "spawning claude", { prompt: prompt.slice(0, 100) });

    const proc = spawn(CLAUDE_BIN, args, {
      stdio: ["inherit", "pipe", "pipe"],
      cwd: options.cwd || "/workspace",
    });

    let stdout = "";
    let stderr = "";
    proc.stdout.on("data", (d) => (stdout += d));
    proc.stderr.on("data", (d) => (stderr += d));

    proc.on("error", (err) => reject(err));
    proc.on("close", (code) => {
      if (code !== 0) {
        log("error", "claude exited with error", { code, stderr });
        reject(new Error(stderr || `claude exited with code ${code}`));
      } else {
        resolve(stdout);
      }
    });
  });
}

// ---------------------------------------------------------------------------
// Claude runner with native client tools (stream-json + MCP bridge)
// ---------------------------------------------------------------------------

// Runs a session in which `tools` are real MCP tools. Resolves with
//   { text, toolCalls: [{id, name, input}], structured, usage, stopReason }
// As soon as the assistant emits tool_use blocks for client tools, the session is killed.
function runClaudeWithTools(prompt, options, tools) {
  return new Promise((resolve, reject) => {
    const workDir = fs.mkdtempSync(path.join(os.tmpdir(), "claudebox-tools-"));
    const toolsFile = path.join(workDir, "tools.json");
    const mcpFile = path.join(workDir, "mcp.json");
    fs.writeFileSync(toolsFile, JSON.stringify(tools));
    fs.writeFileSync(
      mcpFile,
      JSON.stringify({
        mcpServers: {
          [MCP_SERVER_NAME]: {
            type: "stdio",
            command: "node",
            args: [BRIDGE_PATH],
            env: { CLAUDEBOX_TOOLS_FILE: toolsFile },
            // Load every schema into the first prompt; otherwise Claude Code defers MCP tools
            // behind ToolSearch and the model may call them blind (empty arguments).
            alwaysLoad: true,
          },
        },
      })
    );

    const args = buildClaudeArgs(prompt, { ...options, outputFormat: "stream-json", mcpConfig: mcpFile });
    log("info", "spawning claude (native tools)", {
      prompt: prompt.slice(0, 100),
      tools: tools.map((t) => t.name),
    });

    const proc = spawn(CLAUDE_BIN, args, {
      stdio: ["ignore", "pipe", "pipe"],
      cwd: options.cwd || "/workspace",
      // No deferred tool loading for client functions (see alwaysLoad above).
      env: { ...process.env, ENABLE_TOOL_SEARCH: "false" },
    });

    let stderr = "";
    let buffer = "";
    let settled = false;
    let settleTimer = null;
    const toolCalls = [];
    const textParts = [];
    let lastUsage = {};
    let sawInit = false;

    const cleanup = () => {
      if (settleTimer) clearTimeout(settleTimer);
      try {
        fs.rmSync(workDir, { recursive: true, force: true });
      } catch {}
    };
    const finish = (value) => {
      if (settled) return;
      settled = true;
      cleanup();
      resolve(value);
    };
    const fail = (err) => {
      if (settled) return;
      settled = true;
      cleanup();
      reject(err);
    };
    const endSession = () => {
      // We have the calls; the bridge never answers, so stop the session now.
      finish({ text: textParts.join(""), toolCalls, usage: lastUsage, stopReason: "tool_use" });
      try {
        proc.kill("SIGTERM");
      } catch {}
      setTimeout(() => {
        try {
          proc.kill("SIGKILL");
        } catch {}
      }, 2000).unref();
    };
    const armSettleTimer = () => {
      if (settleTimer) clearTimeout(settleTimer);
      settleTimer = setTimeout(endSession, TOOL_CALL_SETTLE_MS);
    };

    const onEvent = (ev) => {
      if (ev.type === "system" && ev.subtype === "init") {
        sawInit = true;
        const bridged = (ev.tools || []).filter((n) => String(n).startsWith(MCP_PREFIX));
        const errs = ev.mcp_server_errors || ev.mcpServerErrors;
        if (errs && Object.keys(errs).length > 0) log("warn", "mcp bridge reported errors", { errs });
        log("info", "session init", { bridgedTools: bridged.length, requested: tools.length });
        if (bridged.length === 0) {
          log("error", "no client tools registered in session", { toolsField: ev.tools });
        }
        return;
      }
      if (ev.type === "assistant" && ev.message) {
        const content = Array.isArray(ev.message.content) ? ev.message.content : [];
        if (ev.message.usage) lastUsage = ev.message.usage;
        let sawClientTool = false;
        for (const block of content) {
          if (block.type === "text" && block.text) {
            textParts.push(block.text);
          } else if (block.type === "tool_use" && String(block.name).startsWith(MCP_PREFIX)) {
            if (!toolCalls.some((c) => c.id === block.id)) {
              toolCalls.push({
                id: block.id,
                name: String(block.name).slice(MCP_PREFIX.length),
                input: block.input && typeof block.input === "object" ? block.input : {},
              });
            }
            sawClientTool = true;
          }
        }
        if (sawClientTool || toolCalls.length > 0) armSettleTimer();
        return;
      }
      if (toolCalls.length > 0) {
        // any other event after calls were seen (e.g. tool_result placeholders) just resets the timer
        armSettleTimer();
        return;
      }
      if (ev.type === "result") {
        finish({
          text: ev.result || textParts.join(""),
          toolCalls: [],
          structured: ev.structured_output,
          usage: ev.usage || lastUsage,
          stopReason: ev.stop_reason || "end_turn",
          isError: !!ev.is_error,
          subtype: ev.subtype,
        });
      }
    };

    proc.stdout.setEncoding("utf8");
    proc.stdout.on("data", (chunk) => {
      buffer += chunk;
      let idx;
      while ((idx = buffer.indexOf("\n")) >= 0) {
        const line = buffer.slice(0, idx).trim();
        buffer = buffer.slice(idx + 1);
        if (!line) continue;
        let ev;
        try {
          ev = JSON.parse(line);
        } catch {
          continue;
        }
        try {
          onEvent(ev);
        } catch (err) {
          log("warn", "stream event handling failed", { error: err.message });
        }
      }
    });
    proc.stderr.on("data", (d) => (stderr += d));
    proc.on("error", (err) => fail(err));
    proc.on("close", (code, signal) => {
      if (settled) return;
      if (toolCalls.length > 0) {
        endSession();
        return;
      }
      if (code !== 0) {
        log("error", "claude exited with error", { code, signal, stderr: stderr.slice(0, 2000), sawInit });
        fail(new Error(stderr || `claude exited with code ${code}`));
      } else {
        finish({ text: textParts.join(""), toolCalls: [], usage: lastUsage, stopReason: "end_turn" });
      }
    });
  });
}

// ---------------------------------------------------------------------------
// OpenAI Compatibility Layer
// ---------------------------------------------------------------------------

const TMP_DIR = process.env.CLAUDEBOX_TMP_DIR || "/workspace/.tmp";

const MIME_TO_EXT = {
  "image/jpeg": "jpg",
  "image/png": "png",
  "image/gif": "gif",
  "image/webp": "webp",
  "image/bmp": "bmp",
  "image/tiff": "tiff",
};

function checkApiKey(req) {
  if (!CLAUDEBOX_API_KEY) return true;
  const auth = req.headers.authorization || "";
  return auth === `Bearer ${CLAUDEBOX_API_KEY}`;
}

function ensureTmpDir() {
  if (!fs.existsSync(TMP_DIR)) {
    fs.mkdirSync(TMP_DIR, { recursive: true });
  }
}

function cleanupTmpFiles(files) {
  for (const f of files) {
    try {
      fs.unlinkSync(f);
    } catch (err) {
      log("warn", "failed to cleanup temp file", { file: f, error: err.message });
    }
  }
}

function processContentParts(content) {
  const textParts = [];
  const tmpFiles = [];

  try {
    for (const part of content) {
      if (part.type === "text") {
        textParts.push(part.text);
      } else if (part.type === "image_url") {
        const url = part.image_url?.url || "";
        const match = url.match(/^data:(image\/[^;]+);base64,(.+)$/s);
        if (!match) {
          throw new Error(
            "Only data: base64 image URIs are supported. External image URLs are not accepted."
          );
        }
        const mimeType = match[1];
        const base64Data = match[2];
        const ext = MIME_TO_EXT[mimeType] || mimeType.split("/")[1];
        const filename = `img-${crypto.randomUUID()}.${ext}`;
        const filepath = path.join(TMP_DIR, filename);

        ensureTmpDir();
        fs.writeFileSync(filepath, Buffer.from(base64Data, "base64"));
        tmpFiles.push(filepath);

        textParts.push(
          `[Attached image: ${filepath} - use the Read tool to view this file]`
        );
      }
    }
  } catch (err) {
    cleanupTmpFiles(tmpFiles);
    throw err;
  }

  return { text: textParts.join("\n"), tmpFiles };
}

function extractMessageText(content) {
  if (typeof content === "string") return { text: content, tmpFiles: [] };
  if (Array.isArray(content)) return processContentParts(content);
  if (content === null || content === undefined) return { text: "", tmpFiles: [] };
  return { text: String(content), tmpFiles: [] };
}

// --- Tools -----------------------------------------------------------------

// Normalise the OpenAI `tools` array (and legacy `functions`) to [{name, description, parameters}].
function normaliseTools(body) {
  const out = [];
  const source = Array.isArray(body.tools)
    ? body.tools
    : Array.isArray(body.functions)
      ? body.functions.map((f) => ({ type: "function", function: f }))
      : [];
  for (const t of source) {
    if (!t || t.type !== "function" || !t.function || !t.function.name) continue;
    out.push({
      name: String(t.function.name),
      description: t.function.description || "",
      parameters: t.function.parameters || { type: "object", properties: {} },
    });
  }
  return out;
}

// Resolve tool_choice (or legacy function_call) to {mode: "none"|"auto"|"required", name?}.
function resolveToolChoice(body) {
  const tc = body.tool_choice !== undefined ? body.tool_choice : body.function_call;
  if (tc === undefined || tc === null || tc === "auto") return { mode: "auto" };
  if (tc === "none") return { mode: "none" };
  if (tc === "required" || tc === "any") return { mode: "required" };
  if (typeof tc === "object") {
    const name = tc.function?.name || tc.name;
    if (name) return { mode: "required", name: String(name) };
  }
  return { mode: "auto" };
}

function renderToolCallsForHistory(toolCalls) {
  return toolCalls
    .map((c) => {
      const fn = c.function || {};
      return `[called ${fn.name || "?"} id=${c.id || "?"} with ${fn.arguments || "{}"}]`;
    })
    .join("\n");
}

// Short guidance appended to the system prompt when client tools are registered.
function buildToolSystemPrompt(tools, choice) {
  const names = tools.map((t) => `${MCP_PREFIX}${t.name}`).join(", ");
  const lines = [
    `# Client functions (MCP server "${MCP_SERVER_NAME}")`,
    "",
    `The tools ${names} are functions provided by the client application that sent this request.`,
    "Call them exactly like any other tool whenever the request needs them. The client executes each call and",
    "the results are delivered in a later message, so after issuing the call(s) you need, stop and wait: do not",
    "expect a result in this turn and do not use built-in tools (Bash, Read, WebSearch, ...) as a substitute.",
    "Earlier calls and their results appear in the conversation history as text.",
  ];
  if (choice.mode === "required" && choice.name) {
    lines.push("", `For this turn you MUST call ${MCP_PREFIX}${choice.name}.`);
  } else if (choice.mode === "required") {
    lines.push("", "For this turn you MUST call at least one of the client functions.");
  }
  return lines.join("\n");
}

// --- Request parsing ---------------------------------------------------------

function parseOpenAIRequest(body) {
  const messages = body.messages || [];
  const systemParts = [];
  const conversationMessages = [];
  const allTmpFiles = [];

  try {
    for (const msg of messages) {
      if (msg.role === "system" || msg.role === "developer") {
        const { text } = extractMessageText(msg.content);
        if (text) systemParts.push(text);
      } else if (msg.role === "tool" || msg.role === "function") {
        const { text } = extractMessageText(msg.content);
        const id = msg.tool_call_id || msg.name || "?";
        conversationMessages.push({ role: "tool", text: `[result for call id=${id}] ${text}` });
      } else if (msg.role === "assistant") {
        const { text } = extractMessageText(msg.content);
        const parts = [];
        if (text) parts.push(text);
        if (Array.isArray(msg.tool_calls) && msg.tool_calls.length > 0) {
          parts.push(renderToolCallsForHistory(msg.tool_calls));
        } else if (msg.function_call) {
          parts.push(renderToolCallsForHistory([{ id: msg.function_call.name, function: msg.function_call }]));
        }
        conversationMessages.push({ role: "assistant", text: parts.join("\n") });
      } else {
        const { text, tmpFiles } = extractMessageText(msg.content);
        allTmpFiles.push(...tmpFiles);
        conversationMessages.push({ role: "user", text });
      }
    }
  } catch (err) {
    cleanupTmpFiles(allTmpFiles);
    throw err;
  }

  const roleLabel = { user: "User", assistant: "Assistant", tool: "Tool result" };

  let prompt;
  if (conversationMessages.length === 0) {
    prompt = "";
  } else if (conversationMessages.length === 1 && conversationMessages[0].role === "user") {
    prompt = conversationMessages[0].text;
  } else {
    const history = conversationMessages.slice(0, -1);
    const last = conversationMessages[conversationMessages.length - 1];
    const historyLines = history.map((m) => `${roleLabel[m.role]}: ${m.text}`);
    const lastLabel =
      last.role === "user"
        ? "[current request]"
        : last.role === "tool"
          ? "[latest tool results - continue the task from here]"
          : "[your previous message - continue from here]";
    prompt = `[conversation history]\n${historyLines.join("\n")}\n${lastLabel}\n${last.text}`;
  }

  const tools = normaliseTools(body);
  const toolChoice = resolveToolChoice(body);
  const useTools = tools.length > 0 && toolChoice.mode !== "none";

  return {
    prompt,
    systemPrompt: systemParts.length > 0 ? systemParts.join("\n\n") : null,
    model: body.model || null,
    responseFormat: body.response_format || null,
    tools: useTools ? tools : [],
    toolChoice,
    stream: body.stream === true,
    includeUsage: !!(body.stream_options && body.stream_options.include_usage),
    tmpFiles: allTmpFiles,
  };
}

function getJsonSchema(responseFormat) {
  if (!responseFormat) return null;
  if (responseFormat.type === "json_schema") {
    return responseFormat.json_schema?.schema || responseFormat.schema || null;
  }
  if (responseFormat.type === "json_object") return { type: "object" };
  return null;
}

function parseClaudeOutput(raw) {
  try {
    const parsed = JSON.parse(raw);
    return {
      text: parsed.result || "",
      structured: parsed.structured_output,
      usage: parsed.usage || {},
    };
  } catch {
    return { text: raw, structured: undefined, usage: {} };
  }
}

// Build the OpenAI assistant message (+ finish_reason) from a session outcome.
function buildAssistantMessage(out) {
  const message = { role: "assistant", content: out.text || "" };
  let finishReason = "stop";

  if (Array.isArray(out.toolCalls) && out.toolCalls.length > 0) {
    message.content = out.text ? out.text : null;
    message.tool_calls = out.toolCalls.map((c) => ({
      id: c.id || `call_${crypto.randomUUID().replace(/-/g, "").slice(0, 24)}`,
      type: "function",
      function: { name: c.name, arguments: JSON.stringify(c.input || {}) },
    }));
    finishReason = "tool_calls";
  } else if (out.structured !== undefined) {
    message.content = JSON.stringify(out.structured);
  }

  return { message, finishReason, usage: out.usage || {} };
}

function toOpenAIUsage(usage) {
  const prompt =
    (usage.input_tokens || 0) +
    (usage.cache_creation_input_tokens || 0) +
    (usage.cache_read_input_tokens || 0);
  const completion = usage.output_tokens || 0;
  return {
    prompt_tokens: prompt,
    completion_tokens: completion,
    total_tokens: prompt + completion,
  };
}

function toOpenAIResponse(id, created, model, message, finishReason, usage) {
  return {
    id,
    object: "chat.completion",
    created,
    model,
    choices: [{ index: 0, message, finish_reason: finishReason, logprobs: null }],
    usage: toOpenAIUsage(usage),
  };
}

// Emit the finished completion as a minimal SSE stream (one content chunk, one finish chunk).
function writeSSEResponse(res, id, created, model, message, finishReason, usage, includeUsage) {
  res.writeHead(200, {
    "Content-Type": "text/event-stream",
    "Cache-Control": "no-cache",
    Connection: "keep-alive",
  });
  const send = (obj) => res.write(`data: ${JSON.stringify(obj)}\n\n`);
  const base = { id, object: "chat.completion.chunk", created, model };

  const delta = { role: "assistant", content: message.content ?? "" };
  if (message.tool_calls) {
    delta.tool_calls = message.tool_calls.map((c, i) => ({ index: i, ...c }));
  }
  send({ ...base, choices: [{ index: 0, delta, finish_reason: null }] });
  send({ ...base, choices: [{ index: 0, delta: {}, finish_reason: finishReason }] });
  if (includeUsage) send({ ...base, choices: [], usage: toOpenAIUsage(usage) });
  res.write("data: [DONE]\n\n");
  res.end();
}

async function handleChatCompletions(req, res) {
  if (!checkApiKey(req)) {
    return openaiError(res, 401, "Invalid API key", "authentication_error");
  }

  if (activeRequests >= MAX_CONCURRENT) {
    log("warn", "rejected openai request, at capacity", { activeRequests });
    return openaiError(res, 429, "Too many concurrent requests", "rate_limit_error");
  }

  activeRequests++;
  let parsed;
  try {
    const rawBody = await readBody(req);
    let body;
    try {
      body = JSON.parse(rawBody);
    } catch {
      return openaiError(res, 400, "Invalid JSON body", "invalid_request_error");
    }

    if (!body.messages || !Array.isArray(body.messages) || body.messages.length === 0) {
      return openaiError(res, 400, "Missing or empty 'messages' array", "invalid_request_error");
    }

    try {
      parsed = parseOpenAIRequest(body);
    } catch (err) {
      return openaiError(res, 400, err.message, "invalid_request_error");
    }

    if (!parsed.prompt) {
      return openaiError(res, 400, "No user message content found", "invalid_request_error");
    }

    const options = {};
    const model = parsed.model || DEFAULT_MODEL || null;
    if (model) options.model = model;
    if (BUILTIN_TOOLS !== undefined) options.tools = BUILTIN_TOOLS;

    const contentSchema = getJsonSchema(parsed.responseFormat);
    if (contentSchema) options.jsonSchema = JSON.stringify(contentSchema);

    const hasTools = parsed.tools.length > 0;
    const systemParts = [];
    if (parsed.systemPrompt) systemParts.push(parsed.systemPrompt);
    if (hasTools) systemParts.push(buildToolSystemPrompt(parsed.tools, parsed.toolChoice));
    if (systemParts.length > 0) options.appendSystemPrompt = systemParts.join("\n\n");

    log("info", "openai compat: spawning claude", {
      model,
      tools: parsed.tools.map((t) => t.name),
      toolChoice: parsed.toolChoice.mode,
      hasSchema: !!contentSchema,
      stream: parsed.stream,
    });

    let out;
    if (hasTools) {
      out = await runClaudeWithTools(parsed.prompt, options, parsed.tools);
      if (out.isError) throw new Error(out.text || `claude session failed (${out.subtype || "error"})`);
    } else {
      out = parseClaudeOutput(await runClaude(parsed.prompt, options));
    }
    const { message, finishReason, usage } = buildAssistantMessage(out);

    const id = `chatcmpl-${crypto.randomUUID()}`;
    const created = Math.floor(Date.now() / 1000);
    const respModel = model || "claude";

    log("info", "openai compat: done", {
      finishReason,
      toolCalls: (message.tool_calls || []).map((c) => c.function.name),
    });

    if (parsed.stream) {
      writeSSEResponse(res, id, created, respModel, message, finishReason, usage, parsed.includeUsage);
    } else {
      sendJSON(res, 200, toOpenAIResponse(id, created, respModel, message, finishReason, usage));
    }
  } catch (err) {
    log("error", "openai compat error", { error: err.message });
    if (!res.headersSent) openaiError(res, 500, err.message, "server_error");
    else res.end();
  } finally {
    if (parsed?.tmpFiles?.length > 0) {
      cleanupTmpFiles(parsed.tmpFiles);
    }
    activeRequests--;
  }
}

function handleModels(req, res) {
  if (!checkApiKey(req)) {
    return openaiError(res, 401, "Invalid API key", "authentication_error");
  }

  const models = MODELS.map((id) => ({
    id,
    object: "model",
    created: 1711100000,
    owned_by: "anthropic",
  }));

  sendJSON(res, 200, { object: "list", data: models });
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

const server = http.createServer(async (req, res) => {
  // --- Health check ---
  if (req.method === "GET" && req.url === "/health") {
    sendJSON(res, 200, { status: "ok", activeRequests, openaiCompat: OPENAI_COMPAT });
    return;
  }

  // --- Custom prompt API ---
  if (req.method === "POST" && req.url === "/prompt") {
    if (activeRequests >= MAX_CONCURRENT) {
      log("warn", "rejected request, at capacity", { activeRequests });
      sendJSON(res, 429, { error: "Too many concurrent requests" });
      return;
    }

    activeRequests++;
    try {
      const raw = await readBody(req);
      let body;
      try {
        body = JSON.parse(raw);
      } catch {
        sendJSON(res, 400, { error: "Invalid JSON body" });
        return;
      }

      if (!body.prompt) {
        sendJSON(res, 400, { error: "Missing 'prompt' field" });
        return;
      }

      const result = await runClaude(body.prompt, body.options || {});
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(result);
    } catch (err) {
      sendJSON(res, 500, { error: err.message });
    } finally {
      activeRequests--;
    }
    return;
  }

  // --- OpenAI-compatible endpoints ---
  if (OPENAI_COMPAT) {
    if (req.method === "POST" && req.url === "/v1/chat/completions") {
      await handleChatCompletions(req, res);
      return;
    }

    if (req.method === "GET" && req.url === "/v1/models") {
      handleModels(req, res);
      return;
    }
  }

  // --- 404 ---
  const routes = ["POST /prompt", "GET /health"];
  if (OPENAI_COMPAT) {
    routes.push("POST /v1/chat/completions", "GET /v1/models");
  }
  sendJSON(res, 404, { error: `Not found. Available: ${routes.join(", ")}` });
});

if (require.main === module) {
  server.listen(PORT, "0.0.0.0", () => {
    log("info", `claudebox server listening on port ${PORT}`, {
      maxConcurrent: MAX_CONCURRENT,
      openaiCompat: OPENAI_COMPAT,
      bridge: BRIDGE_PATH,
    });
  });
}

module.exports = {
  buildClaudeArgs,
  parseOpenAIRequest,
  buildAssistantMessage,
  runClaudeWithTools,
  MODELS,
};
