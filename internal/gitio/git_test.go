package gitio

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommitLeavesUnrelatedStagedChangesAlone(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	writeFile(t, dir, "target.txt", "target before\n")
	writeFile(t, dir, "unrelated.txt", "unrelated before\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "baseline")

	writeFile(t, dir, "target.txt", "target after\n")
	writeFile(t, dir, "unrelated.txt", "unrelated after\n")
	runGit(t, dir, "add", "unrelated.txt")

	require.NoError(t, Commit(context.Background(), dir, "update target", "body", "target.txt"))
	changed := runGit(t, dir, "show", "--format=", "--name-only", "HEAD")
	require.Equal(t, "target.txt\n", changed)
	require.Equal(t, "M  unrelated.txt\n", runGit(t, dir, "status", "--porcelain"))
	require.Equal(t, "unrelated before\n", runGit(t, dir, "show", "HEAD:unrelated.txt"))
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := Run(context.Background(), dir, args...)
	require.NoError(t, err, out)
	return out
}

func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644))
}
