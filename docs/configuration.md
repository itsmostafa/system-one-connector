# Configuration

How to install `evaluate`, choose an API route, and connect it to your agents.

## Install

The install script supports macOS and Linux on amd64 and arm64. It checks the release archive against its published SHA-256 checksum before installing:

```sh
curl -fsSL https://raw.githubusercontent.com/itsmostafa/typesafe-mcp/main/install.sh | sh
```

It installs to `~/.local/bin`, or to `EVALUATE_INSTALL_DIR` if that is set. If the directory is not on your `PATH`, add it:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

With Go installed, you can build from source instead:

```sh
go install github.com/itsmostafa/typesafe-mcp/cmd/evaluate@latest
```

To upgrade, run `evaluate update`. It replaces the binary in place with the latest GitHub release, after verifying its checksum. When a newer release is out, the server tells your agent at startup, so it can remind you. Restart Claude Desktop, or start a new Codex session, to load the new binary.

## Commands

| Command | What it does |
|---|---|
| `evaluate setup mcp` | Registers the server with Claude Code, Claude Desktop and Codex |
| `evaluate setup pi` | Installs the `evaluate` extension for pi |
| `evaluate mcp` | Runs the MCP server over stdio (clients start this for you) |
| `evaluate update` | Updates to the latest release |
| `evaluate version` | Prints the version |

## API keys and routes

`evaluate` can reach Jev in two ways:

| Variable | Route | Default model |
|---|---|---|
| `TYPESAFE_API_KEY` | TypeSafe API, `POST {TYPESAFE_BASE_URL}/v1/systemone` | `jev-latest` |
| `OPENROUTER_API_KEY` | OpenRouter Decisions endpoint, billed to your OpenRouter account | `~typesafe/jev-latest` |

Get a TypeSafe key at https://console.typesafe.ai/. The model page on OpenRouter is https://openrouter.ai/~typesafe/jev-latest.

If both keys are set, `TYPESAFE_API_KEY` wins. A stray OpenRouter key therefore cannot move an existing setup onto a different bill. If you pass `model` explicitly in a tool call, `evaluate` sends it unchanged on either route.

OpenRouter's Decisions endpoint is still on its `/api/alpha/` path and may move.

### Custom TypeSafe host

To send TypeSafe requests through a proxy or a self-hosted gateway, set `TYPESAFE_BASE_URL`:

```sh
TYPESAFE_API_KEY=your-key TYPESAFE_BASE_URL=https://jev.internal evaluate setup mcp
```

- The default is `https://api.typesafe.ai`.
- Set only the host: `evaluate` appends `/v1/systemone`. A trailing slash is fine.
- The value must be an absolute `http` or `https` URL. `evaluate setup` rejects anything else rather than writing a broken endpoint into your client configs.
- It has no effect on the OpenRouter route.

## `evaluate setup mcp`

```sh
TYPESAFE_API_KEY=your-key evaluate setup mcp
```

This registers the binary as an MCP server named `evaluate` with each client it finds:

- **Claude Code**, at user scope, when the `claude` CLI is on `PATH`.
- **Codex**, when the `codex` CLI is on `PATH`.
- **Claude Desktop**, when it is installed. Setup edits `claude_desktop_config.json`. Restart Claude Desktop afterwards.

MCP clients start the server without your shell environment. Setup therefore copies every `TYPESAFE_*` variable in your shell, plus `OPENROUTER_API_KEY`, into each client's config. After you change a key or add a variable, run setup again.

A client that setup does not find is skipped with a message. You can configure it by hand (see below).

## pi

[pi](https://pi.dev) has no MCP client, so `evaluate` ships a native pi extension instead:

```sh
evaluate setup pi
```

This writes `~/.pi/agent/extensions/evaluate.ts`, or `$PI_CODING_AGENT_DIR/extensions/evaluate.ts` if that variable is set. The extension registers `evaluate` as a pi tool and runs `evaluate mcp` for you. Run `/reload` in pi to load it.

Unlike the MCP clients, the extension file contains no keys. It reads them from the shell pi runs in, so export `TYPESAFE_API_KEY` (or `OPENROUTER_API_KEY`) there.

## Manual client config

To use any other MCP client, point it at:

```
/absolute/path/to/evaluate mcp
```

Put `TYPESAFE_API_KEY` or `OPENROUTER_API_KEY` in the server's `env`. Add `TYPESAFE_BASE_URL` if you use a custom host. For example:

```json
{
  "mcpServers": {
    "evaluate": {
      "command": "/Users/you/.local/bin/evaluate",
      "args": ["mcp"],
      "env": { "TYPESAFE_API_KEY": "your-key" }
    }
  }
}
```

To install the pi extension by hand:

1. Copy `cmd/evaluate/pi.ts` to `~/.pi/agent/extensions/evaluate.ts`, or to `$PI_CODING_AGENT_DIR/extensions/`.
2. Replace `__EVALUATE_BINARY__` with the absolute path to the binary, as a quoted string.
3. Replace `__EVALUATE_INSTRUCTIONS__` with a quoted guidance string.
