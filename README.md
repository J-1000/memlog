# memlog

Append-only, git-backed memory for agents and humans. Facts are written to JSONL journal files, rendered into deterministic Markdown, and committed through the local `git` binary.

## Install

```sh
go install github.com/J-1000/memlog/cmd/memlog@latest
```

## Commands

```sh
memlog init [PATH]
memlog add FACT --session S [--tags a,b] [--subject x] [--agent A] [--source TEXT]
memlog supersede REF FACT --session S [--tags a,b] [--subject x] [--agent A] [--source TEXT]
memlog retract REF --session S [--source TEXT]
memlog show REF [--json]
memlog search QUERY [--tag T] [--subject X] [--all]
memlog history
memlog render
memlog sessions [--json]
memlog doctor [--fix]
```

Global flags:

```sh
--store PATH
--json
```

The default store is `./.memlog`, discovered by walking upward from the current directory. `MEMLOG_STORE` can also point at a store.

## Agent Usage

```sh
memlog add "The staging database password rotates every 30 days." --session claude-code-2026-06-12-a --agent claude-code --source "user told me directly" --tags infra,staging --subject staging-db
memlog supersede 01J9XK7M "The staging database password rotates every 45 days." --session claude-code-2026-06-12-b --agent claude-code --source "user corrected the rotation policy" --tags infra,staging --subject staging-db
memlog retract 01J9XK7M --session claude-code-2026-06-12-c --source "policy no longer applies"
```

## Crash Recovery

Mutating commands append to the journal before committing. If `git commit` fails, the journal line remains on disk and the next successful commit will include it. Run:

```sh
memlog doctor
memlog doctor --fix
```

`doctor --fix` re-renders `MEMORY.md` and commits recovered store changes without deleting or rewriting journal history.

## JSON Output

`init --json` prints `meta.json`. `show --json` prints the entry chain for the logical fact. `sessions --json` prints an array of objects with `session`, `count`, and `newest`.

## Design Notes

Refs accept unambiguous ULID prefixes of at least 8 characters. The renderer uses only journal contents, never wall-clock time, so repeated renders are byte-identical.

## Future Work

MCP server support is intentionally out of scope for the initial CLI build.
