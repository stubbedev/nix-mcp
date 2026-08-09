package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stubbedev/nix-mcp/version"
)

// API endpoints (mirrors the Python config.py).
const (
	nixosAPI       = "https://search.nixos.org/backend"
	darwinURL      = "https://nix-darwin.github.io/nix-darwin/manual/index.html"
	flakehubAPI    = "https://api.flakehub.com"
	wikiAPI        = "https://wiki.nixos.org/w/api.php"
	nixdevIndexURL = "https://nix.dev/searchindex.js"
	nixdevBaseURL  = "https://nix.dev"
	noogleAPI      = "https://noogle.dev/api/v1/data"
	nixhubAPI      = "https://search.devbox.sh"
	cacheNixosOrg  = "https://cache.nixos.org"

	maxFileSize      = 1024 * 1024
	defaultLineLimit = 500
	maxLineLimit     = 2000
)

// search.nixos.org public ES credentials (same as the website uses).
var nixosAuth = [2]string{"aWVSALXpZv", "X8gPHnzL52wFEekuxsfQ9cSh"}

func userAgent() string { return "nix-mcp/" + version.Version }

// knownSources distinguishes a source name from a flake directory path.
var knownSources = map[string]bool{
	"nixos": true, "home-manager": true, "darwin": true, "flakes": true,
	"flakehub": true, "nixvim": true, "wiki": true, "nix-dev": true,
	"noogle": true, "nixhub": true,
}

// httpClient has no overall timeout — every request carries its own
// context deadline — but the transport pools connections (so the concurrent
// channel-discovery / cache-check bursts reuse TLS sessions instead of
// re-handshaking per request) and bounds dial/TLS time so a half-open
// connection can never hang a request past its context deadline.
var httpClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

// httpGet performs a GET and returns status + body, with a per-call timeout.
func httpGet(
	ctx context.Context,
	rawURL string,
	params map[string]string,
	headers map[string]string,
	timeout time.Duration,
) (int, []byte, error) {
	if len(params) > 0 {
		q := url.Values{}
		for k, v := range params {
			q.Set(k, v)
		}
		if strings.Contains(rawURL, "?") {
			rawURL += "&" + q.Encode()
		} else {
			rawURL += "?" + q.Encode()
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", userAgent())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024*1024))
	return resp.StatusCode, body, err
}

// getJSON GETs and decodes JSON into out. Non-2xx returns an error.
func getJSON(ctx context.Context, rawURL string, params, headers map[string]string, timeout time.Duration, out any) error {
	status, body, err := httpGet(ctx, rawURL, params, headers, timeout)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("HTTP %d", status)
	}
	return json.Unmarshal(body, out)
}

// ── Elasticsearch (search.nixos.org) ─────────────────────────────────────────

type esHit struct {
	Source map[string]any `json:"_source"`
}

type esResponse struct {
	Hits struct {
		Total struct {
			Value int64 `json:"value"`
		} `json:"total"`
		Hits []esHit `json:"hits"`
	} `json:"hits"`
	Count int64 `json:"count"`
}

func esPost(ctx context.Context, index, action string, payload map[string]any, timeout time.Duration) (*esResponse, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	rawURL := fmt.Sprintf("%s/%s/%s", nixosAPI, index, action)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent())
	req.SetBasicAuth(nixosAuth[0], nixosAuth[1])
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024*1024))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	var out esResponse
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, resp.StatusCode, err
		}
	}
	return &out, resp.StatusCode, nil
}

// esQuery runs a _search and returns the hits (matches base.es_query).
func esQuery(ctx context.Context, index string, query map[string]any, size int) ([]esHit, error) {
	resp, status, err := esPost(ctx, index, "_search", map[string]any{"query": query, "size": size}, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("API error: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("API error: HTTP %d", status)
	}
	return resp.Hits.Hits, nil
}

// esCount runs a _count and returns the document count.
func esCount(ctx context.Context, index string, query map[string]any) (int64, error) {
	resp, status, err := esPost(ctx, index, "_count", map[string]any{"query": query}, 10*time.Second)
	if err != nil {
		return 0, err
	}
	if status < 200 || status >= 300 {
		return 0, fmt.Errorf("HTTP %d", status)
	}
	return resp.Count, nil
}

// src helpers for pulling typed values out of an ES _source map.
func srcStr(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func srcStrList(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{x}
	}
	return nil
}
