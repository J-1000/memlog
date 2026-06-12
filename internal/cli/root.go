package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/J-1000/memlog/internal/gitio"
	"github.com/J-1000/memlog/internal/mcp"
	"github.com/J-1000/memlog/internal/model"
	"github.com/J-1000/memlog/internal/render"
	"github.com/J-1000/memlog/internal/store"
	"github.com/spf13/cobra"
)

// Version is the build version, overridden at release time via
// -ldflags "-X github.com/J-1000/memlog/internal/cli.Version=...".
var Version = "dev"

type app struct {
	storePath string
	jsonOut   bool
	ts        string
}

func NewRoot() *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:           "memlog",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&a.storePath, "store", "", "store path")
	root.PersistentFlags().BoolVar(&a.jsonOut, "json", false, "machine-readable output")
	root.PersistentFlags().StringVar(&a.ts, "ts", "", "test timestamp")
	_ = root.PersistentFlags().MarkHidden("ts")
	root.AddCommand(
		a.initCmd(),
		a.addCmd(),
		a.supersedeCmd(),
		a.retractCmd(),
		a.historyCmd(),
		a.showCmd(),
		a.searchCmd(),
		a.listCmd(),
		a.contextCmd(),
		a.renderCmd(),
		a.sessionsCmd(),
		a.tagsCmd(),
		a.subjectsCmd(),
		a.doctorCmd(),
		a.mcpCmd(),
	)
	return root
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		fmt.Fprintln(os.Stderr, err)
		return exit.ExitCode()
	}
	fmt.Fprintln(os.Stderr, err)
	return store.Code(err)
}

func (a *app) now() (time.Time, error) {
	if a.ts == "" {
		return time.Now().UTC().Truncate(time.Second), nil
	}
	t, err := time.Parse(time.RFC3339, a.ts)
	if err != nil {
		return time.Time{}, store.ErrUsage{Err: fmt.Errorf("invalid --ts: %w", err)}
	}
	return t.UTC(), nil
}

func (a *app) open() (store.Store, error) { return store.Resolve(a.storePath) }

func (a *app) initCmd() *cobra.Command {
	return &cobra.Command{
		Use:  "init [PATH]",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ".memlog"
			if len(args) == 1 {
				path = args[0]
			} else if a.storePath != "" {
				path = a.storePath
			}
			now, err := a.now()
			if err != nil {
				return err
			}
			_, meta, err := store.Init(cmd.Context(), path, now, render.Memory)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(meta)
			}
			fmt.Fprintln(cmd.OutOrStdout(), meta.StoreID)
			return nil
		},
	}
}

func (a *app) addCmd() *cobra.Command {
	var tags, subject, session, agent, source string
	var stdin bool
	cmd := &cobra.Command{
		Use:  "add [FACT]",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdin == (len(args) == 1) {
				return store.ErrUsage{Err: fmt.Errorf("provide either FACT or --stdin")}
			}
			st, err := a.open()
			if err != nil {
				return err
			}
			now, err := a.now()
			if err != nil {
				return err
			}
			facts := args
			if stdin {
				if facts, err = readFacts(cmd.InOrStdin()); err != nil {
					return err
				}
				if len(facts) == 0 {
					return store.ErrUsage{Err: fmt.Errorf("no facts on stdin")}
				}
			}
			entries := store.NewEntries(model.OpAdd, facts, parseTags(tags), subject, session, agent, source, nil, now)
			if len(entries) == 1 {
				return a.writeEntry(cmd, st, entries[0])
			}
			body := strings.Join(facts, "\n")
			body += "\n\nMemlog-Session: " + session
			body += "\nMemlog-Agent: " + agent
			message := fmt.Sprintf("memlog: add %d facts", len(entries))
			if err := st.Append(cmd.Context(), entries, render.Memory, message, body); err != nil {
				return err
			}
			for _, e := range entries {
				fmt.Fprintln(cmd.OutOrStdout(), e.ID)
			}
			return nil
		},
	}
	addEntryFlags(cmd, &tags, &subject, &session, &agent, &source)
	cmd.Flags().BoolVar(&stdin, "stdin", false, "read facts from stdin, one per line")
	return cmd
}

func readFacts(r io.Reader) ([]string, error) {
	var facts []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		facts = append(facts, line)
	}
	return facts, sc.Err()
}

