package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/J-1000/memlog/internal/render"
	"github.com/J-1000/memlog/internal/store"
	"github.com/stretchr/testify/require"
)

func TestServeLifecycle(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test User")
	storeDir := filepath.Join(dir, ".memlog")
	_, _, err := store.Init(context.Background(), storeDir, time.Now().UTC(), render.Memory)
	require.NoError(t, err)
	s := &Server{StorePath: storeDir, Version: "test"}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memlog_add","arguments":{"fact":"MCP fact","session":"mcp-s1","tags":["mcp"]}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"memlog_search","arguments":{"query":"mcp"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"memlog_add","arguments":{"fact":"no session"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"memlog_nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"nonsense"}`,
	}
	var out bytes.Buffer
	require.NoError(t, s.Serve(context.Background(), strings.NewReader(strings.Join(requests, "\n")+"\n"), &out))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 7) // the notification gets no response

	type resp struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	decode := func(line string) resp {
		var r resp
		require.NoError(t, json.Unmarshal([]byte(line), &r))
		return r
	}

	init := decode(lines[0])
	require.Contains(t, string(init.Result), `"protocolVersion":"2025-06-18"`)
	require.Contains(t, string(init.Result), `"name":"memlog"`)

	list := decode(lines[1])
	for _, name := range []string{"memlog_add", "memlog_search", "memlog_show", "memlog_supersede", "memlog_retract"} {
		require.Contains(t, string(list.Result), `"name":"`+name+`"`)
	}

	var added toolResult
	require.NoError(t, json.Unmarshal(decode(lines[2]).Result, &added))
	require.False(t, added.IsError)
	require.Len(t, added.Content[0].Text, 26)

	var search toolResult
	require.NoError(t, json.Unmarshal(decode(lines[3]).Result, &search))
	require.Contains(t, search.Content[0].Text, "MCP fact")

	var invalid toolResult
	require.NoError(t, json.Unmarshal(decode(lines[4]).Result, &invalid))
	require.True(t, invalid.IsError)
	require.Contains(t, invalid.Content[0].Text, "session must be 1-128 printable ASCII characters")

	require.Equal(t, -32602, decode(lines[5]).Error.Code)
	require.Equal(t, -32601, decode(lines[6]).Error.Code)
}

func TestServeShowSupersedeRetract(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test User")
	storeDir := filepath.Join(dir, ".memlog")
	_, _, err := store.Init(context.Background(), storeDir, time.Now().UTC(), render.Memory)
	require.NoError(t, err)
	s := &Server{StorePath: storeDir, Version: "test"}

	id, err := toolAdd(context.Background(), s, json.RawMessage(`{"fact":"v1","session":"s1","tags":["infra"],"subject":"db"}`))
	require.NoError(t, err)
	id2, err := toolSupersede(context.Background(), s, json.RawMessage(`{"ref":"`+id+`","fact":"v2","session":"s2","inherit":true}`))
	require.NoError(t, err)
	chain, err := toolShow(context.Background(), s, json.RawMessage(`{"ref":"`+id+`"}`))
	require.NoError(t, err)
	require.Contains(t, chain, `"fact":"v1"`)
	require.Contains(t, chain, `"fact":"v2"`)
	require.Contains(t, chain, `"tags":["infra"]`)
	_, err = toolRetract(context.Background(), s, json.RawMessage(`{"ref":"`+id2+`","session":"s3"}`))
	require.NoError(t, err)
	hits, err := toolSearch(context.Background(), s, json.RawMessage(`{"query":"v2"}`))
	require.NoError(t, err)
	require.Equal(t, "[]", hits)
	_, err = toolAdd(context.Background(), s, json.RawMessage(`{"fact":"x","session":"s","bogus":true}`))
	require.ErrorContains(t, err, "invalid arguments")
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}
