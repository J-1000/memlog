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
	list := run(t, dir, bin, "--store", storeDir, "list")
	require.Contains(t, list, "First fact updated")
	require.Contains(t, list, "Third fact")
	require.NotContains(t, list, "Second fact")
	require.Contains(t, run(t, dir, bin, "--store", storeDir, "list", "--subject", "db"), "First fact updated")
	searchJSON := run(t, dir, bin, "--store", storeDir, "--json", "search", "updated")
	require.True(t, strings.HasPrefix(searchJSON, `[{"id":`), searchJSON)
	require.Contains(t, run(t, dir, bin, "--store", storeDir, "show", id1[:8]), "status: live")
	retractShow := run(t, dir, bin, "--store", storeDir, "show", rid[:8])
	require.Contains(t, retractShow, "Second fact")
	require.Contains(t, retractShow, "status: retracted")
	require.Equal(t, "1  db\n", run(t, dir, bin, "--store", storeDir, "subjects"))
	require.Contains(t, run(t, dir, bin, "--store", storeDir, "doctor"), "clean")
	require.Equal(t, "{\"clean\":true,\"fixed\":false,\"problems\":[]}\n", run(t, dir, bin, "--store", storeDir, "--json", "doctor"))
	require.Contains(t, run(t, dir, "git", "log", "--oneline"), "memlog: retract")
	require.Contains(t, run(t, dir, bin, "--store", storeDir, "render"), "unchanged")
}

func TestAddStdinBatch(t *testing.T) {
	dir := t.TempDir()
	bin := buildCLI(t)
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test User")
	storeDir := filepath.Join(dir, ".memlog")
	run(t, dir, bin, "--store", storeDir, "init")
	cmd := exec.Command(bin, "--store", storeDir, "add", "--stdin", "--session", "s1", "--tags", "batch")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("first fact\n\nsecond fact\n")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	ids := strings.Fields(string(out))
	require.Len(t, ids, 2)
	require.Less(t, ids[0], ids[1])
	log := run(t, dir, "git", "log", "--oneline")
	require.Contains(t, log, "memlog: add 2 facts")
	require.Contains(t, run(t, dir, bin, "--store", storeDir, "search", "second"), "second fact")
	require.Equal(t, "2  batch\n", run(t, dir, bin, "--store", storeDir, "tags"))
	require.Equal(t, "[{\"name\":\"batch\",\"count\":2}]\n", run(t, dir, bin, "--store", storeDir, "--json", "tags"))
	_, code := runExit(t, dir, bin, "--store", storeDir, "add", "fact", "--stdin", "--session", "s1")
	require.Equal(t, 2, code)
}

func TestSupersedeInherit(t *testing.T) {
	dir := t.TempDir()
	bin := buildCLI(t)
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test User")
	storeDir := filepath.Join(dir, ".memlog")
	run(t, dir, bin, "--store", storeDir, "init")
	id := strings.TrimSpace(run(t, dir, bin, "--store", storeDir, "add", "v1", "--session", "s1", "--tags", "infra,staging", "--subject", "db"))
	id2 := strings.TrimSpace(run(t, dir, bin, "--store", storeDir, "supersede", id, "v2", "--session", "s2", "--inherit"))
	hit := run(t, dir, bin, "--store", storeDir, "--json", "search", "v2")
	require.Contains(t, hit, `"tags":["infra","staging"]`)
	require.Contains(t, hit, `"subject":"db"`)
	run(t, dir, bin, "--store", storeDir, "supersede", id2, "v3", "--session", "s3", "--inherit", "--tags", "ops")
	hit = run(t, dir, bin, "--store", storeDir, "--json", "search", "v3")
	require.Contains(t, hit, `"tags":["ops"]`)
	require.Contains(t, hit, `"subject":"db"`)
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

func TestMCPSubcommand(t *testing.T) {
	dir := t.TempDir()
	bin := buildCLI(t)
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test User")
	storeDir := filepath.Join(dir, ".memlog")
	run(t, dir, bin, "--store", storeDir, "init")
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memlog_add","arguments":{"fact":"via mcp","session":"mcp-s"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memlog_search","arguments":{"query":"via mcp"}}}`,
	}, "\n") + "\n"
	cmd := exec.Command(bin, "--store", storeDir, "mcp")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(requests)
	out, err := cmd.Output()
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	require.Len(t, lines, 3)
	require.Contains(t, lines[0], `"serverInfo":{"name":"memlog"`)
	require.Contains(t, lines[2], "via mcp")
	require.Contains(t, run(t, dir, bin, "--store", storeDir, "search", "via mcp"), "via mcp")
	require.Contains(t, run(t, dir, "git", "log", "--oneline"), "memlog: add")
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
