package store

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/J-1000/memlog/internal/gitio"
	"github.com/J-1000/memlog/internal/model"
	"github.com/gofrs/flock"
	"github.com/oklog/ulid/v2"
)

// supportFiles lists the store support files and the lines each must
// contain. init writes them in full; doctor --fix appends missing lines
// to stores created by older versions.
var supportFiles = []struct {
	name  string
	lines []string
}{
	{".gitignore", []string{"*.lock", "*.tmp-*"}},
	// Journal lines are append-only with ULID ids, so union-merging
	// concurrent appends from synced machines is safe.
	{".gitattributes", []string{"journal/*.jsonl merge=union"}},
}

type Store struct {
	Dir     string
	RepoDir string
}

type Meta struct {
	Version   int    `json:"version"`
	CreatedAt string `json:"created_at"`
	StoreID   string `json:"store_id"`
}

type State struct {
	Entries    []model.Entry
	ByID       map[string]model.Entry
	ReplacedBy map[string]string
	Retracted  map[string]string
	Parent     map[string]string
	Meta       Meta
}

func Resolve(explicit string) (Store, error) {
	if explicit != "" {
		return open(filepath.Clean(explicit))
	}
	if env := os.Getenv("MEMLOG_STORE"); env != "" {
		return open(filepath.Clean(env))
	}
	dir, err := os.Getwd()
	if err != nil {
		return Store{}, err
	}
	for {
		candidate := filepath.Join(dir, ".memlog")
		if exists(candidate) {
			return open(candidate)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Store{}, ErrNotFound{Err: fmt.Errorf("memlog store not found")}
		}
		dir = parent
	}
}

func Open(dir string) (Store, error) { return open(filepath.Clean(dir)) }

func open(dir string) (Store, error) {
	dir = canonical(dir)
	if !exists(filepath.Join(dir, "meta.json")) {
		return Store{}, ErrNotFound{Err: fmt.Errorf("memlog store not found at %s", dir)}
	}
	repoDir := dir
	if root, err := gitio.WorkTreeRoot(context.Background(), dir); err == nil {
		repoDir = root
	}
	s := Store{Dir: dir, RepoDir: repoDir}
	meta, err := s.Meta()
	if err != nil {
		return Store{}, err
	}
	if meta.Version > 1 {
		return Store{}, ErrUsage{Err: fmt.Errorf("store version %d not supported; upgrade memlog", meta.Version)}
	}
	return s, nil
}

func Init(ctx context.Context, path string, now time.Time, renderMemory func(State) []byte) (Store, Meta, error) {
	dir := filepath.Clean(path)
	if exists(dir) {
		return Store{}, Meta{}, ErrUsage{Err: fmt.Errorf("store already exists at %s", dir)}
	}
	if err := os.MkdirAll(filepath.Join(dir, "journal"), 0o755); err != nil {
		return Store{}, Meta{}, err
	}
	dir = canonical(dir)
	entropy := ulid.Monotonic(rand.Reader, 0)
	meta := Meta{Version: 1, CreatedAt: now.UTC().Format(time.RFC3339), StoreID: model.NewID(now.UTC(), entropy)}
	if err := writeJSON(filepath.Join(dir, "meta.json"), meta); err != nil {
		return Store{}, Meta{}, err
	}
	for _, sf := range supportFiles {
		if err := os.WriteFile(filepath.Join(dir, sf.name), []byte(strings.Join(sf.lines, "\n")+"\n"), 0o644); err != nil {
			return Store{}, Meta{}, err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), renderMemory(State{Meta: meta}), 0o644); err != nil {
		return Store{}, Meta{}, err
	}
	repoDir := dir
	if gitio.InsideWorkTree(ctx, filepath.Dir(dir)) {
		root, err := gitio.WorkTreeRoot(ctx, filepath.Dir(dir))
		if err != nil {
			return Store{}, Meta{}, err
		}
		repoDir = root
	} else {
		if _, err := gitio.Run(ctx, dir, "init"); err != nil {
			return Store{}, Meta{}, err
		}
	}
	st := Store{Dir: dir, RepoDir: repoDir}
	paths, err := st.repoPaths("meta.json", ".gitignore", ".gitattributes", "MEMORY.md")
	if err != nil {
		return Store{}, Meta{}, err
	}
	if err := gitio.Commit(ctx, repoDir, "memlog: init store "+meta.StoreID, "", paths...); err != nil {
		return Store{}, Meta{}, ErrGit{Err: err}
	}
	return st, meta, nil
}

