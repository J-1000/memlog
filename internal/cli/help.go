package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

const rootHelpText = `memlog - append-only, git-backed memory for agents and humans

Memlog records explicit facts as immutable JSONL entries, renders current live
memory to MEMORY.md, and commits every change through git. Corrections append
new entries; history is not rewritten.

Agent workflow:
  1. Pick the store with --store PATH or MEMLOG_STORE.
  2. Set provenance once when possible:
       export MEMLOG_SESSION=<agent-session-id>
       export MEMLOG_AGENT=<agent-name>
  3. Read before writing with context, search, list, show, tags, and subjects.
  4. Write only explicit facts with add, supersede, or retract.
  5. Use --json when another program will parse the output.

Command reference:
  memlog init [PATH]
      Create a memory store. Uses PATH, or --store, or .memlog. Prints store id.

  memlog add FACT --session S [--agent A] [--source SRC] [--tags T1,T2] [--subject X]
      Record a new live fact. Prints the new entry ULID.

  memlog add --stdin --session S [--agent A] [--source SRC] [--tags T1,T2] [--subject X]
      Read one fact per non-empty stdin line and commit all facts together.

  memlog supersede REF FACT --session S [--inherit] [--agent A] [--source SRC] [--tags T1,T2] [--subject X]
      Replace a live fact with a corrected version. --inherit keeps REF tags and
      subject unless --tags or --subject is provided.

  memlog retract REF --session S [--source SRC]
      Mark a live fact as no longer true.

  memlog show REF
      Show the current logical fact and its version chain.

  memlog search QUERY [--tag T] [--subject X] [--all]
      Search facts by substring. Defaults to live facts; --all includes older
      superseded and retracted entries.

  memlog list [--tag T] [--subject X]
      List live facts without a search query.

  memlog context [--tag T] [--subject X] [--max-chars N]
      Print a compact Markdown digest of live facts for agent context. With
      --max-chars, whole facts are dropped from the end to fit the budget.

  memlog history
      Print the full append-only journal.

  memlog render
      Regenerate MEMORY.md and commit if it changed.

  memlog sessions
      List sessions with entry counts.

  memlog tags
      List distinct tags with live-fact counts. Use this before inventing tags.

  memlog subjects
      List distinct subjects with live-fact counts. Use this before inventing subjects.

  memlog doctor [--fix]
      Check store integrity. --fix re-renders MEMORY.md, restores support-file
      lines, and commits recovered state.

  memlog stale --before DURATION
      List live facts untouched for DURATION, oldest first. Examples: 90d, 12h, 30m.

  memlog mcp
      Serve memlog tools over the Model Context Protocol on stdio.

  memlog completion SHELL
      Generate shell completion. SHELL is bash, zsh, fish, or powershell.

  memlog help [COMMAND]
  memlog [COMMAND] --help
      Show this guide or command-specific help.

Global flags:
  --store PATH   Store path. If omitted, MEMLOG_STORE is used, then the nearest
                 .memlog directory found by walking upward from the current directory.
  --json         Machine-readable output where supported.
  -h, --help     Show help.
  -v, --version  Show version.

Environment:
  MEMLOG_STORE    Default store path.
  MEMLOG_SESSION  Default --session for add, supersede, and retract.
  MEMLOG_AGENT    Default --agent for add and supersede.

Reference syntax:
  REF can be a full ULID or an unambiguous prefix of at least 8 characters.

JSON output:
  Supported by init, show, search, list, history, sessions, tags, subjects,
  doctor, and stale. Empty result sets still exit 1.

Exit codes:
  0  Success.
  1  No matching results, no data for a listing, or referenced entry not found.
  2  Usage error: bad arguments, missing required flag, or unknown command.
  3  Store is locked by another process.
  4  Git failure or unexpected error.
`

func configureHelp(root *cobra.Command) {
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == root {
			fmt.Fprint(cmd.OutOrStdout(), rootHelpText)
			return
		}
		defaultHelp(cmd, args)
	})
}
