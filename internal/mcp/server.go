// Package mcp implements a Model Context Protocol server over stdio,
// per docs/mcp.md: newline-delimited JSON-RPC 2.0, one client, no daemon.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/J-1000/memlog/internal/model"
	"github.com/J-1000/memlog/internal/render"
	"github.com/J-1000/memlog/internal/store"
)

type Server struct {
	StorePath string // explicit store path; empty means resolve like the CLI
	Version   string
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := enc.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}}); err != nil {
				return err
			}
			continue
		}
		resp := s.handle(ctx, req)
		if resp == nil {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (s *Server) handle(ctx context.Context, req request) *response {
	notification := len(req.ID) == 0 || string(req.ID) == "null"
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-06-18"
		}
		return result(req.ID, map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "memlog", "version": s.Version},
		})
	case "notifications/initialized":
		return nil
	case "ping":
		return result(req.ID, map[string]any{})
	case "tools/list":
		return result(req.ID, map[string]any{"tools": toolDefs()})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return rpcErr(req.ID, -32602, "invalid params")
		}
		tool, ok := tools[p.Name]
		if !ok {
			return rpcErr(req.ID, -32602, fmt.Sprintf("unknown tool %q", p.Name))
		}
		text, err := tool(ctx, s, p.Arguments)
		if err != nil {
			return result(req.ID, toolResult{Content: []toolContent{{Type: "text", Text: err.Error()}}, IsError: true})
		}
		return result(req.ID, toolResult{Content: []toolContent{{Type: "text", Text: text}}})
	default:
		if notification {
			return nil
		}
		return rpcErr(req.ID, -32601, fmt.Sprintf("method %q not found", req.Method))
	}
}

func result(id json.RawMessage, v any) *response {
	return &response{JSONRPC: "2.0", ID: id, Result: v}
}

func rpcErr(id json.RawMessage, code int, message string) *response {
	return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}

func (s *Server) open() (store.Store, error) { return store.Resolve(s.StorePath) }

func now() time.Time { return time.Now().UTC().Truncate(time.Second) }

type toolFunc func(ctx context.Context, s *Server, args json.RawMessage) (string, error)

var tools = map[string]toolFunc{
	"memlog_add":       toolAdd,
	"memlog_search":    toolSearch,
	"memlog_show":      toolShow,
	"memlog_supersede": toolSupersede,
	"memlog_retract":   toolRetract,
}

func decodeArgs(args json.RawMessage, v any) error {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func (s *Server) append(ctx context.Context, st store.Store, e model.Entry) (string, error) {
	message, body := store.CommitText(e)
	if err := st.Append(ctx, []model.Entry{e}, render.Memory, message, body); err != nil {
		return "", err
	}
	return e.ID, nil
}

func toolAdd(ctx context.Context, s *Server, args json.RawMessage) (string, error) {
	var p struct {
		Fact    string   `json:"fact"`
		Session string   `json:"session"`
		Tags    []string `json:"tags"`
		Subject string   `json:"subject"`
		Agent   string   `json:"agent"`
		Source  string   `json:"source"`
	}
	if err := decodeArgs(args, &p); err != nil {
		return "", err
	}
	st, err := s.open()
	if err != nil {
		return "", err
	}
	e := store.NewEntry(model.OpAdd, p.Fact, p.Tags, p.Subject, p.Session, p.Agent, p.Source, nil, now())
	return s.append(ctx, st, e)
}

func toolSearch(ctx context.Context, s *Server, args json.RawMessage) (string, error) {
	var p struct {
		Query   string `json:"query"`
		Tag     string `json:"tag"`
		Subject string `json:"subject"`
		All     bool   `json:"all"`
	}
	if err := decodeArgs(args, &p); err != nil {
		return "", err
	}
	if p.Tag != "" && !model.ValidTag(p.Tag) {
		return "", fmt.Errorf("invalid tag %q", p.Tag)
	}
	if p.Subject != "" && !model.ValidTag(p.Subject) {
		return "", fmt.Errorf("invalid subject %q", p.Subject)
	}
	st, err := s.open()
	if err != nil {
		return "", err
	}
	state, err := st.Load()
	if err != nil {
		return "", err
	}
	hits := state.LiveHeads()
	if p.All {
		hits = state.FactEntries()
	}
	hits = store.FilterFacts(hits, p.Tag, p.Subject, p.Query)
	if hits == nil {
		hits = []model.Entry{}
	}
	return encodeJSON(hits)
}

func toolShow(ctx context.Context, s *Server, args json.RawMessage) (string, error) {
	var p struct {
		Ref string `json:"ref"`
	}
	if err := decodeArgs(args, &p); err != nil {
		return "", err
	}
	st, err := s.open()
	if err != nil {
		return "", err
	}
	state, err := st.Load()
	if err != nil {
		return "", err
	}
	id, err := state.ResolvePrefix(p.Ref)
	if err != nil {
		return "", err
	}
	if e := state.ByID[id]; e.Op == model.OpRetract {
		id = *e.Ref
	}
	return encodeJSON(state.Chain(id))
}

func toolSupersede(ctx context.Context, s *Server, args json.RawMessage) (string, error) {
	var p struct {
		Ref     string   `json:"ref"`
		Fact    string   `json:"fact"`
		Session string   `json:"session"`
		Tags    []string `json:"tags"`
		Subject string   `json:"subject"`
		Agent   string   `json:"agent"`
		Source  string   `json:"source"`
		Inherit bool     `json:"inherit"`
	}
	if err := decodeArgs(args, &p); err != nil {
		return "", err
	}
	st, err := s.open()
	if err != nil {
		return "", err
	}
	state, err := st.Load()
	if err != nil {
		return "", err
	}
	ref, err := state.ResolvePrefix(p.Ref)
	if err != nil {
		return "", err
	}
	if p.Inherit {
		target := state.ByID[ref]
		if p.Tags == nil {
			p.Tags = target.Tags
		}
		if p.Subject == "" {
			p.Subject = target.Subject
		}
	}
	e := store.NewEntry(model.OpSupersede, p.Fact, p.Tags, p.Subject, p.Session, p.Agent, p.Source, &ref, now())
	return s.append(ctx, st, e)
}

func toolRetract(ctx context.Context, s *Server, args json.RawMessage) (string, error) {
	var p struct {
		Ref     string `json:"ref"`
		Session string `json:"session"`
		Source  string `json:"source"`
	}
	if err := decodeArgs(args, &p); err != nil {
		return "", err
	}
	st, err := s.open()
	if err != nil {
		return "", err
	}
	state, err := st.Load()
	if err != nil {
		return "", err
	}
	ref, err := state.ResolvePrefix(p.Ref)
	if err != nil {
		return "", err
	}
	e := store.NewEntry(model.OpRetract, "", nil, "", p.Session, "", p.Source, &ref, now())
	return s.append(ctx, st, e)
}

func encodeJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
