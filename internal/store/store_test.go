package store

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/J-1000/memlog/internal/model"
	"github.com/stretchr/testify/require"
)

func TestStateResolutionRejectsInvalidRefs(t *testing.T) {
	add := NewEntry(model.OpAdd, "one", nil, "sub", "s1", "", "", nil, mustTime("2026-06-12T10:00:00Z"))
	sup := NewEntry(model.OpSupersede, "two", nil, "sub", "s2", "", "", &add.ID, mustTime("2026-06-12T10:01:00Z"))
	retract := NewEntry(model.OpRetract, "", nil, "", "s3", "", "", &sup.ID, mustTime("2026-06-12T10:02:00Z"))
	var st State
	st.ByID = map[string]model.Entry{}
	st.ReplacedBy = map[string]string{}
	st.Retracted = map[string]string{}
	st.Parent = map[string]string{}
	require.NoError(t, st.Accept(add))
	require.NoError(t, st.Accept(sup))
	require.ErrorContains(t, st.Accept(NewEntry(model.OpSupersede, "bad", nil, "", "s", "", "", &add.ID, mustTime("2026-06-12T10:03:00Z"))), "already superseded")
	require.NoError(t, st.Accept(retract))
	require.ErrorContains(t, st.Accept(NewEntry(model.OpRetract, "", nil, "", "s", "", "", &sup.ID, mustTime("2026-06-12T10:04:00Z"))), "already retracted")
	missing := "01J9XK7M3QJ8Z6W4V2T1R0PQNM"
	require.ErrorContains(t, st.Accept(NewEntry(model.OpRetract, "", nil, "", "s", "", "", &missing, mustTime("2026-06-12T10:05:00Z"))), "not found")
	require.Empty(t, st.LiveHeads())
}

func TestInitWritesSupportFiles(t *testing.T) {
	dir := initGitStore(t)
	storeDir := filepath.Join(dir, ".memlog")
	ignore, err := os.ReadFile(filepath.Join(storeDir, ".gitignore"))
	require.NoError(t, err)
	require.Equal(t, "*.lock\n*.tmp-*\n", string(ignore))
	attrs, err := os.ReadFile(filepath.Join(storeDir, ".gitattributes"))
	require.NoError(t, err)
	require.Equal(t, "journal/*.jsonl merge=union\n", string(attrs))
}

func TestUpgradeSupportFiles(t *testing.T) {
	dir := initGitStore(t)
	st, err := Open(filepath.Join(dir, ".memlog"))
	require.NoError(t, err)
	require.Empty(t, st.SupportFileProblems())
	require.NoError(t, os.Remove(filepath.Join(st.Dir, ".gitattributes")))
	require.NoError(t, os.WriteFile(filepath.Join(st.Dir, ".gitignore"), []byte("*.lock\n"), 0o644))
	require.Equal(t, []string{
		`.gitignore is missing "*.tmp-*"`,
		`.gitattributes is missing "journal/*.jsonl merge=union"`,
	}, st.SupportFileProblems())
	changed, err := st.UpgradeSupportFiles()
	require.NoError(t, err)
	require.Equal(t, []string{".gitignore", ".gitattributes"}, changed)
	require.Empty(t, st.SupportFileProblems())
	ignore, err := os.ReadFile(filepath.Join(st.Dir, ".gitignore"))
	require.NoError(t, err)
	require.Equal(t, "*.lock\n*.tmp-*\n", string(ignore))
}

func TestLoadToleratesForwardRefs(t *testing.T) {
	dir := initGitStore(t)
	st, err := Open(filepath.Join(dir, ".memlog"))
	require.NoError(t, err)
	add := NewEntry(model.OpAdd, "original", nil, "", "s1", "", "", nil, mustTime("2026-06-12T10:00:00Z"))
	sup := NewEntry(model.OpSupersede, "replacement", nil, "", "s2", "", "", &add.ID, mustTime("2026-06-12T10:01:00Z"))
	// Simulate a union merge that placed the supersede before its target.
	var lines []byte
	for _, e := range []model.Entry{sup, add} {
		b, err := json.Marshal(e)
		require.NoError(t, err)
		lines = append(lines, append(b, '\n')...)
	}
	require.NoError(t, os.WriteFile(filepath.Join(st.Dir, "journal", "2026-06.jsonl"), lines, 0o644))
	state, err := st.Load()
	require.NoError(t, err)
	heads := state.LiveHeads()
	require.Len(t, heads, 1)
	require.Equal(t, "replacement", heads[0].Fact)
	require.Equal(t, []model.Entry{state.ByID[add.ID], state.ByID[sup.ID]}, state.Entries)
}

