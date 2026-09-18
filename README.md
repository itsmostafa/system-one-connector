# Typesafe MCP

**Give your AI agent typed judgments instead of free text.** `jev` is an MCP server that lets Claude Code, Claude Desktop, Codex, and [pi](https://pi.dev) call [TypeSafe](https://typesafe.ai)'s Jev model and get back probabilities they can branch on. One command, `jev setup mcp`, registers it with the first three; `jev setup pi` covers pi.

[![Latest release](https://img.shields.io/github/v/release/itsmostafa/typesafe-mcp?sort=semver)](https://github.com/itsmostafa/typesafe-mcp/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
![Go version](https://img.shields.io/github/go-mod/go-version/itsmostafa/typesafe-mcp)

```
┌──────────────┐  evaluate   ┌─────────┐  POST /v1/systemone  ┌──────────────┐
│ Claude Code  │ ──────────▶ │   jev   │ ───────────────────▶ │ TypeSafe API │
│ Claude Desk. │   (stdio)   │  (MCP)  │  retries 429 / 529   │  ── or ──    │
│ Codex        │             │         │                      │  OpenRouter  │
│ pi           │ ◀────────── │         │ ◀─────────────────── │              │
└──────────────┘ typed JSON  └─────────┘   POST /decisions    └──────────────┘
```

## Why this exists

**Problem:** When an agent needs a yes/no call, a routing decision, or a severity rating, it usually asks an LLM, then parses prose and hopes the format holds. The answer has no probability attached, so the agent cannot tell a confident "yes" from a coin flip.

**Solution:** `jev` exposes one tool, `evaluate`, that sends state plus typed questions to Jev and returns structured answers with probabilities. Nothing to parse and no prompt formatting to maintain. `jev setup mcp` wires it into Claude Desktop, Claude Code, and Codex in one step, and `jev setup pi` installs the equivalent extension for pi.

## Quickstart

**1. Install** (macOS and Linux, amd64 and arm64):

```sh
curl -fsSL https://raw.githubusercontent.com/itsmostafa/typesafe-mcp/main/install.sh | sh
```

It installs to `~/.local/bin`. If that is not on your `PATH`, add it with `export PATH="$HOME/.local/bin:$PATH"`. With Go, you can instead run `go install github.com/itsmostafa/typesafe-mcp/cmd/jev@latest`. Run `jev update` to upgrade in place.

**2. Register with your agents** (get a key at https://console.typesafe.ai/)

```sh
TYPESAFE_API_KEY=your-key jev setup mcp
```

Already on [OpenRouter](https://openrouter.ai/~typesafe/jev-latest)? Use that key instead and jev routes through OpenRouter's Decisions endpoint, billed to your OpenRouter account:

```sh
OPENROUTER_API_KEY=your-key jev setup mcp
```

`TYPESAFE_API_KEY` wins if both are set. OpenRouter's Decisions endpoint is still on its `/api/alpha/` path and may move.

Using [pi](https://pi.dev)? It has no MCP client, so jev ships a pi extension instead:

```sh
jev setup pi
```

That writes `~/.pi/agent/extensions/jev.ts`, which registers `evaluate` as a native pi tool and talks to `jev mcp` for you. Run `/reload` in pi to pick it up. Unlike the MCP clients, nothing is baked into the file: the extension reads your key from the shell pi runs in.

**3. Ask your agent a judgment question**

> "Use jev to decide whether this ticket is urgent and which team should own it: *Help! My payouts have been failing for 3 days.*"

The agent calls `evaluate` with:

```json
{
  "state": "Help! My payouts have been failing for 3 days.",
  "questions": {
    "is_urgent": {"type": "noul", "instructions": "Does this convey urgency?"},
    "department": {"type": "choice", "instructions": "Which team should handle this?",
      "criteria": {"billing": "Payments, refunds", "technical": "Bugs, outages", "sales": "Pricing"}}
  }
}
```

It gets back the raw response JSON, with each answer under the same id you gave it.

## What you get

- **One-command setup across clients.** `jev setup mcp` registers with Claude Code (user scope) and Codex when their CLIs are on `PATH`, and with Claude Desktop when it is installed. Every `TYPESAFE_*` variable in your shell is carried over, plus `OPENROUTER_API_KEY`. Re-run it to update. `jev setup pi` installs the pi extension.
- **Answers your code can branch on.** Three question types: `noul` (probability a condition holds), `choice` (one option from a map), `score` (position on ordered levels).
- **Rate limits handled for you.** 429 and 529 responses are retried with exponential backoff. Other API errors come back to the agent as tool errors it can read and act on.
- **Several questions, one call.** Batch independent questions over the same state; they run in parallel.
- **Agents that use it well out of the box.** The server ships usage guidance (narrow questions, JSON state, no-match options) to the client, so the agent writes better questions without extra prompting.
- **A single static binary.** No runtime, no Node, no Python. `jev update` upgrades it in place from a checksum-verified release. Read-only tool, 60s request timeout, response size capped at 16 MiB.

## About TypeSafe

[TypeSafe](https://typesafe.ai) builds System One models: small units of AI intelligence you use like programming primitives. Instead of generating text, they turn natural language and application state into typed judgments and probabilities that code can combine. Jev is one of them.

[Website](https://typesafe.ai) · [Docs](https://docs.typesafe.ai) · [API reference](https://docs.typesafe.ai/api) · [Console](https://console.typesafe.ai/)

## Reference

### `evaluate`

| Field | Required | Description |
|---|---|---|
| `state` | yes | Content to judge: plain text, or a JSON object/array with named fields |
| `questions` | yes | Map of question id to `{type, instructions, criteria?}` |
| `model` | no | Defaults to `jev-latest`, or `~typesafe/jev-latest` on OpenRouter |

Criteria by type: `noul` takes optional `{"true": ..., "false": ...}` descriptions; `choice` requires a map of option to description; `score` requires an ordered array of at least 2 levels. Full docs: https://docs.typesafe.ai/api

### Manual client config

Skip `jev setup mcp` and point your client at `/absolute/path/to/jev mcp` with `TYPESAFE_API_KEY` (or `OPENROUTER_API_KEY`) in its env. Restart Claude Desktop after any config change.

For pi, `jev setup pi` writes into `~/.pi/agent/extensions/` (or `$PI_CODING_AGENT_DIR/extensions/`), which pi discovers with no settings change. To install by hand, copy `cmd/jev/pi.ts` there as `jev.ts` and replace `__JEV_BINARY__` with the quoted absolute path to your `jev` binary and `__JEV_INSTRUCTIONS__` with a quoted guidance string.

## Contributing

Issues and pull requests are welcome. The repo uses [Task](https://taskfile.dev):

```sh
task check     # gofmt, go vet, and tests with -race
task inspect   # open the MCP Inspector against a local build
```

If `jev` saves you some prompt-parsing, a star helps others find it.
