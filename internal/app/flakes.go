package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// flakeIndexCache discovers the current flake search index. The generation
// number in `latest-<N>-group-manual` bumps whenever search.nixos.org changes
// its schema (it was 44, is 48 as of this writing), so a hardcoded value goes
// stale — probe a range once and pin the highest live generation.
var flakeIndexCache = &memo[string]{loader: discoverFlakeIndex}

func discoverFlakeIndex(ctx context.Context) (string, error) {
	const lo, hi = 44, 64
	var mu sync.Mutex
	best := -1
	var wg sync.WaitGroup
	for n := lo; n <= hi; n++ {
		idx := fmt.Sprintf("latest-%d-group-manual", n)
		safeGo(&wg, func() {
			if c, err := esCount(ctx, idx, map[string]any{"term": map[string]any{"type": "package"}}); err == nil && c > 0 {
				mu.Lock()
				if n > best {
					best = n
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if best < 0 {
		return "", errors.New("no flake index found")
	}
	return fmt.Sprintf("latest-%d-group-manual", best), nil
}

type flakeEntry struct {
	name        string
	description string
	owner       string
	repo        string
	url         string
	packages    map[string]bool
}

// flakeCollector de-duplicates flake hits while preserving first-seen order.
type flakeCollector struct {
	order  []string
	flakes map[string]*flakeEntry
}

func (c *flakeCollector) add(key string, e *flakeEntry) *flakeEntry {
	if _, ok := c.flakes[key]; !ok {
		c.flakes[key] = e
		c.order = append(c.order, key)
	}
	return c.flakes[key]
}

// addHit folds one ES hit into the collector (matches the Python dedup logic:
// key by owner/repo, else url, else flake name).
func (c *flakeCollector) addHit(src map[string]any) {
	flakeName := strings.TrimSpace(srcStr(src, "flake_name"))
	packagePname := srcStr(src, "package_pname")
	if flakeName == "" && packagePname == "" {
		return
	}
	resolved, _ := src["flake_resolved"].(map[string]any)
	owner, repo, urlv := srcStr(resolved, "owner"), srcStr(resolved, "repo"), srcStr(resolved, "url")
	desc := firstNonEmpty(srcStr(src, "flake_description"), srcStr(src, "package_description"))
	attrName := srcStr(src, "package_attr_name")

	var entry *flakeEntry
	switch {
	case resolved != nil && (owner != "" || repo != "" || urlv != ""):
		key, displayName := flakeKeyAndName(flakeName, packagePname, owner, repo, urlv)
		entry = c.add(
			key,
			&flakeEntry{name: displayName, description: desc, owner: owner, repo: repo, url: urlv, packages: map[string]bool{}},
		)
	case flakeName != "":
		entry = c.add(flakeName, &flakeEntry{name: flakeName, description: desc, packages: map[string]bool{}})
	default:
		return
	}
	if attrName != "" {
		entry.packages[attrName] = true
	}
}

func flakeKeyAndName(flakeName, packagePname, owner, repo, urlv string) (key, displayName string) {
	switch {
	case owner != "" && repo != "":
		return owner + "/" + repo, firstNonEmpty(flakeName, repo, packagePname)
	case urlv != "":
		base := strings.TrimSuffix(strings.TrimRight(urlv, "/"), ".git")
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		return urlv, firstNonEmpty(flakeName, base, packagePname)
	default:
		k := firstNonEmpty(flakeName, packagePname)
		return k, k
	}
}

func searchFlakes(ctx context.Context, query string, limit int) string {
	var q map[string]any
	if strings.TrimSpace(query) == "" || query == "*" {
		q = map[string]any{"match_all": map[string]any{}}
	} else {
		q = map[string]any{"bool": map[string]any{
			"should": []any{
				map[string]any{"match": map[string]any{"flake_name": map[string]any{"query": query, "boost": 3}}},
				map[string]any{"match": map[string]any{"flake_description": map[string]any{"query": query, "boost": 2}}},
				map[string]any{"match": map[string]any{"package_pname": map[string]any{"query": query, "boost": 1.5}}},
				map[string]any{"match": map[string]any{"package_description": query}},
				map[string]any{"wildcard": map[string]any{"flake_name": map[string]any{"value": "*" + query + "*", "boost": 2.5}}},
				map[string]any{"wildcard": map[string]any{"package_pname": map[string]any{"value": "*" + query + "*", "boost": 1}}},
				map[string]any{"prefix": map[string]any{"flake_name": map[string]any{"value": query, "boost": 2}}},
			},
			"minimum_should_match": 1,
		}}
	}

	idx, err := flakeIndexCache.get(ctx)
	if err != nil {
		return errMsg("Flake indices not found. Flake search may be temporarily unavailable.")
	}

	searchQuery := map[string]any{"bool": map[string]any{
		"filter": []any{map[string]any{"term": map[string]any{"type": "package"}}},
		"must":   []any{q},
	}}
	resp, status, err := esPost(ctx, idx, "_search",
		map[string]any{"query": searchQuery, "size": limit * 5, "track_total_hits": true}, 10*time.Second)
	if err != nil {
		return errMsg(err.Error())
	}
	if status == 404 {
		return errMsg("Flake indices not found. Flake search may be temporarily unavailable.")
	}
	if status < 200 || status >= 300 {
		return errMsg(fmt.Sprintf("API error: HTTP %d", status))
	}

	hits := resp.Hits.Hits
	total := resp.Hits.Total.Value
	if len(hits) == 0 {
		return fmt.Sprintf("No flakes found matching '%s'", query)
	}

	c := &flakeCollector{flakes: map[string]*flakeEntry{}}
	for _, hit := range hits {
		c.addHit(hit.Source)
	}

	var results []string
	if total > int64(len(c.flakes)) {
		results = append(results, fmt.Sprintf("Found %s matches (%d unique flakes) for '%s':\n", comma(total), len(c.flakes), query))
	} else {
		results = append(results, fmt.Sprintf("Found %d flakes matching '%s':\n", len(c.flakes), query))
	}

	for _, key := range c.order {
		f := c.flakes[key]
		results = append(results, "* "+f.name)
		if f.owner != "" && f.repo != "" {
			results = append(results, "  Repository: "+f.owner+"/"+f.repo)
		} else if f.url != "" {
			results = append(results, "  URL: "+f.url)
		}
		if f.description != "" {
			results = append(results, "  "+truncate(f.description, 200))
		}
		if len(f.packages) > 0 {
			pkgs := make([]string, 0, len(f.packages))
			for p := range f.packages {
				pkgs = append(pkgs, p)
			}
			sort.Strings(pkgs)
			if len(pkgs) > 5 {
				results = append(results, fmt.Sprintf("  Packages: %s, ... (%d total)", strings.Join(pkgs[:5], ", "), len(pkgs)))
			} else {
				results = append(results, "  Packages: "+strings.Join(pkgs, ", "))
			}
		}
		results = append(results, "")
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}

func statsFlakes(ctx context.Context) string {
	idx, err := flakeIndexCache.get(ctx)
	if err != nil {
		return errMsg("Flake indices not found")
	}
	total, err := esCount(ctx, idx, map[string]any{"term": map[string]any{"type": "package"}})
	if err != nil {
		return errMsg("Flake indices not found")
	}
	return "NixOS Flakes Statistics:\n* Available packages: " + comma(total)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