func TestLoadResolvesRefWithSmallerULID(t *testing.T) {
	dir := initGitStore(t)
	st, err := Open(filepath.Join(dir, ".memlog"))
	require.NoError(t, err)
	// Same-second writes can hand the supersede a ULID that sorts before
	// its target; resolution must not depend on id order.
	idAdd := "01J9XK7M3QJ8Z6W4V2T1R0PQNB"
	idSup := "01J9XK7M3QJ8Z6W4V2T1R0PQNA"
	add := model.Entry{ID: idAdd, TS: "2026-06-12T10:00:00Z", Op: model.OpAdd, Fact: "original", Session: "s1"}
	sup := model.Entry{ID: idSup, TS: "2026-06-12T10:00:00Z", Op: model.OpSupersede, Fact: "replacement", Session: "s2", Ref: &idAdd}
	var lines []byte
	for _, e := range []model.Entry{add, sup} {
		b, err := json.Marshal(e)
		require.NoError(t, err)
		lines = append(lines, append(b, '\n')...)
	}
	require.NoError(t, os.WriteFile(filepath.Join(st.Dir, "journal", "2026-06.jsonl"), lines, 0o644))
	state, err := st.Load()
	require.NoError(t, err)
	heads := state.LiveHeads()
	require.Len(t, heads, 1)
	require.Equal(t, "replacement", heads[0].Fact)
	require.Equal(t, []model.Entry{add, sup}, state.Chain(idSup))
	require.Equal(t, idAdd, state.RootOf(idSup))
}

func TestAppendMonthRolloverAndDeterminism(t *testing.T) {
	dir := initGitStore(t)
	st, err := Open(filepath.Join(dir, ".memlog"))
	require.NoError(t, err)
	a := NewEntry(model.OpAdd, "June fact", []string{"a"}, "alpha", "s1", "", "", nil, mustTime("2026-06-30T23:59:00Z"))
	require.NoError(t, st.Append(context.Background(), []model.Entry{a}, stubMemory, "memlog: add "+a.ID, "June fact\n\nMemlog-Session: s1"))
	b := NewEntry(model.OpAdd, "July fact", []string{"b"}, "beta", "s2", "", "", nil, mustTime("2026-07-01T00:00:00Z"))
	require.NoError(t, st.Append(context.Background(), []model.Entry{b}, stubMemory, "memlog: add "+b.ID, "July fact\n\nMemlog-Session: s2"))
	require.FileExists(t, filepath.Join(st.Dir, "journal", "2026-06.jsonl"))
	require.FileExists(t, filepath.Join(st.Dir, "journal", "2026-07.jsonl"))
	loaded, err := st.Load()
	require.NoError(t, err)
	require.Len(t, loaded.LiveHeads(), 2)
}

func TestConcurrentAppendDoesNotCorruptJournal(t *testing.T) {
	dir := initGitStore(t)
	bin := buildBinary(t)
	storeDir := filepath.Join(dir, ".memlog")
	var wg sync.WaitGroup
	for _, fact := range []string{"one", "two"} {
		wg.Add(1)
		go func(fact string) {
			defer wg.Done()
			cmd := exec.Command(bin, "--store", storeDir, "add", fact, "--session", "s")
			cmd.Dir = dir
			_ = cmd.Run()
		}(fact)
	}
	wg.Wait()
	st, err := Open(storeDir)
	require.NoError(t, err)
	state, err := st.Load()
	require.NoError(t, err)
	require.Len(t, state.Entries, 2)
	doctor := exec.Command(bin, "--store", storeDir, "doctor")
	doctor.Dir = dir
	out, err := doctor.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "clean")
}

func stubMemory(State) []byte { return []byte("memory\n") }

func initGitStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test User")
	_, _, err := Init(context.Background(), filepath.Join(dir, ".memlog"), mustTime("2026-06-12T10:00:00Z"), stubMemory)
	require.NoError(t, err)
	return dir
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "memlog")
	run(t, filepath.Join("..", ".."), "go", "build", "-o", bin, "./cmd/memlog")
	return bin
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-06-12T10:00:00Z", "GIT_COMMITTER_DATE=2026-06-12T10:00:00Z")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