func (a *app) supersedeCmd() *cobra.Command {
	var tags, subject, session, agent, source string
	var inherit bool
	cmd := &cobra.Command{
		Use:  "supersede REF FACT",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.open()
			if err != nil {
				return err
			}
			state, err := st.Load()
			if err != nil {
				return err
			}
			ref, err := state.ResolvePrefix(args[0])
			if err != nil {
				return err
			}
			now, err := a.now()
			if err != nil {
				return err
			}
			entryTags := parseTags(tags)
			if inherit {
				target := state.ByID[ref]
				if !cmd.Flags().Changed("tags") {
					entryTags = target.Tags
				}
				if !cmd.Flags().Changed("subject") {
					subject = target.Subject
				}
			}
			e := store.NewEntry(model.OpSupersede, args[1], entryTags, subject, session, agent, source, &ref, now)
			return a.writeEntry(cmd, st, e)
		},
	}
	addEntryFlags(cmd, &tags, &subject, &session, &agent, &source)
	cmd.Flags().BoolVar(&inherit, "inherit", false, "copy tags and subject from REF when not given")
	return cmd
}

func (a *app) retractCmd() *cobra.Command {
	var session, source string
	cmd := &cobra.Command{
		Use:  "retract REF",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.open()
			if err != nil {
				return err
			}
			state, err := st.Load()
			if err != nil {
				return err
			}
			ref, err := state.ResolvePrefix(args[0])
			if err != nil {
				return err
			}
			now, err := a.now()
			if err != nil {
				return err
			}
			e := store.NewEntry(model.OpRetract, "", nil, "", session, "", source, &ref, now)
			return a.writeEntry(cmd, st, e)
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "session")
	cmd.Flags().StringVar(&source, "source", "", "source")
	_ = cmd.MarkFlagRequired("session")
	return cmd
}

func (a *app) writeEntry(cmd *cobra.Command, st store.Store, e model.Entry) error {
	message, body := store.CommitText(e)
	if err := st.Append(cmd.Context(), []model.Entry{e}, render.Memory, message, body); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), e.ID)
	return nil
}

func (a *app) historyCmd() *cobra.Command {
	return &cobra.Command{
		Use:  "history",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.open()
			if err != nil {
				return err
			}
			state, err := st.Load()
			if err != nil {
				return err
			}
			if a.jsonOut {
				entries := state.Entries
				if entries == nil {
					entries = []model.Entry{}
				}
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(entries); err != nil {
					return err
				}
			} else {
				for _, e := range state.Entries {
					text := e.Fact
					if e.Ref != nil && text == "" {
						text = *e.Ref
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s  %s\n", e.TS, e.Op, e.ID[:8], text)
				}
			}
			if len(state.Entries) == 0 {
				return store.ErrNotFound{Err: fmt.Errorf("no entries")}
			}
			return nil
		},
	}
}

func (a *app) showCmd() *cobra.Command {
	return &cobra.Command{
		Use:  "show REF",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.open()
			if err != nil {
				return err
			}
			state, err := st.Load()
			if err != nil {
				return err
			}
			id, err := state.ResolvePrefix(args[0])
			if err != nil {
				return err
			}
			if e := state.ByID[id]; e.Op == model.OpRetract {
				id = *e.Ref
			}
			chain := state.Chain(id)
			head := chain[len(chain)-1]
			if a.jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(chain)
			}
			status := "live"
			if state.IsRetracted(head.ID) {
				status = "retracted"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\nstatus: %s\n", head.Fact, status)
			for i, e := range chain {
				fmt.Fprintf(cmd.OutOrStdout(), "v%d  %s  %s  %s\n", i+1, e.TS, e.ID, e.Session)
			}
			return nil
		},
	}
}

func (a *app) searchCmd() *cobra.Command {
	var tag, subject string
	var all bool
	cmd := &cobra.Command{
		Use:  "search QUERY",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFilters(tag, subject); err != nil {
				return err
			}
			st, err := a.open()
			if err != nil {
				return err
			}
			state, err := st.Load()
			if err != nil {
				return err
			}
			hits := state.LiveHeads()
			if all {
				hits = state.FactEntries()
			}
			return a.printFacts(cmd, store.FilterFacts(hits, tag, subject, args[0]))
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "tag")
	cmd.Flags().StringVar(&subject, "subject", "", "subject")
	cmd.Flags().BoolVar(&all, "all", false, "include non-live entries")
	return cmd
}

