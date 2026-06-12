# memlog MCP Server — Specification

This document specifies the `memlog mcp` subcommand to the same decision-free
standard as the main plan. The §12 "When in doubt" rules of the plan apply
unchanged: reject rather than guess, never rewrite history, keep stdout
machine-friendly.

## 1. Scope

`memlog mcp` runs a Model Context Protocol server over stdio. It is not a
daemon: it starts when an MCP client launches it, serves one client over
stdin/stdout, and exits when stdin closes. It exposes exactly five tools —
`memlog_add`, `memlog_search`, `memlog_show`, `memlog_supersede`,
`memlog_retract` — reusing the existing `store`, `model`, and `render`
packages unchanged. No other commands are exposed; `init`, `doctor`, and
rendering stay CLI-only.

## 2. Transport and protocol

- Transport: stdio, newline-delimited JSON-RPC 2.0 messages (one message per
  line). No Content-Length framing.
- Logging and diagnostics go to stderr only; stdout carries exclusively
  JSON-RPC messages.
- `initialize`: respond with the client's requested `protocolVersion`
  verbatim, `capabilities: {"tools": {}}`, and
  `serverInfo: {"name": "memlog", "version": <build version>}`.
- `notifications/initialized`: accepted, no response.
- `ping`: responds with an empty object result.
- `tools/list`: returns the five tool definitions below. Pagination is not
  implemented; `nextCursor` is omitted.
- `tools/call`: executes a tool. Unknown tool name → JSON-RPC error -32602.
- Any other method: JSON-RPC error -32601. Parse errors: -32700. Notifications
  other than `initialized` are ignored.

## 3. Store resolution

The server resolves the store per tool call using the same rules as the CLI:
`--store` flag on `memlog mcp`, else `MEMLOG_STORE`, else walking up from the
working directory. A missing store is a tool error (not a protocol error).

## 4. Tools

All tools return one `content` item of type `text`. On success `isError` is
absent/false; on failure `isError` is true and the text is the error message
the CLI would print to stderr — `model.Validate` and store errors map to tool
errors verbatim. Timestamps and ULIDs are generated exactly as in the CLI
write path (§6 of the plan), including the lock, re-render, and git commit.

| Tool | Required args | Optional args | Success text |
|---|---|---|---|
| `memlog_add` | `fact`, `session` | `tags` ([]string), `subject`, `agent`, `source` | the new entry ULID |
| `memlog_search` | `query` | `tag`, `subject`, `all` (bool) | JSON array of matching raw entries (`[]` if none; no hits is not an error) |
| `memlog_show` | `ref` | — | JSON array of the chain's raw entries, oldest first (same shape as `show --json`) |
| `memlog_supersede` | `ref`, `fact`, `session` | `tags`, `subject`, `agent`, `source`, `inherit` (bool) | the new entry ULID |
| `memlog_retract` | `ref`, `session` | `source` | the new entry ULID |

`ref` accepts a full ULID or an unambiguous prefix of ≥8 characters, exactly
like the CLI. `inherit` copies the target head's tags/subject when `tags` /
`subject` are not provided, mirroring `supersede --inherit`.

`tags` is a JSON array of strings (not a comma-joined string); each element
must satisfy the tag rules of plan §5.

## 5. Input schemas

Each tool declares a JSON Schema `inputSchema` of type `object` with the
properties above, `required` listing the required args, and
`additionalProperties: false`. Unknown or mistyped arguments are rejected as
tool errors.

## 6. Testing

An integration test launches the built binary with `mcp`, performs the
`initialize` handshake, lists tools (asserting all five names), calls
`memlog_add` then `memlog_search`, and asserts a validation failure (e.g.
missing `session`) surfaces as `isError: true` with the verbatim message.