func (s Store) Load() (State, error) {
	meta, err := s.Meta()
	if err != nil {
		return State{}, err
	}
	files, err := filepath.Glob(filepath.Join(s.Dir, "journal", "*.jsonl"))
	if err != nil {
		return State{}, err
	}
	slices.Sort(files)
	state := State{ByID: map[string]model.Entry{}, ReplacedBy: map[string]string{}, Retracted: map[string]string{}, Parent: map[string]string{}, Meta: meta}
	// A git union merge can place a supersede/retract line before the entry
	// it references, so index every entry first and apply refs second.
	// Sorting by ULID makes the result independent of line order.
	var entries []model.Entry
	for _, file := range files {
		if err := readLines(file, func(line string) error {
			var e model.Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
			entries = append(entries, e)
			return nil
		}); err != nil {
			return State{}, err
		}
	}
	slices.SortStableFunc(entries, func(a, b model.Entry) int { return strings.Compare(a.ID, b.ID) })
	for _, e := range entries {
		if err := state.index(e); err != nil {
			return State{}, err
		}
	}
	for _, e := range entries {
		if err := state.applyRef(e); err != nil {
			return State{}, err
		}
	}
	return state, nil
}

func (s Store) Meta() (Meta, error) {
	var meta Meta
	b, err := os.ReadFile(filepath.Join(s.Dir, "meta.json"))
	if err != nil {
		return meta, err
	}
	return meta, json.Unmarshal(b, &meta)
}

func (st *State) Accept(e model.Entry) error {
	if err := st.index(e); err != nil {
		return err
	}
	if err := st.applyRef(e); err != nil {
		delete(st.ByID, e.ID)
		st.Entries = st.Entries[:len(st.Entries)-1]
		return err
	}
	return nil
}

func (st *State) index(e model.Entry) error {
	if err := model.Validate(e); err != nil {
		return err
	}
	if _, ok := st.ByID[e.ID]; ok {
		return fmt.Errorf("duplicate id %s", e.ID)
	}
	st.ByID[e.ID] = e
	st.Entries = append(st.Entries, e)
	return nil
}

func (st *State) applyRef(e model.Entry) error {
	if e.Ref == nil {
		return nil
	}
	ref := *e.Ref
	target, ok := st.ByID[ref]
	if !ok {
		return fmt.Errorf("entry %s not found", ref)
	}
	if target.Op != model.OpAdd && target.Op != model.OpSupersede {
		return fmt.Errorf("entry %s cannot be referenced", ref)
	}
	if by, ok := st.ReplacedBy[ref]; ok {
		return fmt.Errorf("entry %s is already superseded by %s", ref, by)
	}
	if by, ok := st.Retracted[ref]; ok {
		return fmt.Errorf("entry %s is already retracted by %s", ref, by)
	}
	for cur := ref; cur != ""; cur = st.Parent[cur] {
		if cur == e.ID {
			return fmt.Errorf("entry %s creates a supersede cycle", e.ID)
		}
	}
	if e.Op == model.OpSupersede {
		st.ReplacedBy[ref] = e.ID
		st.Parent[e.ID] = ref
	} else {
		st.Retracted[ref] = e.ID
	}
	return nil
}

func (st State) RootOf(id string) string {
	for {
		parent, ok := st.Parent[id]
		if !ok {
			return id
		}
		id = parent
	}
}

func (st State) Head(id string) string {
	for {
		next := st.ReplacedBy[id]
		if next == "" {
			return id
		}
		id = next
	}
}

func (st State) IsRetracted(head string) bool { return st.Retracted[head] != "" }

