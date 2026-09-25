# System One Connector

**Give your AI agent answers it can act on: typed judgments with real probabilities, instead of prose it has to parse.**

`evaluate` connects Claude Code, Claude Desktop, Codex, Hermes and [pi](https://pi.dev) to [TypeSafe](https://typesafe.ai)'s Jev model. Your agent asks a question like "is this urgent?" or "which team owns this?" and gets back a number or an option it can use in an `if` statement.

[![Latest release](https://img.shields.io/github/v/release/itsmostafa/system-one-connector?sort=semver)](https://github.com/itsmostafa/system-one-connector/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
![Go version](https://img.shields.io/github/go-mod/go-version/itsmostafa/system-one-connector)

```
  "Help! My payouts have been          ┌──────────┐        is_urgent   0.95
   failing for 3 days."          ───▶  │   Jev    │  ───▶  department  billing  (86%)
                                       └──────────┘                   technical (14%)
  is it urgent? which team?                                           sales      (0%)
```

## Why this exists

**The problem:** An agent that needs a quick judgment call usually asks an LLM, reads a paragraph back, and guesses what it meant. "This seems fairly urgent" gives the agent nothing to branch on, and it can't tell a confident answer from a coin flip.

**The fix:** Jev is a model built for judgments rather than text generation. You name the question and the possible answers, and Jev returns a probability for each answer in a fixed format. Your agent gets data it can compare against a threshold, and never has to parse prose.

## Quickstart

**1. Install** (macOS and Linux):

```sh
curl -fsSL https://raw.githubusercontent.com/itsmostafa/system-one-connector/main/install.sh | sh
```

**2. Connect your agents** ([get a key](https://console.typesafe.ai/)):

```sh
TYPESAFE_API_KEY=your-key evaluate setup mcp
```

This finds Claude Code, Claude Desktop, Codex and Hermes and registers `evaluate` with each one. If you already have an [OpenRouter](https://openrouter.ai/~typesafe/jev-latest) account, set `OPENROUTER_API_KEY` instead. To run an open model such as Laya locally, point `TYPESAFE_BASE_URL` at your server and keep `TYPESAFE_API_KEY` set, since it selects that route; any non-empty value works if your server doesn't check keys, e.g. `TYPESAFE_API_KEY=local TYPESAFE_BASE_URL=http://127.0.0.1:8787 evaluate setup mcp` (see [custom hosts](docs/configuration.md#custom-typesafe-host)). [CLM-v0.1-8B](https://huggingface.co/Contrastive-LM/CLM-v0.1-8B) works the same way; add `TYPESAFE_MODEL=clm-latest` (see [running CLM](docs/configuration.md#running-clm-locally)). For pi, run `evaluate setup pi`.

**3. Ask a question:**

> "Use evaluate to decide whether this ticket is urgent and which team should own it: *Help! My payouts have been failing for 3 days.*"

Your agent sends:

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

And gets back:

```json
{
  "answers": {
    "is_urgent": {"type": "noul", "noul": 0.95},
    "department": {"type": "choice", "choice": "billing", "confidence": 0.79,
      "probabilities": {"billing": 0.86, "technical": 0.14, "sales": 0.0}}
  }
}
```

## What you get

- **Answers your agent can branch on.** Three question types cover most judgment calls: yes or no (`noul`), pick one option (`choice`), and rate on a scale (`score`). Each answer comes with probabilities.
- **Confidence you can act on.** A 0.95 and a 0.55 lead to different actions. Your agent can proceed on confident answers and escalate unsure ones to you.
- **Fast enough to call often.** Jev typically answers in under half a second.
- **Many questions in one call.** Ask about urgency, ownership and sentiment together, and they run in parallel.
- **Whole datasets in one call.** Pass up to 500 records as `items` and ask the same questions of each one. If one record fails, the rest still complete.
- **Agents that use it well without extra prompting.** The server tells your agent how to write good questions (narrow judgments, structured state, evidence rather than conclusions).
- **Setup in one command.** `evaluate setup mcp` configures every supported client it finds. Run it again to update.
- **No dependencies.** One static binary with no Node or Python runtime. `evaluate update` upgrades it in place.

## Documentation

- [Configuration](docs/configuration.md): install options, API keys, OpenRouter, custom hosts, pi, and manual client setup.
- [Tool reference](docs/tool-reference.md): input fields, question types, reading scores, `items`, limits and errors.
- [Development](docs/development.md): building, testing and releasing.

## About TypeSafe

[TypeSafe](https://typesafe.ai) builds System One models: small units of AI judgment that you use like programming primitives. Instead of generating text, they turn natural language and application state into typed answers and probabilities that code can combine. Jev is the first of them.

[Website](https://typesafe.ai) · [Docs](https://docs.typesafe.ai) · [API reference](https://docs.typesafe.ai/api) · [Console](https://console.typesafe.ai/)

## Contributing

Issues and pull requests are welcome. See [docs/development.md](docs/development.md) to get started.

If `evaluate` spares your agent some prose-parsing, a ⭐ helps others find it.
