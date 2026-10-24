// Package mcp is a minimal Model Context Protocol server over streamable HTTP.
// It is a transport adapter: tools decide everything about household state.
// Specification: https://modelcontextprotocol.io/specification/2025-06-18
package mcp

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
)

// Supported protocol revisions, newest first. Plain JSON responses and no
// server-initiated stream are valid for all of them.
var Versions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

const maxBody = 1 << 20

// Tool arguments are decoded by the tool itself. A result must encode to a JSON object.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Call        func(context.Context, json.RawMessage) (any, error)
}

// ToolError is a rejected call that the agent can correct. Any other error is
// reported without internal detail.
type ToolError struct{ Message string }

func (e *ToolError) Error() string { return e.Message }

func Reject(format string, args ...any) error { return &ToolError{fmt.Sprintf(format, args...)} }

type Info struct {
	Name, Version, Instructions string
}

type Server struct {
	info  Info
	token []byte
	tools []Tool
	index map[string]int
}

// New requires a long bearer token. Authentication is mandatory even on
// loopback: any local process could otherwise mutate the household.
func New(info Info, token string, tools []Tool) (*Server, error) {
	if len(token) < 32 {
		return nil, errors.New("MCP bearer token must have at least 32 characters")
	}
	if info.Name == "" || info.Version == "" {
		return nil, errors.New("server name and version required")
	}
	s := &Server{info: info, token: []byte(token), tools: tools, index: make(map[string]int, len(tools))}
	for i, t := range tools {
		if t.Name == "" || t.Call == nil || !json.Valid(t.InputSchema) {
			return nil, errors.New("tool needs a name, handler and valid input schema")
		}
		if _, dup := s.index[t.Name]; dup {
			return nil, errors.New("duplicate tool name")
		}
		s.index[t.Name] = i
	}
	return s, nil
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Browsers send Origin; agents do not. Refusing it prevents DNS rebinding.
	if r.Header.Get("Origin") != "" {
		http.Error(w, "cross-origin requests are not accepted", http.StatusForbidden)
		return
	}
	header := r.Header.Get("Authorization")
	given, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(given), s.token) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="hoarder"`)
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		// No server-initiated stream and no sessions to delete.
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
		return
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !supported(v) {
		http.Error(w, "unsupported MCP protocol version", http.StatusBadRequest)
		return
	}
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		http.Error(w, "cannot read request", http.StatusBadRequest)
		return
	}
	if len(body) > maxBody {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		reply(w, response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32600, "batch requests are not supported"}})
		return
	}
	var m message
	if json.Unmarshal(trimmed, &m) != nil {
		reply(w, response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
		return
	}
	isNull := len(m.ID) == 0 || string(m.ID) == "null"
	if m.Method == "" || isNull {
		// Client responses and notifications need no reply.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if m.JSONRPC != "2.0" || !(m.ID[0] == '"' || m.ID[0] == '-' || m.ID[0] >= '0' && m.ID[0] <= '9') {
		reply(w, response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32600, "invalid request"}})
		return
	}
	result, rerr := s.dispatch(r.Context(), m)
	reply(w, response{JSONRPC: "2.0", ID: m.ID, Result: result, Error: rerr})
}

func supported(v string) bool {
	for _, known := range Versions {
		if v == known {
			return true
		}
	}
	return false
}

func reply(w http.ResponseWriter, r response) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(r); err != nil {
		slog.Warn("MCP response write failed", "error", err)
	}
}

func (s *Server) dispatch(ctx context.Context, m message) (any, *rpcError) {
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if len(m.Params) > 0 && json.Unmarshal(m.Params, &p) != nil {
			return nil, &rpcError{-32602, "invalid initialize parameters"}
		}
		version := Versions[0]
		if supported(p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]string{"name": s.info.Name, "version": s.info.Version},
			"instructions":    s.info.Instructions,
		}, nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		list := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if len(m.Params) == 0 || json.Unmarshal(m.Params, &p) != nil {
			return nil, &rpcError{-32602, "invalid tool call parameters"}
		}
		i, ok := s.index[p.Name]
		if !ok {
			return nil, &rpcError{-32602, "unknown tool"}
		}
		if len(p.Arguments) == 0 || string(p.Arguments) == "null" {
			p.Arguments = json.RawMessage("{}")
		}
		return s.call(ctx, s.tools[i], p.Arguments), nil
	}
	return nil, &rpcError{-32601, "method not found"}
}

// call reports tool failures inside the result, as the protocol requires, so
// the agent can read the reason and correct its next call.
func (s *Server) call(ctx context.Context, t Tool, args json.RawMessage) (result map[string]any) {
	failed := func(text string) map[string]any {
		return map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": true}
	}
	defer func() {
		if p := recover(); p != nil {
			slog.Error("MCP tool panicked", "tool", t.Name)
			result = failed("internal error; see the hoarder log")
		}
	}()
	out, err := t.Call(ctx, args)
	var rejected *ToolError
	if errors.As(err, &rejected) {
		return failed(rejected.Message)
	}
	if err != nil {
		slog.Error("MCP tool failed", "tool", t.Name, "error", err)
		return failed("internal error; see the hoarder log")
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) == 0 || raw[0] != '{' {
		slog.Error("MCP tool returned a non-object result", "tool", t.Name)
		return failed("internal error; see the hoarder log")
	}
	return map[string]any{"content": []map[string]string{{"type": "text", "text": string(raw)}},
		"structuredContent": json.RawMessage(raw), "isError": false}
}
