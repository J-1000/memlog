# memlog

Append-only, git-backed memory for agents and humans.

`memlog` records explicit facts as immutable JSONL entries, renders the current live memory to `MEMORY.md`, and commits every change through the local `git` binary. It is designed for auditability: history is plain text, provenance is first-class, and corrections are appended instead of rewritten.

## Why memlog

Agents need memory, but memory should be inspectable. `memlog` keeps the storage model simple enough that `git log`, `git diff`, and a text editor can explain everything that happened.

- Append-only journal files, one JSON object per line
- Git commits for every store mutation
- Provenance on every fact: session, agent, source, timestamp, and refs
- Supersede and retract operations instead of edits or deletes
- Deterministic Markdown rendering for review and sharing
- No daemon, database, embeddings, vector search, or LLM calls

## Install

```sh
go install github.com/J-1000/memlog/cmd/memlog@latest
```

Requires Go 1.22+ and `git` on `PATH`.

## Quick Start

```sh
memlog init

memlog add "The staging database password rotates every 30 days." \
  --session claude-code-2026-06-12-a \
  --agent claude-code \
  --source "user told me directly" \
  --tags infra,staging \
  --subject staging-db

memlog search password
memlog show 01J9XK7M
memlog doctor
```

By default, `memlog` discovers `./.memlog` by walking upward from the current directory. Use `--store PATH` or `MEMLOG_STORE` to target a specific store.

## Store Layout

```text
.memlog/
├── journal/
│   └── 2026-06.jsonl
├── MEMORY.md
├── meta.json
└── .gitignore
```

`journal/*.jsonl` is the source of truth. `MEMORY.md` is generated from live facts and committed so humans can review memory without using the CLI.

## Commands

| Command | Purpose |
|---|---|
| `memlog init [PATH]` | Create a memory store |
| `memlog add FACT --session S` | Record a new fact |
| `memlog supersede REF FACT --session S` | Replace a previous live fact with a new version |
| `memlog retract REF --session S` | Mark a live fact as no longer true |
| `memlog show REF` | Show the current logical fact and its chain |
| `memlog search QUERY` | Search live facts by substring |
| `memlog history` | Print the full append-only journal |
| `memlog render` | Regenerate `MEMORY.md` and commit if changed |
| `memlog sessions` | List sessions with entry counts |
| `memlog doctor [--fix]` | Check integrity and recover stale generated state |

Global flags:

```sh
--store PATH
--json
```

`REF` can be a full ULID or an unambiguous prefix of at least 8 characters.

## Agent Examples

Add a fact:

```sh
memlog add "The staging database password rotates every 30 days." \
  --session claude-code-2026-06-12-a \
  --agent claude-code \
  --source "user told me directly" \
  --tags infra,staging \
  --subject staging-db
```

Supersede a fact:

```sh
memlog supersede 01J9XK7M "The staging database password rotates every 45 days." \
  --session claude-code-2026-06-12-b \
  --agent claude-code \
  --source "user corrected the rotation policy" \
  --tags infra,staging \
  --subject staging-db
```

Retract a fact:

```sh
memlog retract 01J9XK7M \
  --session claude-code-2026-06-12-c \
  --source "policy no longer applies"
```

## Output Model

Each journal line is a JSON object with:

- `id`: generated ULID
- `ts`: generated UTC timestamp
- `op`: `add`, `supersede`, or `retract`
- `fact`: single-line fact text for add/supersede
- `tags` and `subject`: normalized grouping metadata
- `session`, `agent`, `source`: provenance
- `ref`: referenced entry for supersede/retract

The renderer resolves live facts by replaying journal files in filename and line order. Subjects sort ascending, unsubjected facts render last, and the output ends with a provenance table.

## JSON Output

Machine-readable output is available where useful:

```sh
memlog init --json
memlog show REF --json
memlog sessions --json
```

JSON shapes are stable and suitable for scripts.

## Crash Recovery

Mutating commands append to the journal before committing. If `git commit` fails, the journal line remains on disk and the next successful command can include it.

Run:

```sh
memlog doctor
memlog doctor --fix
```

`doctor --fix` re-renders `MEMORY.md` and commits recovered store changes without deleting journal lines or rewriting git history.

## Development

```sh
go test ./...
go vet ./...
gofmt -w .
go build ./cmd/memlog
```

CI runs formatting, vet, and tests on Linux and macOS.

## Design Notes

- Git integration shells out to `git` via `os/exec`; the project does not use go-git.
- Rendering uses the newest journal timestamp, not wall-clock time, so repeated renders are byte-identical.
- Inputs are rejected rather than guessed when validity is unclear.
- Existing journal lines are never mutated or removed.

## Future Work

An MCP server exposing add/search/show is a possible future milestone. It is intentionally out of scope for the initial CLI.
