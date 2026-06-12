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
	Roots      map[string]string
	Meta       Meta
}

func Resolve(start, explicit string) (Store, error) {
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
			return Store{}, fmt.Errorf("memlog store not found")
		}
		dir = parent
	}
}

func Open(dir string) (Store, error) { return open(filepath.Clean(dir)) }

func open(dir string) (Store, error) {
	dir = canonical(dir)
	if !exists(filepath.Join(dir, "meta.json")) {
		return Store{}, fmt.Errorf("memlog store not found at %s", dir)
	}
	repoDir := dir
	if root, err := gitio.WorkTreeRoot(context.Background(), dir); err == nil {
		repoDir = root
	}
	return Store{Dir: dir, RepoDir: repoDir}, nil
}

func Init(ctx context.Context, path string, now time.Time) (Store, Meta, error) {
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
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.lock\n"), 0o644); err != nil {
		return Store{}, Meta{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("# Memory\n\n_Last updated: never · 0 live facts · 0 retracted · store "+meta.StoreID+"_\n\n## Provenance\n\n| id | learned | session | agent | source | history |\n|---|---|---|---|---|---|\n"), 0o644); err != nil {
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
	paths, err := st.repoPaths("meta.json", ".gitignore", "MEMORY.md")
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
	state := State{ByID: map[string]model.Entry{}, ReplacedBy: map[string]string{}, Retracted: map[string]string{}, Roots: map[string]string{}, Meta: meta}
	for _, file := range files {
		if err := readLines(file, func(line string) error {
			var e model.Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
			if err := state.Accept(e); err != nil {
				return err
			}
			return nil
		}); err != nil {
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
	if err := model.Validate(e); err != nil {
		return err
	}
	if _, ok := st.ByID[e.ID]; ok {
		return fmt.Errorf("duplicate id %s", e.ID)
	}
	if e.Ref != nil {
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
			return fmt.Errorf("entry %s is already superseded by %s", ref, by)
		}
		if e.Op == model.OpSupersede {
			st.ReplacedBy[ref] = e.ID
			st.Roots[e.ID] = st.RootOf(ref)
		} else {
			st.Retracted[ref] = e.ID
		}
	} else {
		st.Roots[e.ID] = e.ID
	}
	st.ByID[e.ID] = e
	st.Entries = append(st.Entries, e)
	return nil
}

func (st State) RootOf(id string) string {
	if root := st.Roots[id]; root != "" {
		return root
	}
	return id
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

func (s Store) Append(ctx context.Context, e model.Entry, memory []byte, commitBody string) error {
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
	if err := state.Accept(e); err != nil {
		return ErrUsage{Err: err}
	}
	month := e.TS[:7]
	journalRel := filepath.Join("journal", month+".jsonl")
	journalPath := filepath.Join(s.Dir, journalRel)
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(journalPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := AtomicWrite(filepath.Join(s.Dir, "MEMORY.md"), memory); err != nil {
		return err
	}
	paths, err := s.repoPaths(journalRel, "MEMORY.md")
	if err != nil {
		return err
	}
	if err := gitio.Commit(ctx, s.RepoDir, "memlog: "+e.Op+" "+e.ID, commitBody, paths...); err != nil {
		return ErrGit{Err: err}
	}
	return nil
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

func NewEntry(op, fact string, tags []string, subject, session, agent, source string, ref *string, ts time.Time) model.Entry {
	return model.Entry{
		ID:      model.NewID(ts.UTC(), ulid.Monotonic(rand.Reader, 0)),
		TS:      ts.UTC().Format(time.RFC3339),
		Op:      op,
		Fact:    fact,
		Tags:    model.NormalizeTags(tags),
		Subject: subject,
		Session: session,
		Agent:   agent,
		Source:  source,
		Ref:     ref,
	}
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
