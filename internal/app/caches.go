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

// cacheTTL is how long discovered data (channel/flake index generations, the
// noogle dataset, the nix.dev index) is reused before a refresh. Long-running
// servers must re-discover periodically: index generations bump ~monthly and a
// stale one 404s. Short enough to self-heal within hours of an upstream change,
// long enough that discovery cost is negligible.
const cacheTTL = 6 * time.Hour

// memo is a lazy cache with a TTL and serve-stale-on-error: after the TTL it
// re-runs the loader, but if that refresh fails it keeps serving the last good
// value rather than breaking a working server on a transient blip. A failure
// with no prior value is returned to the caller (so a fallback can kick in).
type memo[T any] struct {
	mu     sync.Mutex
	ok     bool
	at     time.Time
	val    T
	ttl    time.Duration // 0 = never expire
	loader func(context.Context) (T, error)
}

func (m *memo[T]) get(ctx context.Context) (T, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ok && (m.ttl == 0 || time.Since(m.at) < m.ttl) {
		return m.val, nil
	}
	v, err := m.loader(ctx)
	if err != nil {
		if m.ok {
			return m.val, nil // serve stale rather than fail
		}
		var zero T
		return zero, err
	}
	m.val, m.ok, m.at = v, true, time.Now()
	return m.val, nil
}

// ── Noogle data ──────────────────────────────────────────────────────────────

var noogleCache = &memo[[]map[string]any]{ttl: cacheTTL, loader: loadNoogle}

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

var nixdevCache = &memo[*nixdevIndex]{ttl: cacheTTL, loader: loadNixdev}

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
