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
├── .gitattributes
└── .gitignore
```

`journal/*.jsonl` is the source of truth. `MEMORY.md` is generated from live facts and committed so humans can review memory without using the CLI.

## Syncing Between Machines

A store syncs with plain `git pull` and `git push`. The store's `.gitattributes` marks journal files with `merge=union`, so concurrent appends from different machines merge without conflicts: lines are append-only and ULID ids are time-ordered, so memlog re-sorts entries by id when loading and the result is independent of line order. Stores created by older memlog versions can be upgraded with `memlog doctor --fix`, which appends any missing `.gitignore` and `.gitattributes` lines.

## Commands

| Command | Purpose |
|---|---|
| `memlog init [PATH]` | Create a memory store |
| `memlog add FACT --session S` | Record a new fact |
| `memlog add --stdin --session S` | Record one fact per stdin line in a single commit |
| `memlog supersede REF FACT --session S [--inherit]` | Replace a previous live fact with a new version |
| `memlog retract REF --session S` | Mark a live fact as no longer true |
| `memlog show REF` | Show the current logical fact and its chain |
| `memlog search QUERY` | Search live facts by substring |
| `memlog list [--subject X] [--tag T]` | List live facts without a query |
| `memlog context [--subject X] [--max-chars N]` | Print a compact live-fact digest for agent context |
| `memlog history` | Print the full append-only journal |
| `memlog render` | Regenerate `MEMORY.md` and commit if changed |
| `memlog sessions` | List sessions with entry counts |
| `memlog tags` | List distinct tags with live-fact counts |
| `memlog subjects` | List distinct subjects with live-fact counts |
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

Batch-add facts (one per line, blank lines skipped, one commit):

```sh
printf 'first fact\nsecond fact\n' | memlog add --stdin \
  --session claude-code-2026-06-12-a \
  --tags notes
```

Correct a fact while keeping its tags and subject:

```sh
memlog supersede 01J9XK7M "Updated wording." --inherit \
  --session claude-code-2026-06-12-b
```

Reuse the existing taxonomy instead of inventing new tags or subjects:

```sh
memlog tags
memlog subjects
```

Inject live memory at session start:

```sh
memlog context --max-chars 4000
```

`context` prints a Markdown digest of live facts without provenance or ids. With `--max-chars`, whole facts are dropped from the end to fit the budget and a note goes to stderr.

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
memlog search QUERY --json
memlog list --json
memlog history --json
memlog tags --json
memlog subjects --json
memlog doctor --json
```

JSON shapes are stable and suitable for scripts:

- `init`: the store's `meta.json` object.
- `show`, `search`, `list`, `history`: an array of raw journal entries (`[]` when empty).
- `sessions`: an array of `{"session", "count", "newest"}`.
- `tags`, `subjects`: an array of `{"name", "count"}` sorted by name.
- `doctor`: `{"clean": bool, "fixed": bool, "problems": [string]}`.

Exit codes are the same with and without `--json`; an empty result still exits 1.

## Crash Recovery

Mutating commands append to the journal before committing. If `git commit` fails, the journal line remains on disk and the next successful command can include it.

Run:

```sh
memlog doctor
memlog doctor --fix
```

`doctor --fix` re-renders `MEMORY.md`, appends missing support-file lines, and commits recovered store changes without deleting journal lines or rewriting git history.

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
- Journal loading indexes all entries before applying refs and replays them in ULID order, so resolution does not depend on line order within or across files.
- A batch `add --stdin` of more than one fact commits as `memlog: add <N> facts` with the facts in the body.
- `context` always prints the `# Memory` heading and exits 0 even when the store has no live facts, so it is safe to inject unconditionally.

## Future Work

An MCP server exposing add/search/show is a possible future milestone. It is intentionally out of scope for the initial CLI.
