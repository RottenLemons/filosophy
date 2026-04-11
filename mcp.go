// Copyright (C) 2025 Filosophy
//
// MCP (Model Context Protocol) SSE server.
// Implements the 2024-11-05 MCP spec over HTTP+SSE transport on the same port
// as the REST API, so a single port exposes both.
//
// Endpoints (all require Bearer auth):
//   GET  /mcp/sse              — SSE stream; sends endpoint + ready events
//   POST /mcp/message          — JSON-RPC 2.0 messages from the client
//
// Tools exposed:
//   search_files(query: string, limit?: number) → {results: [{path,score,size,modified}]}

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// ── JSON-RPC types ─────────────────────────────────────────────────────────────

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func rpcOK(id json.RawMessage, result any) jsonRPCResponse {
	return jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func rpcErr(id json.RawMessage, code int, msg string) jsonRPCResponse {
	return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &jsonRPCError{Code: code, Message: msg}}
}

// ── Session ────────────────────────────────────────────────────────────────────

type mcpSession struct {
	id      string
	msgCh   chan jsonRPCResponse // responses queued for SSE delivery
	closeCh chan struct{}
	authed  bool // key was verified on SSE connect — trust sessionId on POSTs
}

// ── MCPServer ──────────────────────────────────────────────────────────────────

type MCPServer struct {
	app      *APIServer
	mu       sync.Mutex
	sessions map[string]*mcpSession
}

func newMCPServer(api *APIServer) *MCPServer {
	return &MCPServer{
		app:      api,
		sessions: make(map[string]*mcpSession),
	}
}

// register mounts /mcp/sse and /mcp/message on the given mux.
// If MCPKey is empty, no auth is required. Otherwise key is verified on SSE
// connect and subsequent POSTs are trusted via sessionId.
func (m *MCPServer) register(mux *http.ServeMux) {
	if m.app.app.config.MCPKey != "" {
		mux.HandleFunc("/mcp/sse", m.app.mcpAuth(m.handleSSE))
	} else {
		mux.HandleFunc("/mcp/sse", m.handleSSE)
	}
	mux.HandleFunc("/mcp/message", m.handleMessage)
}

// ── SSE endpoint ───────────────────────────────────────────────────────────────

func (m *MCPServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	sess := &mcpSession{
		id:      fmt.Sprintf("%d", time.Now().UnixNano()),
		msgCh:   make(chan jsonRPCResponse, 32),
		closeCh: make(chan struct{}),
		authed:  true, // verified by mcpAuth (or no key required)
	}

	m.mu.Lock()
	m.sessions[sess.id] = sess
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.sessions, sess.id)
		m.mu.Unlock()
		close(sess.closeCh)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send endpoint event so the client knows where to POST messages
	port := m.app.app.config.APIPort
	if port <= 0 {
		port = 7700
	}
	fmt.Fprintf(w, "event: endpoint\ndata: http://127.0.0.1:%d/mcp/message?sessionId=%s\n\n", port, sess.id)
	flusher.Flush()

	// Keep-alive ping every 15 s
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case msg, ok := <-sess.msgCh:
			if !ok {
				return
			}
			data, _ := json.Marshal(msg)
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// ── Message endpoint ───────────────────────────────────────────────────────────

func (m *MCPServer) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok || !sess.authed {
		http.Error(w, "unknown or unauthorized session", http.StatusUnauthorized)
		return
	}

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	var resp jsonRPCResponse
	switch req.Method {
	case "initialize":
		resp = m.handleInitialize(req)
	case "notifications/initialized":
		// client acknowledgement — no response needed
		w.WriteHeader(http.StatusAccepted)
		return
	case "tools/list":
		resp = m.handleToolsList(req)
	case "tools/call":
		resp = m.handleToolsCall(req)
	case "ping":
		resp = rpcOK(req.ID, map[string]any{})
	default:
		resp = rpcErr(req.ID, -32601, "method not found: "+req.Method)
	}

	// Send response back over SSE
	select {
	case sess.msgCh <- resp:
	default:
		log.Println("mcp: session message buffer full, dropping response")
	}

	w.WriteHeader(http.StatusAccepted)
}

// ── MCP method handlers ────────────────────────────────────────────────────────

func (m *MCPServer) handleInitialize(req jsonRPCRequest) jsonRPCResponse {
	return rpcOK(req.ID, map[string]any{
		"protocolVersion": "2024-11-05",
		"serverInfo": map[string]string{
			"name":    "filosophy",
			"version": "1.0.0",
		},
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
	})
}

func (m *MCPServer) handleToolsList(req jsonRPCRequest) jsonRPCResponse {
	tools := []map[string]any{
		{
			"name":        "search_files",
			"description": "Search the user's locally indexed files using semantic + keyword search. Use short, focused queries (1-4 keywords). For author searches use the author's name. Can be called multiple times with different queries.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Short search query (1-4 keywords). Not the full user message.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max results to return (1-20, default 10).",
						"default":     10,
					},
				},
				"required": []string{"query"},
			},
		},
	}
	return rpcOK(req.ID, map[string]any{"tools": tools})
}

func (m *MCPServer) handleToolsCall(req jsonRPCRequest) jsonRPCResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return rpcErr(req.ID, -32602, "invalid params")
	}

	if params.Name != "search_files" {
		return rpcErr(req.ID, -32601, "unknown tool: "+params.Name)
	}

	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(params.Arguments, &args); err != nil || args.Query == "" {
		return rpcErr(req.ID, -32602, "missing required argument: query")
	}
	if args.Limit <= 0 || args.Limit > 20 {
		args.Limit = 10
	}

	m.app.app.mu.Lock()
	engine := m.app.app.engine
	m.app.app.mu.Unlock()
	if engine == nil {
		return rpcErr(req.ID, -32603, "engine not ready")
	}

	raw, err := engine.Search(args.Query)
	if err != nil {
		return rpcErr(req.ID, -32603, "search error: "+err.Error())
	}

	const minScore = 0.15
	type resultItem struct {
		Path     string  `json:"path"`
		Score    float64 `json:"score"`
		Size     int64   `json:"size"`
		Modified string  `json:"modified"`
	}
	items := make([]resultItem, 0, args.Limit)
	for _, r := range raw {
		if len(items) >= args.Limit {
			break
		}
		if r.Score < minScore {
			continue
		}
		items = append(items, resultItem{
			Path:     r.Path,
			Score:    float64(r.Score),
			Size:     r.Size,
			Modified: r.Modified,
		})
	}

	// Format as MCP text content so any client can display it
	resultJSON, _ := json.MarshalIndent(items, "", "  ")
	return rpcOK(req.ID, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(resultJSON)},
		},
	})
}
