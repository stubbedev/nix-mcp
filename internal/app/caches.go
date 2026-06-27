package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// memo is a tiny lazy, once-successful cache: the loader runs until it
// succeeds, then the value is reused for the process lifetime (mirrors the
// Python module-level singleton caches).
type memo[T any] struct {
	mu     sync.Mutex
	ok     bool
	val    T
	loader func(context.Context) (T, error)
}

func (m *memo[T]) get(ctx context.Context) (T, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ok {
		return m.val, nil
	}
	v, err := m.loader(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	m.val, m.ok = v, true
	return v, nil
}

// ── Nixvim options (NuschtOS meta JSON chunks) ───────────────────────────────

var nixvimCache = &memo[[]map[string]any]{loader: loadNixvim}

func loadNixvim(ctx context.Context) ([]map[string]any, error) {
	var all []map[string]any
	for chunk := 0; ; chunk++ {
		url := fmt.Sprintf("%s/%d.json", nixvimMetaBase, chunk)
		status, body, err := httpGet(ctx, url, nil, nil, 30*time.Second)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch Nixvim options: %w", err)
		}
		if status == 404 {
			break
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("failed to fetch Nixvim options: HTTP %d", status)
		}
		var part []map[string]any
		if json.Unmarshal(body, &part) != nil {
			break // unexpected format ends pagination, matching Python
		}
		all = append(all, part...)
	}
	// A malformed chunk ends pagination and we return the pages gathered so far
	// (matching the Python implementation) — the parse error is intentionally
	// not propagated.
	return all, nil //nolint:nilerr // partial pages on a malformed chunk are deliberate
}

// ── Noogle data ──────────────────────────────────────────────────────────────

var noogleCache = &memo[[]map[string]any]{loader: loadNoogle}

func loadNoogle(ctx context.Context) ([]map[string]any, error) {
	status, body, err := httpGet(ctx, noogleAPI, nil, nil, 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Noogle data: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("failed to fetch Noogle data: HTTP %d", status)
	}
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse Noogle data: %w", err)
	}
	return payload.Data, nil
}

// ── nix.dev Sphinx search index ──────────────────────────────────────────────

type nixdevIndex struct {
	Docnames []string         `json:"docnames"`
	Titles   []string         `json:"titles"`
	Terms    map[string][]int `json:"terms"`
}

var nixdevCache = &memo[*nixdevIndex]{loader: loadNixdev}

func loadNixdev(ctx context.Context) (*nixdevIndex, error) {
	status, body, err := httpGet(ctx, nixdevIndexURL, nil, nil, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch nix.dev index: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("failed to fetch nix.dev index: HTTP %d", status)
	}
	content := strings.TrimSpace(string(body))
	const prefix = "Search.setIndex("
	if !strings.HasPrefix(content, prefix) {
		return nil, errors.New("failed to parse nix.dev index: unexpected format")
	}
	jsonStr := strings.TrimSuffix(strings.TrimSpace(content[len(prefix):]), ")")
	// The Sphinx index uses a flexible "terms" shape: doc id list may be an int
	// or a list. Decode loosely, then normalize.
	var raw struct {
		Docnames []string                   `json:"docnames"`
		Titles   []string                   `json:"titles"`
		Terms    map[string]json.RawMessage `json:"terms"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse nix.dev index: %w", err)
	}
	idx := &nixdevIndex{Docnames: raw.Docnames, Titles: raw.Titles, Terms: map[string][]int{}}
	for term, rm := range raw.Terms {
		// The Python implementation only scores terms whose value is a list of
		// doc ids; single-int entries are ignored. Match that.
		var asList []int
		if err := json.Unmarshal(rm, &asList); err == nil {
			idx.Terms[term] = asList
		}
	}
	return idx, nil
}
