# memlog Examples

Practical ways to use `memlog`, for agents and for humans. Every command below
assumes a store has been created with `memlog init` and that `git` is on `PATH`.

## 1. Persistent memory for a coding agent

An agent such as Claude Code forgets everything between sessions. `memlog`
gives it a durable, inspectable memory. Inject the digest at session start and
record what the user says during the session.

```sh
# start of session
export MEMLOG_SESSION=claude-code-2026-09-14-a
export MEMLOG_AGENT=claude-code
memlog context --max-chars 4000        # paste into the agent's prompt

# during the session
memlog add "Tests must run with -race in CI." \
  --source "user told me directly" --tags ci,testing --subject ci
memlog add "The API package is frozen until the v2 release." \
  --source "user told me directly" --tags api --subject api-v2
```

`context` always prints a `# Memory` heading and exits 0, so it is safe to
inject unconditionally even when the store is empty.

## 2. Expose the store to an MCP client

Instead of shelling out, let the agent call `memlog_add`, `memlog_search`,
`memlog_show`, `memlog_supersede`, and `memlog_retract` as native tools.

```json
{
  "mcpServers": {
    "memlog": {
      "command": "memlog",
      "args": ["--store", "/path/to/project/.memlog", "mcp"]
    }
  }
}
```

See [mcp.md](mcp.md) for the protocol details.

## 3. Correct memory without losing history

When a fact changes, supersede it. The old version stays in the journal, so
you can always see what the agent used to believe and why it changed.

```sh
memlog search "rotates every"
memlog supersede 01J9XK7M "The staging DB password rotates every 45 days." \
  --inherit --source "user corrected the policy"
memlog show 01J9XK7M          # full chain: original -> replacement
```

If something stops being true altogether, retract it:

```sh
memlog retract 01J9XK7M --source "staging environment was decommissioned"
```

## 4. Team-shared project knowledge synced via git

Commit the store into a project repo or a dedicated one. Everyone pulls and
pushes as usual. Journal files use `merge=union`, so two people adding facts
on different machines never conflict. The generated `MEMORY.md` is readable on
GitHub without the CLI.

```sh
cd .memlog && git pull && git push
```

## 5. Scoped context for different tasks

Filter by tag or subject so an agent working on infrastructure does not get
frontend facts, and cap the size to control token cost.

```sh
memlog context --tag infra --max-chars 3000
memlog context --subject staging-db
memlog list --tag onboarding
```

## 6. A personal decision log

Humans can use `memlog` directly as an append-only notebook for decisions,
with provenance and a grep-able history.

```sh
memlog add "We chose Postgres over SQLite because of concurrent writers." \
  --session jan-2026-09 --source "architecture meeting" --tags decision,db
memlog history                   # full audit trail
git -C .memlog log --oneline     # every change is a commit
```

## 7. Bulk import from existing notes

Pipe lines from a file to load many facts in one commit. Blank lines are
skipped.

```sh
grep -v '^#' notes.txt | memlog add --stdin --session import-2026-09 --tags imported
```

## 8. Memory hygiene and auditing

Periodically review facts nobody has touched in a long time, reuse the
existing taxonomy, and repair state after a crash.

```sh
memlog stale --before 90d         # candidates for supersede or retract
memlog tags && memlog subjects    # see the taxonomy before inventing new tags
memlog doctor --fix               # verify and recover store state
```

## 9. Scripting with JSON output

Every read command supports `--json`, so you can build reports or hooks on top.

```sh
memlog sessions --json | jq '.[] | select(.count > 10)'
memlog search deploy --json | jq -r '.[].fact'
```

## 10. Multi-agent setups with clear provenance

Several agents can write to the same store under different agent names and
sessions. Later you can see exactly which agent learned what, from which
source, and when.

```sh
memlog add "Release branches are cut on Mondays." \
  --session ci-bot-run-8812 --agent ci-bot --source "observed from git history"
memlog sessions                    # which sessions contributed, and how much
```
