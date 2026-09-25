# system-one-connector

`evaluate`: a Go MCP stdio server that forwards typed questions to TypeSafe's Jev model. One static binary, no runtime dependencies. README.md is the user-facing pitch and `docs/` holds the user-facing reference (configuration, tool reference, development); code notes live in `cmd/evaluate/CLAUDE.md` — read it before editing under `cmd/`.

## Workflow

- `task check` (gofmt, `go vet`, `go test -race -count=1`) before calling work done.
- `task build` writes `./evaluate`, which is gitignored. `task inspect` drives the built server through the MCP Inspector.

## Commits

Conventional Commits drive release-please, so the subject line picks the version bump: `feat` minor, `fix` patch, `!` breaking (still minor pre-1.0), `docs`/`test`/`chore` no bump. Scopes in use: `tools`, `setup`, `client`, `cli`, `readme`. `CHANGELOG.md` and `.release-please-manifest.json` are generated — let the release workflow own them.