func (a *app) listCmd() *cobra.Command {
	var tag, subject string
	cmd := &cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFilters(tag, subject); err != nil {
				return err
			}
			st, err := a.open()
			if err != nil {
				return err
			}
			state, err := st.Load()
			if err != nil {
				return err
			}
			return a.printFacts(cmd, store.FilterFacts(state.LiveHeads(), tag, subject, ""))
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "tag")
	cmd.Flags().StringVar(&subject, "subject", "", "subject")
	return cmd
}

func (a *app) contextCmd() *cobra.Command {
	var subject string
	var maxChars int
	cmd := &cobra.Command{
		Use:  "context",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFilters("", subject); err != nil {
				return err
			}
			st, err := a.open()
			if err != nil {
				return err
			}
			state, err := st.Load()
			if err != nil {
				return err
			}
			out, dropped := render.Context(state, subject, maxChars)
			if _, err := cmd.OutOrStdout().Write(out); err != nil {
				return err
			}
			if dropped > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "truncated: %d facts omitted to stay within %d chars\n", dropped, maxChars)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&subject, "subject", "", "subject")
	cmd.Flags().IntVar(&maxChars, "max-chars", 0, "character budget; whole facts are dropped to fit")
	return cmd
}

func validateFilters(tag, subject string) error {
	if tag != "" && !model.ValidTag(tag) {
		return store.ErrUsage{Err: fmt.Errorf("invalid tag %q", tag)}
	}
	if subject != "" && !model.ValidTag(subject) {
		return store.ErrUsage{Err: fmt.Errorf("invalid subject %q", subject)}
	}
	return nil
}

func (a *app) printFacts(cmd *cobra.Command, hits []model.Entry) error {
	if a.jsonOut {
		if hits == nil {
			hits = []model.Entry{}
		}
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(hits); err != nil {
			return err
		}
	} else {
		for _, e := range hits {
			subj := e.Subject
			if subj == "" {
				subj = "-"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s\n", e.ID[:8], subj, e.Fact)
		}
	}
	if len(hits) == 0 {
		return store.ErrNotFound{Err: fmt.Errorf("no matches")}
	}
	return nil
}

func (a *app) renderCmd() *cobra.Command {
	return &cobra.Command{
		Use:  "render",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.open()
			if err != nil {
				return err
			}
			state, err := st.Load()
			if err != nil {
				return err
			}
			next := render.Memory(state)
			path := filepath.Join(st.Dir, "MEMORY.md")
			cur, _ := os.ReadFile(path)
			if string(cur) == string(next) {
				fmt.Fprintln(cmd.OutOrStdout(), "unchanged")
				return nil
			}
			if err := store.AtomicWrite(path, next); err != nil {
				return err
			}
			paths, err := st.RepoPaths("MEMORY.md")
			if err != nil {
				return err
			}
			if err := gitio.Commit(cmd.Context(), st.RepoDir, "memlog: render", "render MEMORY.md", paths...); err != nil {
				return store.ErrGit{Err: err}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "updated")
			return nil
		},
	}
}

func (a *app) sessionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:  "sessions",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.open()
			if err != nil {
				return err
			}
			state, err := st.Load()
			if err != nil {
				return err
			}
			type srow struct {
				Session string `json:"session"`
				Count   int    `json:"count"`
				Newest  string `json:"newest"`
			}
			rowsBy := map[string]*srow{}
			for _, e := range state.Entries {
				row := rowsBy[e.Session]
				if row == nil {
					row = &srow{Session: e.Session}
					rowsBy[e.Session] = row
				}
				row.Count++
				if e.TS > row.Newest {
					row.Newest = e.TS
				}
			}
			var rows []srow
			for _, row := range rowsBy {
				rows = append(rows, *row)
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].Newest > rows[j].Newest })
			if a.jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
			}
			for _, row := range rows {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %d  %s\n", row.Newest, row.Count, row.Session)
			}
			if len(rows) == 0 {
				return store.ErrNotFound{Err: fmt.Errorf("no sessions")}
			}
			return nil
		},
	}
}

func (a *app) tagsCmd() *cobra.Command {
	return a.taxonomyCmd("tags", func(e model.Entry) []string { return e.Tags })
}