func (st State) LiveHeads() []model.Entry {
	var out []model.Entry
	for _, e := range st.Entries {
		if (e.Op == model.OpAdd || e.Op == model.OpSupersede) && st.Head(e.ID) == e.ID && !st.IsRetracted(e.ID) {
			out = append(out, e)
		}
	}
	return out
}

func (st State) Chain(id string) []model.Entry {
	root := st.RootOf(id)
	var chain []model.Entry
	for cur := root; cur != ""; cur = st.ReplacedBy[cur] {
		chain = append(chain, st.ByID[cur])
	}
	return chain
}

// FilterFacts narrows entries by tag, subject, and case-insensitive
// substring query; empty filters match everything.
func FilterFacts(entries []model.Entry, tag, subject, query string) []model.Entry {
	q := strings.ToLower(query)
	var out []model.Entry
	for _, e := range entries {
		if tag != "" && !slices.Contains(e.Tags, tag) {
			continue
		}
		if subject != "" && e.Subject != subject {
			continue
		}
		if !strings.Contains(strings.ToLower(e.Fact), q) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// FactEntries returns every add/supersede entry, including superseded
// and retracted versions.
func (st State) FactEntries() []model.Entry {
	var out []model.Entry
	for _, e := range st.Entries {
		if e.Op == model.OpAdd || e.Op == model.OpSupersede {
			out = append(out, e)
		}
	}
	return out
}

func (st State) ResolvePrefix(prefix string) (string, error) {
	if len(prefix) < 8 {
		return "", ErrUsage{Err: fmt.Errorf("ref prefix must be at least 8 characters")}
	}
	var matches []string
	for id := range st.ByID {
		if strings.HasPrefix(id, prefix) {
			matches = append(matches, id)
		}
	}
	slices.Sort(matches)
	if len(matches) == 0 {
		return "", ErrNotFound{Err: fmt.Errorf("entry %s not found", prefix)}
	}
	if len(matches) > 1 {
		return "", ErrUsage{Err: fmt.Errorf("ambiguous ref %s: %s", prefix, strings.Join(matches, ", "))}
	}
	return matches[0], nil
}

// Append validates and appends entries under one lock, one journal
// replay, and one git commit.
func (s Store) Append(ctx context.Context, entries []model.Entry, renderMemory func(State) []byte, message, body string) error {
	lock := flock.New(filepath.Join(s.Dir, "journal.lock"))
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ok, err := lock.TryLockContext(lockCtx, 100*time.Millisecond)
	if err != nil {
		return err
	}
	if !ok {
		return ErrLocked{Err: fmt.Errorf("store is locked by another process")}
	}
	defer lock.Unlock()
	state, err := s.Load()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := state.Accept(e); err != nil {
			return ErrUsage{Err: err}
		}
	}
	relPaths := []string{"MEMORY.md"}
	written := map[string]*os.File{}
	defer func() {
		for _, f := range written {
			f.Close()
		}
	}()
	for _, e := range entries {
		journalRel := filepath.Join("journal", e.TS[:7]+".jsonl")
		f := written[journalRel]
		if f == nil {
			journalPath := filepath.Join(s.Dir, journalRel)
			if err := os.MkdirAll(filepath.Dir(journalPath), 0o755); err != nil {
				return err
			}
			f, err = os.OpenFile(journalPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			written[journalRel] = f
			relPaths = append(relPaths, journalRel)
		}
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	for _, f := range written {
		if err := f.Sync(); err != nil {
			return err
		}
	}
	if err := AtomicWrite(filepath.Join(s.Dir, "MEMORY.md"), renderMemory(state)); err != nil {
		return err
	}
	paths, err := s.repoPaths(relPaths...)
	if err != nil {
		return err
	}
	if err := gitio.Commit(ctx, s.RepoDir, message, body, paths...); err != nil {
		return ErrGit{Err: err}
	}
	return nil
}

// SupportFileProblems reports required support-file lines missing from
// the store, in a stable order.
func (s Store) SupportFileProblems() []string {
	var problems []string
	for _, sf := range supportFiles {
		for _, line := range s.missingSupportLines(sf.name, sf.lines) {
			problems = append(problems, fmt.Sprintf("%s is missing %q", sf.name, line))
		}
	}
	return problems
}

// UpgradeSupportFiles appends missing required lines to the store's
// support files and returns the names of the files it changed. Existing
// lines are never rewritten.
func (s Store) UpgradeSupportFiles() ([]string, error) {
	var changed []string
	for _, sf := range supportFiles {
		missing := s.missingSupportLines(sf.name, sf.lines)
		if len(missing) == 0 {
			continue
		}
		path := filepath.Join(s.Dir, sf.name)
		cur, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		next := string(cur)
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		next += strings.Join(missing, "\n") + "\n"
		if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
			return nil, err
		}
		changed = append(changed, sf.name)
	}
	return changed, nil
}

func (s Store) missingSupportLines(name string, lines []string) []string {
	b, _ := os.ReadFile(filepath.Join(s.Dir, name))
	have := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var missing []string
	for _, l := range lines {
		if !have[l] {
			missing = append(missing, l)
		}
	}
	return missing
}

func (s Store) repoPaths(paths ...string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		abs := canonical(filepath.Join(s.Dir, p))
		rel, err := filepath.Rel(s.RepoDir, abs)
		if err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	return out, nil
}

func (s Store) RepoPaths(paths ...string) ([]string, error) { return s.repoPaths(paths...) }

func AtomicWrite(path string, b []byte) error {
	tmp := path + ".tmp-" + randSuffix()
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// CommitText returns the git commit message and body for a single
// appended entry: `memlog: <op> <id>`, then the fact (or `retracts
// <ref>`) and the provenance trailers.
func CommitText(e model.Entry) (message, body string) {
	body = e.Fact
	if e.Op == model.OpRetract && e.Ref != nil {
		body = "retracts " + *e.Ref
	}
	body += "\n\nMemlog-Session: " + e.Session
	body += "\nMemlog-Agent: " + e.Agent
	return "memlog: " + e.Op + " " + e.ID, body
}

func NewEntry(op, fact string, tags []string, subject, session, agent, source string, ref *string, ts time.Time) model.Entry {
	return NewEntries(op, []string{fact}, tags, subject, session, agent, source, ref, ts)[0]
}

// NewEntries builds one entry per fact with shared metadata. A single
// monotonic entropy source keeps the ULIDs in fact order even within
// the same millisecond.
func NewEntries(op string, facts []string, tags []string, subject, session, agent, source string, ref *string, ts time.Time) []model.Entry {
	entropy := ulid.Monotonic(rand.Reader, 0)
	entries := make([]model.Entry, 0, len(facts))
	for _, fact := range facts {
		entries = append(entries, model.Entry{
			ID:      model.NewID(ts.UTC(), entropy),
			TS:      ts.UTC().Format(time.RFC3339),
			Op:      op,
			Fact:    fact,
			Tags:    model.NormalizeTags(tags),
			Subject: subject,
			Session: session,
			Agent:   agent,
			Source:  source,
			Ref:     ref,
		})
	}
	return entries
}

type ErrUsage struct{ Err error }
type ErrLocked struct{ Err error }
type ErrGit struct{ Err error }
type ErrNotFound struct{ Err error }

func (e ErrUsage) Error() string    { return e.Err.Error() }
func (e ErrLocked) Error() string   { return e.Err.Error() }
func (e ErrGit) Error() string      { return e.Err.Error() }
func (e ErrNotFound) Error() string { return e.Err.Error() }

func Code(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ErrUsage{}):
		return 2
	case errors.As(err, &ErrLocked{}):
		return 3
	case errors.As(err, &ErrGit{}):
		return 4
	case errors.As(err, &ErrNotFound{}):
		return 1
	default:
		return 4
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	real, err := filepath.EvalSymlinks(path)
	if err == nil {
		return real
	}
	return filepath.Clean(path)
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func readLines(path string, fn func(string) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		line = strings.TrimSuffix(line, "\n")
		if strings.TrimSpace(line) != "" {
			if err := fn(line); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

func randSuffix() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000_000))
	return n.String()
}
