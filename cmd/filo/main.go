// filo — CLI client for the Filosophy search API.
//
// Usage:
//
//	filo search "books by camus"
//	filo status
//
// Configuration (in order of precedence):
//
//	--key / FILOSOPHY_API_KEY env var
//	--port (default 7700)
//	--host (default 127.0.0.1)
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultHost = "127.0.0.1"
const defaultPort = 7700

var (
	host  string
	port  int
	key   string
	limit int
	jsonOut bool
)

func init() {
	flag.StringVar(&host, "host", defaultHost, "API host")
	flag.IntVar(&port, "port", defaultPort, "API port")
	flag.StringVar(&key, "key", "", "API key (or set FILOSOPHY_API_KEY)")
	flag.IntVar(&limit, "limit", 10, "max results (search only)")
	flag.BoolVar(&jsonOut, "json", false, "output raw JSON")
	flag.Usage = usage
}

func main() {
	flag.Parse()

	if key == "" {
		key = os.Getenv("FILOSOPHY_API_KEY")
	}

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	cmd := args[0]
	switch cmd {
	case "search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: filo search <query>")
			os.Exit(1)
		}
		query := strings.Join(args[1:], " ")
		runSearch(query)
	case "status":
		runStatus()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Filosophy CLI — semantic file search")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  filo [flags] search <query>")
	fmt.Fprintln(os.Stderr, "  filo [flags] status")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags:")
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Environment:")
	fmt.Fprintln(os.Stderr, "  FILOSOPHY_API_KEY   API key (same as --key)")
}

func baseURL() string {
	return fmt.Sprintf("http://%s:%d", host, port)
}

func doRequest(method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, baseURL()+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	return client.Do(req)
}

// ── search ─────────────────────────────────────────────────────────────────

type searchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type searchResult struct {
	Path     string  `json:"path"`
	Score    float64 `json:"score"`
	Size     int64   `json:"size"`
	Modified string  `json:"modified"`
}

type searchResponse struct {
	Results []searchResult `json:"results"`
}

func runSearch(query string) {
	resp, err := doRequest("POST", "/search", searchRequest{Query: query, Limit: limit})
	if err != nil {
		fatal("request failed: %v\n\nIs Filosophy running with the API enabled?", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		handleError(resp.StatusCode, raw)
	}

	if jsonOut {
		fmt.Println(string(raw))
		return
	}

	var result searchResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		fatal("failed to parse response: %v", err)
	}

	if len(result.Results) == 0 {
		fmt.Println("No results found.")
		return
	}

	fmt.Printf("Found %d result(s) for %q:\n\n", len(result.Results), query)
	for i, r := range result.Results {
		score := fmt.Sprintf("%.0f%%", r.Score*100)
		size := formatSize(r.Size)
		fmt.Printf("  %2d. [%s] %s\n      %s  %s\n", i+1, score, r.Path, size, r.Modified)
	}
}

// ── status ──────────────────────────────────────────────────────────────────

func runStatus() {
	resp, err := doRequest("GET", "/status", nil)
	if err != nil {
		fatal("request failed: %v\n\nIs Filosophy running with the API enabled?", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		handleError(resp.StatusCode, raw)
	}

	if jsonOut {
		fmt.Println(string(raw))
		return
	}

	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		fatal("failed to parse response: %v", err)
	}

	engine := fmt.Sprint(s["engine"])
	indexing := s["indexing"] == true
	progress := 0.0
	if p, ok := s["progress"].(float64); ok {
		progress = p
	}
	msg := fmt.Sprint(s["message"])

	fmt.Printf("Engine:   %s\n", engine)
	if indexing {
		fmt.Printf("Indexing: yes (%.0f%%)\n", progress*100)
		if msg != "" && msg != "<nil>" {
			fmt.Printf("Message:  %s\n", msg)
		}
	} else {
		fmt.Println("Indexing: idle")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func handleError(code int, body []byte) {
	var e map[string]string
	if json.Unmarshal(body, &e) == nil {
		if msg, ok := e["error"]; ok {
			if code == http.StatusUnauthorized {
				fatal("unauthorized: %s\n\nSet --key or FILOSOPHY_API_KEY", msg)
			}
			fatal("API error (%d): %s", code, msg)
		}
	}
	fatal("API error (%d): %s", code, string(body))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	default:
		return strconv.FormatInt(bytes, 10) + " B"
	}
}