func (a *app) subjectsCmd() *cobra.Command {
	return a.taxonomyCmd("subjects", func(e model.Entry) []string {
		if e.Subject == "" {
			return nil
		}
		return []string{e.Subject}
	})
}

// taxonomyCmd lists distinct values with live-fact counts, sorted
// ascending so agents can reuse the existing taxonomy.
func (a *app) taxonomyCmd(use string, valuesOf func(model.Entry) []string) *cobra.Command {
	return &cobra.Command{
		Use:  use,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.open()
			if err != nil {
				return err
			}
			state, err := st.Load()
			if err != nil {
				return err
			}
			counts := map[string]int{}
			for _, e := range state.LiveHeads() {
				for _, v := range valuesOf(e) {
					counts[v]++
				}
			}
			type row struct {
				Name  string `json:"name"`
				Count int    `json:"count"`
			}
			rows := []row{}
			for name, count := range counts {
				rows = append(rows, row{Name: name, Count: count})
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
			if a.jsonOut {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(rows); err != nil {
					return err
				}
			} else {
				for _, r := range rows {
					fmt.Fprintf(cmd.OutOrStdout(), "%d  %s\n", r.Count, r.Name)
				}
			}
			if len(rows) == 0 {
				return store.ErrNotFound{Err: fmt.Errorf("no %s", use)}
			}
			return nil
		},
	}
}

func (a *app) doctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:  "doctor",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.open()
			if err != nil {
				return err
			}
			var problems []string
			state, err := st.Load()
			if err != nil {
				problems = append(problems, err.Error())
			}
			if err == nil {
				next := render.Memory(state)
				cur, _ := os.ReadFile(filepath.Join(st.Dir, "MEMORY.md"))
				if string(cur) != string(next) {
					problems = append(problems, "MEMORY.md is stale")
					if fix {
						if err := store.AtomicWrite(filepath.Join(st.Dir, "MEMORY.md"), next); err != nil {
							return err
						}
					}
				}
				if sup := st.SupportFileProblems(); len(sup) > 0 {
					problems = append(problems, sup...)
					if fix {
						if _, err := st.UpgradeSupportFiles(); err != nil {
							return err
						}
					}
				}
				paths, err := st.RepoPaths(".")
				if err == nil {
					dirty, derr := gitio.HasUncommitted(cmd.Context(), st.RepoDir, paths...)
					if derr != nil {
						problems = append(problems, derr.Error())
					} else if dirty {
						problems = append(problems, "store has uncommitted changes")
						if fix {
							if err := gitio.Commit(cmd.Context(), st.RepoDir, "memlog: doctor fix", "commit recovered store state", paths...); err != nil {
								return store.ErrGit{Err: err}
							}
						}
					}
				}
			}
			if a.jsonOut {
				report := struct {
					Clean    bool     `json:"clean"`
					Fixed    bool     `json:"fixed"`
					Problems []string `json:"problems"`
				}{Clean: len(problems) == 0, Fixed: fix && len(problems) > 0, Problems: problems}
				if report.Problems == nil {
					report.Problems = []string{}
				}
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(report); err != nil {
					return err
				}
				if len(problems) > 0 && !fix {
					return store.ErrNotFound{Err: fmt.Errorf("problems found")}
				}
				return nil
			}
			if len(problems) > 0 && !fix {
				for _, p := range problems {
					fmt.Fprintln(cmd.ErrOrStderr(), p)
				}
				return store.ErrNotFound{Err: fmt.Errorf("problems found")}
			}
			if len(problems) > 0 && fix {
				fmt.Fprintln(cmd.OutOrStdout(), "fixed")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "clean")
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "fix problems")
	return cmd
}

func (a *app) mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve memlog tools over the Model Context Protocol (stdio)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := &mcp.Server{StorePath: a.storePath, Version: Version}
			return s.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

func addEntryFlags(cmd *cobra.Command, tags, subject, session, agent, source *string) {
	cmd.Flags().StringVar(tags, "tags", "", "comma-separated tags")
	cmd.Flags().StringVar(subject, "subject", "", "subject")
	cmd.Flags().StringVar(session, "session", "", "session")
	cmd.Flags().StringVar(agent, "agent", "", "agent")
	cmd.Flags().StringVar(source, "source", "", "source")
	_ = cmd.MarkFlagRequired("session")
}

func parseTags(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var tags []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			tags = append(tags, p)
		}
	}
	return model.NormalizeTags(tags)
}
