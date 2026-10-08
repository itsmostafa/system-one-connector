# cmd/evaluate

Tests all sit in `evaluate_test.go`, table-driven over `httptest`.

| File | Holds |
|---|---|
| `main.go` | cobra command tree, `route` (endpoint from profile or env), the `instructions` const |
| `profile.go` | named profiles in `profiles.json` under `os.UserConfigDir()`, the `profile` subcommands, and `systemOneURL`, the base-URL check shared with `route` |
| `client.go` | POSTs the request; retries 429/529 with backoff up to 3 times; rejects a body over the 16 MiB cap rather than truncating it, and a non-JSON 2xx body; sends `Client.Headers` under the fixed `Authorization` and `Content-Type` |
| `tools.go` | the `evaluate` tool: input schema in `jsonschema` struct tags, `validate`, the `items` fan-out, and `shape`, which post-processes every reply |
| `setup.go` | registration for Claude Code/Desktop, Codex, Hermes and pi; renders `pi.ts` |
| `update.go` | self-update from a checksum-verified GitHub release |
| `pi.ts` | pi extension template, embedded and rendered by `setup.go` |

## Constraints

- `instructions` in `main.go` is served as the MCP server instructions *and* baked into the pi extension, so editing it there covers both. Only the MCP server appends `updateNotice` (a newer-release line) at startup; pi never reads server instructions, so `pi.ts` passes `--no-update-check` to skip the GitHub request on every call. The tool description and the `jsonschema` field descriptions in `tools.go` do **not** flow: `pi.ts` hand-copies them as TypeBox descriptions. Change those strings in lockstep.
- Every line of `instructions` is one guideline: `pi.ts` splits the const on newlines, so a bullet wrapped across two physical lines ships as two broken guidelines.
- Stdout of `evaluate mcp` carries the MCP protocol; diagnostics go to stderr.
- `route` takes the profile named by `TYPESAFE_PROFILE`, else the file's `active`, ahead of any env key: setup bakes keys into client configs, so env-first would make `profile use` a no-op for existing installs. A profile replaces `TYPESAFE_API_KEY`/`BASE_URL`/`MODEL` wholly; `TYPESAFE_MAX_ITEMS` and `TYPESAFE_HEADERS` still apply. A missing file means no profiles, not an error. Tests call `isolateProfiles` so the developer's real file never leaks in.
- Without a profile, `route` prefers `TYPESAFE_API_KEY` over `OPENROUTER_API_KEY`, so a stray OpenRouter key cannot re-bill an existing setup. An explicit `model` passes through unmapped, whichever route is live.
- `TYPESAFE_BASE_URL` overrides the TypeSafe host (default `https://api.typesafe.ai`); `/v1/systemone` is appended, so the base is host-level and trailing slashes are tolerated — except on `openrouter.ai`, where `systemOneURL` keeps the URL as given (bare host → `openRouterURL`) so profiles can target OpenRouter's Decisions router per model. Only the host distinguishes it: Liquid's base also ends in `/decisions` and does need the suffix. It must be an absolute `http(s)` URL — `route` rejects anything else, so `evaluate setup` fails instead of baking a dead endpoint into every client config. It does not touch the OpenRouter route. `TYPESAFE_MODEL` likewise replaces the default `jev-latest` (TypeSafe route only), for hosts such as `clm-serve` that reject unknown model names. `TYPESAFE_MAX_ITEMS` (1 to `maxItems`, validated in `route`, both routes) sets `Client.MaxItems`, lowering the per-call item cap; it is a per-call ceiling, not a spend budget. `TYPESAFE_HEADERS` (a JSON object of strings, names and values validated in `route`, both routes) fills `Client.Headers`. `setupEnv` already carries every `TYPESAFE_*` variable into the client configs, so a new one needs no setup change.
- `add` in `tools.go` re-decodes the raw arguments with `UseNumber`, so numbers in the `any` fields (`state`, `instructions`, `criteria`) forward as written instead of rounding past 2^53. That means they arrive as `json.Number`, not `float64` — `jsonKind` matches both.
- Criteria go upstream as the caller's raw bytes (`apiQuestion`, built by `upstream`), not re-marshaled from `question.Criteria`: a Go map would sort a choice's options alphabetically. `question` has no custom (un)marshaler, so the inferred input schema stays plain.
- `shape` reorders `probabilities` (and `legend`) into criteria order, or level order for score; the API emits choice probabilities in no fixed order. Anything it does not recognise passes through unchanged, never as an error. It re-encodes without HTML escaping, and other object keys come out alphabetical.
- `min_confidence` never goes upstream (`apiQuestion` carries it unexported); `shape` applies it via `lowConfidence`.
- `validate` rejects the criteria shapes the API refuses, plus two it mishandles: unknown question types (the API answers a bare "Invalid request."; its OpenAPI spec lists only noul, choice and score) and noul criteria keys other than `true`/`false` (silently dropped).
- `TestPiDescriptionsMatch` fails when the `pi.ts` copies drift from `toolDescription` or the `jsonschema` tags.
- `pi.ts` must stay valid TypeScript once `__EVALUATE_BINARY__` and `__EVALUATE_INSTRUCTIONS__` are replaced with JSON-marshaled strings. `node --check` covers syntax and `pi -e cmd/evaluate/pi.ts` covers behaviour; there is no tsc and no Node dev dependency.

## Tool reference

`evaluate` takes `state` (evidence to judge, plus background as named fields), `questions` (id → `{type, instructions, criteria?, min_confidence?}`), an optional `model`, optional `items` (id → record), and `include_item_usage`.

`items` has no API counterpart: the tool sends one request per item, at most `itemConcurrency` at a time and `maxItems` per call, with state `{"item": <record>, "context": <state>}` (`context` only when `state` is set). Respan's Span models (`respan` in `tools.go`, matched on the model name's last segment) reject any other state than a string or a bare `{input, output}` conversation, so they get the record as the whole state, and `state` with `items` is refused before any request. Upstream bodies use `request`, not `evaluateIn`, so `items` never reaches the API. Per-item failures go to `errors` (always present) without cancelling siblings; only a total failure is a tool error. `meta` carries the model, summed usage, item count and wall-clock latency once; `shape` strips each item's `model`/`usage` unless `include_item_usage` is set.

| Type | `criteria` |
|---|---|
| `noul` | optional `{"true": ..., "false": ...}` descriptions |
| `choice` | required: map of option to description |
| `score` | required: ordered array of level descriptions, low to high |

Score answers are 0-indexed: N levels score `0` to `N-1`, so `3.87` over 5 levels sits between levels 3 and 4, not `3.87/5`. The response carries a `legend` mapping index to level, plus a `probabilities` entry per level. Full API docs: https://docs.typesafe.ai/api

## Manual client config

Point any MCP client at `/absolute/path/to/evaluate mcp` with `TYPESAFE_API_KEY` (or `OPENROUTER_API_KEY`) in its env; add `TYPESAFE_BASE_URL` to retarget the TypeSafe route. Restart Claude Desktop after a config change. For pi by hand, copy `pi.ts` to `~/.pi/agent/extensions/evaluate.ts` (or `$PI_CODING_AGENT_DIR/extensions/`) and replace `__EVALUATE_BINARY__` with the quoted absolute path to the binary and `__EVALUATE_INSTRUCTIONS__` with a quoted guidance string.
