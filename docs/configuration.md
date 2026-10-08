# Configuration

How to install `evaluate`, choose an API route, and connect it to your agents.

## Install

The install script supports macOS and Linux on amd64 and arm64. It checks the release archive against its published SHA-256 checksum before installing:

```sh
curl -fsSL https://raw.githubusercontent.com/itsmostafa/system-one-connector/main/install.sh | sh
```

It installs to `~/.local/bin`, or to `EVALUATE_INSTALL_DIR` if that is set. If the directory is not on your `PATH`, add it:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

With Go installed, you can build from source instead:

```sh
go install github.com/itsmostafa/system-one-connector/cmd/evaluate@latest
```

To upgrade, run `evaluate update`. It replaces the binary in place with the latest GitHub release, after verifying its checksum. When a newer release is out, the server tells your agent at startup, so it can remind you. Restart Claude Desktop, or start a new Codex session, to load the new binary.

## Commands

| Command | What it does |
|---|---|
| `evaluate setup mcp` | Registers the server with Claude Code, Claude Desktop, Codex and Hermes |
| `evaluate setup pi` | Installs the `evaluate` extension for pi |
| `evaluate profile add\|use\|list\|remove` | Manages [model profiles](#profiles) |
| `evaluate mcp` | Runs the MCP server over stdio (clients start this for you) |
| `evaluate update` | Updates to the latest release |
| `evaluate version` | Prints the version |

## API keys and routes

`evaluate` can reach Jev in two ways:

| Variable | Route | Default model |
|---|---|---|
| `TYPESAFE_API_KEY` | TypeSafe API, `POST {TYPESAFE_BASE_URL}/v1/systemone` | `jev-latest`, or `TYPESAFE_MODEL` if set |
| `OPENROUTER_API_KEY` | OpenRouter Decisions endpoint, billed to your OpenRouter account | `~typesafe/jev-latest` |

Get a TypeSafe key at https://console.typesafe.ai/. The model page on OpenRouter is https://openrouter.ai/~typesafe/jev-latest.

A [profile](#profiles), when one is selected, takes precedence over both. If both keys are set, `TYPESAFE_API_KEY` wins. A stray OpenRouter key therefore cannot move an existing setup onto a different bill. If you pass `model` explicitly in a tool call, `evaluate` sends it unchanged on either route.

OpenRouter's Decisions endpoint is still on its `/api/alpha/` path and may move.

### Custom TypeSafe host

To send TypeSafe requests through a proxy or a self-hosted gateway, set `TYPESAFE_BASE_URL`:

```sh
TYPESAFE_API_KEY=your-key TYPESAFE_BASE_URL=https://jev.internal evaluate setup mcp
```

- The default is `https://api.typesafe.ai`.
- Set only the host: `evaluate` appends `/v1/systemone`. A trailing slash is fine.
- The value must be an absolute `http` or `https` URL. `evaluate setup` rejects anything else rather than writing a broken endpoint into your client configs.
- It has no effect on the OpenRouter route. A base on `openrouter.ai` is the exception to the appended path: it is used as given, so a profile can point at OpenRouter's Decisions endpoint.
- It also works with a local server that implements `POST /v1/systemone`, such as one serving Laya. `TYPESAFE_API_KEY` must still be set, since it is what selects this route. Use whatever key your server expects; if it doesn't check keys, any non-empty value such as `local` works.

### Running CLM locally

[CLM-v0.1-8B](https://huggingface.co/Contrastive-LM/CLM-v0.1-8B) is an open System One model whose `clm-serve` implements `POST /v1/systemone`, so it works through the custom host route. Start its servers as described in the [CLM README](https://github.com/Contrastive-LM/CLM#quickstart) (it needs a GPU), then register `evaluate` with the CLM model as the default:

```sh
TYPESAFE_API_KEY=local TYPESAFE_BASE_URL=http://127.0.0.1:8700 TYPESAFE_MODEL=clm-latest evaluate setup mcp
```

- `TYPESAFE_MODEL` replaces the default `jev-latest` on the TypeSafe route. CLM rejects unknown model names, so without it every call must pass `model: "clm-latest"`. It has no effect on the OpenRouter route, and a `model` passed in a tool call still wins.
- Use the real key instead of `local` if you started `clm-serve` with `CLM_API_KEY`.

### d1 (Liquid AI)

[Liquid AI](https://liquid.ai) hosts d1, a System One model, at `https://api.liquid.ai/decisions/v1/systemone`. Since that already ends in `/v1/systemone`, it works through the same custom host route as a self-run server — just point `TYPESAFE_BASE_URL` at Liquid AI's host instead of `127.0.0.1`:

```sh
TYPESAFE_API_KEY=your-liquid-key TYPESAFE_BASE_URL=https://api.liquid.ai/decisions TYPESAFE_MODEL=d1:free evaluate setup mcp
```

- `TYPESAFE_API_KEY` just selects this route; use your Liquid AI API key as its value, whatever Liquid AI itself calls that key.
- `TYPESAFE_MODEL` replaces the default `jev-latest`; without it, every call must pass `model: "d1:free"`.

### Profiles

A profile saves a model, a host and a key under a name, so you can switch between System One models without editing client configs:

```sh
evaluate profile add typesafe-ai --model jev-latest --api-key your-key
evaluate profile add liquid --model d1:free --base-url https://api.liquid.ai/decisions --api-key your-liquid-key
evaluate profile add clm --model clm-latest --base-url http://127.0.0.1:8700 --api-key local
evaluate profile add kev-4b --model kev-4b --base-url https://openrouter.ai/api/alpha/decisions --api-key your-openrouter-key
evaluate profile add span-01 --model span-01 --base-url https://openrouter.ai/api/alpha/decisions --api-key your-openrouter-key
evaluate profile use liquid
evaluate profile list
```

- `--model` defaults to `jev-latest` and `--base-url` to `https://api.typesafe.ai`; the base URL follows the same rules as [`TYPESAFE_BASE_URL`](#custom-typesafe-host). Without `--api-key`, `add` takes the key from `TYPESAFE_API_KEY`.
- The first profile you add becomes active. Running `add` with an existing name replaces that profile.
- Profiles live in `profiles.json` in the `evaluate` folder of your config directory (`~/Library/Application Support` on macOS, `~/.config` on Linux), readable only by you.
- The server reads the active profile when it starts, so after `profile use`, start a new Claude Code or Codex session, or restart Claude Desktop. You don't need to run setup again.
- Set `TYPESAFE_PROFILE=name` to pin one client to a profile regardless of which one is active. `evaluate setup mcp` copies it into client configs like any other `TYPESAFE_*` variable.
- A selected profile replaces `TYPESAFE_API_KEY`, `TYPESAFE_BASE_URL` and `TYPESAFE_MODEL`, including values setup already wrote into your client configs. `TYPESAFE_MAX_ITEMS` and `TYPESAFE_HEADERS` still apply. With no profile active (for example, after you `remove` the active one), the environment variables apply as before.
- Profiles cover hosts that serve `POST /v1/systemone`, plus OpenRouter's Decisions endpoint. A base URL on `openrouter.ai` is used as given rather than getting `/v1/systemone` appended, and a bare `https://openrouter.ai` gets the Decisions path. OpenRouter serves several models, so add one profile per model and always pass `--model`, since the default `jev-latest` is not an OpenRouter model name.

### Span-01 (Respan)

[Respan](https://www.respan.ai)'s Span-01 scores behaviors in a conversation: for each behavior you describe, it returns the probability that the behavior is present. It is served through OpenRouter, so it needs an OpenRouter key:

```sh
evaluate profile add span-01 --model span-01 --base-url https://openrouter.ai/api/alpha/decisions --api-key your-openrouter-key
```

For the free tier, use `--model respan/span-01-lite:free`. Span-01 is stricter than Jev about what it accepts, and it rejects anything else with a 400:

- **Only `noul` questions.** Any `choice` or `score` question fails the whole request. Write each question as a behavior to look for, such as "The assistant apologizes to the user."
- **State is a string or a conversation.** Use plain text, or exactly `{"input": [messages], "output": message}`, where each message has a string `content` and a `role` of `system`, `user`, `assistant` or `tool`, and `output` has the `assistant` role. A general object such as `{"ticket": ...}` is rejected.
- **Each item is the whole state.** With `items`, each record goes upstream on its own instead of inside `{"item": ...}`, so each record must follow the state rule above. `state` cannot be combined with `items`; put any shared context into each record.

### Capping items per call

`TYPESAFE_MAX_ITEMS` lowers how many items one `evaluate` call accepts. The default and the maximum are both 500; set any whole number from 1 to 500. For example, with `TYPESAFE_MAX_ITEMS=50`, a call with 80 items fails before anything is sent, and a call with 50 items runs as usual.

Why cap it: every item is sent as its own billed request, so one 500-item call costs 500 requests. `evaluate` is marked read-only because it changes nothing, and some MCP hosts run read-only tools without asking you first.

The cap applies on both the TypeSafe and OpenRouter routes. It limits each call, not your total spend: an agent that hits the cap can split the batch across several calls. For a hard ceiling, set a spending limit on your provider account, if the provider offers one.

```sh
TYPESAFE_API_KEY=your-key TYPESAFE_MAX_ITEMS=50 evaluate setup mcp
```

### Extra request headers

`TYPESAFE_HEADERS` adds HTTP headers to every request `evaluate` sends to the model endpoint. Set it to a JSON object of header names to string values. For example, OpenRouter's [app attribution](https://openrouter.ai/docs/app-attribution) headers:

```sh
OPENROUTER_API_KEY=your-key TYPESAFE_HEADERS='{"HTTP-Referer":"https://opencode.ai/","X-OpenRouter-Title":"Decisions MCP"}' evaluate setup mcp
```

The headers apply on every route, including a selected profile. `Authorization` and `Content-Type` are always set by `evaluate`, so entries with those names are ignored. If the value isn't a JSON object of strings, or holds a header name or value HTTP doesn't allow, the server and `evaluate setup mcp` both fail with an error.

## `evaluate setup mcp`

```sh
TYPESAFE_API_KEY=your-key evaluate setup mcp
```

This registers the binary as an MCP server named `evaluate` with each client it finds:

- **Claude Code**, at user scope, when the `claude` CLI is on `PATH`.
- **Codex**, when the `codex` CLI is on `PATH`.
- **Hermes**, when the `hermes` CLI is on `PATH`.
- **Claude Desktop**, when it is installed. Setup edits `claude_desktop_config.json`. Restart Claude Desktop afterwards.

MCP clients start the server without your shell environment. Setup therefore copies every `TYPESAFE_*` variable in your shell, plus `OPENROUTER_API_KEY`, into each client's config. After you change a key or add a variable, run setup again. Running it again replaces the existing `evaluate` entry in each client, so anything you changed on that entry (a Hermes tool selection, say) is reset.

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

Put `TYPESAFE_API_KEY` or `OPENROUTER_API_KEY` in the server's `env`, or leave `env` empty and use a [profile](#profiles). Add `TYPESAFE_BASE_URL` if you use a custom host, `TYPESAFE_MODEL` if that host serves a model other than `jev-latest`, `TYPESAFE_MAX_ITEMS` to [cap items per call](#capping-items-per-call), and `TYPESAFE_HEADERS` to [send extra headers](#extra-request-headers). For example:

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
