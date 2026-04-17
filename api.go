// Copyright (C) 2025 Filosophy
//
// External HTTP/SSE API server.
// Runs on localhost:PORT alongside the Wails app, exposing:
//   POST /search         — semantic file search
//   GET  /status         — engine + indexing status
//
// All requests require the header:  Authorization: Bearer <api-key>
// The API key is generated on first enable and stored in filosophy_config.json.

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

)

// APIServer is the external HTTP server. It is started/stopped by the App
// when the user toggles the API on or off in Settings.
type APIServer struct {
	app      *App
	srv      *http.Server
	mcp      *MCPServer
	mu       sync.Mutex
	running  bool
}

func newAPIServer(app *App) *APIServer {
	s := &APIServer{app: app}
	s.mcp = newMCPServer(s)
	return s
}

// start launches the server on the configured port. No-op if already running.
func (s *APIServer) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}

	port := s.app.config.APIPort
	if port <= 0 {
		port = 7700
	}

	mux := http.NewServeMux()
	if s.app.config.APIEnabled {
		mux.HandleFunc("/search", s.apiAuth(s.handleSearch))
		mux.HandleFunc("/status", s.apiAuth(s.handleStatus))
		mux.HandleFunc("/feedback", s.apiAuth(s.handleFeedback))
		mux.HandleFunc("/admin/weights", s.apiAuth(s.handleAdminWeights))
	}
	if s.app.config.MCPEnabled {
		s.mcp.register(mux)
	}

	s.srv = &http.Server{
		Addr:        fmt.Sprintf("127.0.0.1:%d", port),
		Handler:     corsMiddleware(mux),
		ReadTimeout: 30 * time.Second,
		// No WriteTimeout — SSE connections are long-lived and a timeout
		// would close the stream mid-session.
	}

	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("api server: listen on %s: %w", s.srv.Addr, err)
	}

	s.running = true
	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Println("api server error:", err)
		}
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	log.Printf("API server listening on http://127.0.0.1:%d", port)
	return nil
}

// stop gracefully shuts the server down.
func (s *APIServer) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.srv.Shutdown(ctx)
}

// restart stops then starts (used when port changes).
func (s *APIServer) restart() error {
	s.stop()
	return s.start()
}

// ── Auth middleware ────────────────────────────────────────────────────────────

// corsMiddleware sets CORS headers on every response.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// apiAuth wraps a handler with REST API key authentication.
// If APIKey is empty, no auth is required.
func (s *APIServer) apiAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := s.app.config.APIKey
		if key != "" && r.Header.Get("Authorization") != "Bearer "+key {
			jsonError(w, "invalid API key", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// mcpAuth wraps a handler with MCP key authentication.
// If MCPKey is empty, no auth is required.
func (s *APIServer) mcpAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := s.app.config.MCPKey
		if key != "" && r.Header.Get("Authorization") != "Bearer "+key {
			jsonError(w, "invalid MCP key", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// ── Handlers ──────────────────────────────────────────────────────────────────

type searchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type searchResponse struct {
	Results []searchResultItem `json:"results"`
}

type searchResultItem struct {
	Path     string  `json:"path"`
	Score    float64 `json:"score"`
	Size     int64   `json:"size"`
	Modified string  `json:"modified"`
}

func (s *APIServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Query == "" {
		jsonError(w, "missing query", http.StatusBadRequest)
		return
	}
	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 20
	}

	s.app.mu.Lock()
	engine := s.app.engine
	s.app.mu.Unlock()
	if engine == nil {
		jsonError(w, "engine not ready", http.StatusServiceUnavailable)
		return
	}

	raw, err := engine.Search(req.Query)
	if err != nil {
		jsonError(w, "search error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Apply a minimum score threshold to match the quality bar of the UI.
	// Results below 0.15 are typically noise (unrelated files that share incidental tokens).
	const minScore = 0.15
	items := make([]searchResultItem, 0, len(raw))
	for _, r := range raw {
		if len(items) >= req.Limit {
			break
		}
		if r.Score < minScore {
			continue
		}
		items = append(items, searchResultItem{
			Path:     r.Path,
			Score:    float64(r.Score),
			Size:     r.Size,
			Modified: r.Modified,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(searchResponse{Results: items})
}

func (s *APIServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	status := s.app.GetEngineStatus()
	idxStatus := s.app.GetIndexingStatus()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"engine":     status,
		"indexing":   idxStatus.IsIndexing,
		"progress":   idxStatus.Progress,
		"message":    idxStatus.StatusMessage,
	})
}

// handleFeedback records a thumbs-up/down event for a search result.
//
//	POST /feedback
//	{"query":"...", "path":"...", "rank":0, "score":0.87, "feedback":1}
//
// feedback must be +1 (thumbs up), -1 (thumbs down), or 0 (click-through).
func (s *APIServer) handleFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Query    string  `json:"query"`
		Path     string  `json:"path"`
		Rank     int     `json:"rank"`
		Score    float64 `json:"score"`
		Feedback int     `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Query == "" || req.Path == "" {
		jsonError(w, "query and path are required", http.StatusBadRequest)
		return
	}
	if req.Feedback != -1 && req.Feedback != 0 && req.Feedback != 1 {
		jsonError(w, "feedback must be -1, 0, or 1", http.StatusBadRequest)
		return
	}

	s.app.mu.Lock()
	engine := s.app.engine
	s.app.mu.Unlock()
	if engine == nil {
		jsonError(w, "engine not ready", http.StatusServiceUnavailable)
		return
	}

	engine.SubmitFeedback(s.app.sessionID, req.Query, req.Path, req.Rank, req.Score, req.Feedback)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleAdminWeights allows updating ranking weights at runtime without restart.
//
//	POST /admin/weights
//	{"w_recency": 0.5, "w_semantic_text": 1.2}
//
// Only keys present in the request body are updated; others are left unchanged.
func (s *APIServer) handleAdminWeights(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var updates map[string]float64
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil || len(updates) == 0 {
		jsonError(w, "invalid JSON or empty body", http.StatusBadRequest)
		return
	}

	s.app.mu.Lock()
	engine := s.app.engine
	s.app.mu.Unlock()
	if engine == nil {
		jsonError(w, "engine not ready", http.StatusServiceUnavailable)
		return
	}

	// Write each key to search_config. Unknown keys are silently stored —
	// they will be ignored by the weight loader but won't cause harm.
	for k, v := range updates {
		if _, err := engine.GetSQLDB().Exec(
			`INSERT INTO search_config(key, value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
			k, v,
		); err != nil {
			jsonError(w, "db write failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Trigger an immediate reload so the new weights take effect within this request.
	if engine.Weights != nil {
		engine.Weights.ForceReload()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// GenerateAPIKey creates a new random 32-byte hex key, saves it, and returns it.
func GenerateAPIKey() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
