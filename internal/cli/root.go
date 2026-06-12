package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/J-1000/memlog/internal/gitio"
	"github.com/J-1000/memlog/internal/model"
	"github.com/J-1000/memlog/internal/render"
	"github.com/J-1000/memlog/internal/store"
	"github.com/spf13/cobra"
)

type app struct {
	storePath string
	jsonOut   bool
	ts        string
}

func NewRoot() *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:           "memlog",
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
		a.renderCmd(),
		a.sessionsCmd(),
		a.doctorCmd(),
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

func (a *app) open() (store.Store, error) { return store.Resolve(".", a.storePath) }

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
			_, meta, err := store.Init(cmd.Context(), path, now)
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
	cmd := &cobra.Command{
		Use:  "add FACT",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.open()
			if err != nil {
				return err
			}
			now, err := a.now()
			if err != nil {
				return err
			}
			e := store.NewEntry(model.OpAdd, args[0], parseTags(tags), subject, session, agent, source, nil, now)
			return a.writeEntry(cmd, st, e)
		},
	}
	addEntryFlags(cmd, &tags, &subject, &session, &agent, &source)
	return cmd
}

func (a *app) supersedeCmd() *cobra.Command {
	var tags, subject, session, agent, source string
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
			e := store.NewEntry(model.OpSupersede, args[1], parseTags(tags), subject, session, agent, source, &ref, now)
			return a.writeEntry(cmd, st, e)
		},
	}
	addEntryFlags(cmd, &tags, &subject, &session, &agent, &source)
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
	body := e.Fact
	if e.Op == model.OpRetract && e.Ref != nil {
		body = "retracts " + *e.Ref
	}
	body += "\n\nMemlog-Session: " + e.Session
	body += "\nMemlog-Agent: " + e.Agent
	if err := st.Append(cmd.Context(), e, render.Memory, body); err != nil {
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
			for _, e := range state.Entries {
				text := e.Fact
				if e.Ref != nil && text == "" {
					text = *e.Ref
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s  %s\n", e.TS, e.Op, e.ID[:8], text)
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
			st, err := a.open()
			if err != nil {
				return err
			}
			state, err := st.Load()
			if err != nil {
				return err
			}
			q := strings.ToLower(args[0])
			var hits []model.Entry
			if all {
				for _, e := range state.Entries {
					if e.Op == model.OpAdd || e.Op == model.OpSupersede {
						hits = append(hits, e)
					}
				}
			} else {
				hits = state.LiveHeads()
			}
			count := 0
			for _, e := range hits {
				if tag != "" && !hasTag(e.Tags, tag) {
					continue
				}
				if subject != "" && e.Subject != subject {
					continue
				}
				if !strings.Contains(strings.ToLower(e.Fact), q) {
					continue
				}
				subj := e.Subject
				if subj == "" {
					subj = "-"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s\n", e.ID[:8], subj, e.Fact)
				count++
			}
			if count == 0 {
				return store.ErrNotFound{Err: fmt.Errorf("no matches")}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "tag")
	cmd.Flags().StringVar(&subject, "subject", "", "subject")
	cmd.Flags().BoolVar(&all, "all", false, "include non-live entries")
	return cmd
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
			path := st.Dir + string(os.PathSeparator) + "MEMORY.md"
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
				cur, _ := os.ReadFile(st.Dir + string(os.PathSeparator) + "MEMORY.md")
				if string(cur) != string(next) {
					problems = append(problems, "MEMORY.md is stale")
					if fix {
						if err := store.AtomicWrite(st.Dir+string(os.PathSeparator)+"MEMORY.md", next); err != nil {
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

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}
