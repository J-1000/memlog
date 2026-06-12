package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCLILifecycle(t *testing.T) {
	dir := t.TempDir()
	bin := buildCLI(t)
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test User")
	storeDir := filepath.Join(dir, ".memlog")
	initID := run(t, dir, bin, "--store", storeDir, "--ts", "2026-06-12T10:00:00Z", "init")
	require.Len(t, strings.TrimSpace(initID), 26)
	id1 := strings.TrimSpace(run(t, dir, bin, "--store", storeDir, "--ts", "2026-06-12T10:01:00Z", "add", "First fact", "--session", "s1", "--agent", "agent", "--source", "source", "--tags", "infra,staging", "--subject", "db"))
	id2 := strings.TrimSpace(run(t, dir, bin, "--store", storeDir, "--ts", "2026-06-12T10:02:00Z", "add", "Second fact", "--session", "s1"))
	_ = run(t, dir, bin, "--store", storeDir, "--ts", "2026-06-12T10:03:00Z", "add", "Third fact", "--session", "s2")
	_ = run(t, dir, bin, "--store", storeDir, "--ts", "2026-06-12T10:04:00Z", "supersede", id1[:8], "First fact updated", "--session", "s3", "--subject", "db")
	rid := strings.TrimSpace(run(t, dir, bin, "--store", storeDir, "--ts", "2026-06-12T10:05:00Z", "retract", id2[:8], "--session", "s4"))
	require.Contains(t, run(t, dir, bin, "--store", storeDir, "search", "updated"), "First fact updated")
	require.Contains(t, run(t, dir, bin, "--store", storeDir, "show", id1[:8]), "status: live")
	retractShow := run(t, dir, bin, "--store", storeDir, "show", rid[:8])
	require.Contains(t, retractShow, "Second fact")
	require.Contains(t, retractShow, "status: retracted")
	require.Contains(t, run(t, dir, bin, "--store", storeDir, "doctor"), "clean")
	require.Contains(t, run(t, dir, "git", "log", "--oneline"), "memlog: retract")
	require.Contains(t, run(t, dir, bin, "--store", storeDir, "render"), "unchanged")
}

func TestDoctorFixUpgradesSupportFiles(t *testing.T) {
	dir := t.TempDir()
	bin := buildCLI(t)
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test User")
	storeDir := filepath.Join(dir, ".memlog")
	run(t, dir, bin, "--store", storeDir, "init")
	require.NoError(t, os.Remove(filepath.Join(storeDir, ".gitattributes")))
	run(t, dir, "git", "commit", "-am", "drop gitattributes")
	out, code := runExit(t, dir, bin, "--store", storeDir, "doctor")
	require.Equal(t, 1, code)
	require.Contains(t, out, ".gitattributes is missing")
	require.Contains(t, run(t, dir, bin, "--store", storeDir, "doctor", "--fix"), "fixed")
	require.Contains(t, run(t, dir, bin, "--store", storeDir, "doctor"), "clean")
	attrs, err := os.ReadFile(filepath.Join(storeDir, ".gitattributes"))
	require.NoError(t, err)
	require.Equal(t, "journal/*.jsonl merge=union\n", string(attrs))
}

func buildCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "memlog")
	run(t, filepath.Join("..", ".."), "go", "build", "-o", bin, "./cmd/memlog")
	return bin
}

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	out, code := runExit(t, dir, name, args...)
	require.Zero(t, code, out)
	return out
}

func runExit(t *testing.T, dir string, name string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-06-12T10:00:00Z", "GIT_COMMITTER_DATE=2026-06-12T10:00:00Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit, string(out))
		return string(out), exit.ExitCode()
	}
	return string(out), 0
}
