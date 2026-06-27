package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type flakeEntry struct {
	name        string
	description string
	owner       string
	repo        string
	url         string
	packages    map[string]bool
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

	searchQuery := map[string]any{"bool": map[string]any{
		"filter": []any{map[string]any{"term": map[string]any{"type": "package"}}},
		"must":   []any{q},
	}}
	resp, status, err := esPost(ctx, flakeIndex, "_search",
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

	order := []string{}
	flakes := map[string]*flakeEntry{}
	add := func(key string, e *flakeEntry) {
		if _, ok := flakes[key]; !ok {
			flakes[key] = e
			order = append(order, key)
		}
	}

	for _, hit := range hits {
		src := hit.Source
		flakeName := strings.TrimSpace(srcStr(src, "flake_name"))
		packagePname := srcStr(src, "package_pname")
		resolved, _ := src["flake_resolved"].(map[string]any)

		if flakeName == "" && packagePname == "" {
			continue
		}

		mapStr := func(m map[string]any, k string) string {
			if m == nil {
				return ""
			}
			if s, ok := m[k].(string); ok {
				return s
			}
			return ""
		}
		owner := mapStr(resolved, "owner")
		repo := mapStr(resolved, "repo")
		urlv := mapStr(resolved, "url")
		attrName := srcStr(src, "package_attr_name")

		if resolved != nil && (owner != "" || repo != "" || urlv != "") {
			var flakeKey, displayName string
			if owner != "" && repo != "" {
				flakeKey = owner + "/" + repo
				displayName = firstNonEmpty(flakeName, repo, packagePname)
			} else if urlv != "" {
				flakeKey = urlv
				base := strings.TrimSuffix(strings.TrimRight(urlv, "/"), ".git")
				if i := strings.LastIndex(base, "/"); i >= 0 {
					base = base[i+1:]
				}
				displayName = firstNonEmpty(flakeName, base, packagePname)
			} else {
				flakeKey = firstNonEmpty(flakeName, packagePname)
				displayName = flakeKey
			}
			desc := firstNonEmpty(srcStr(src, "flake_description"), srcStr(src, "package_description"))
			add(
				flakeKey,
				&flakeEntry{name: displayName, description: desc, owner: owner, repo: repo, url: urlv, packages: map[string]bool{}},
			)
			if attrName != "" {
				flakes[flakeKey].packages[attrName] = true
			}
		} else if flakeName != "" {
			desc := firstNonEmpty(srcStr(src, "flake_description"), srcStr(src, "package_description"))
			add(flakeName, &flakeEntry{name: flakeName, description: desc, packages: map[string]bool{}})
			if attrName != "" {
				flakes[flakeName].packages[attrName] = true
			}
		}
	}

	var results []string
	if total > int64(len(flakes)) {
		results = append(results, fmt.Sprintf("Found %s matches (%d unique flakes) for '%s':\n", comma(total), len(flakes), query))
	} else {
		results = append(results, fmt.Sprintf("Found %d flakes matching '%s':\n", len(flakes), query))
	}

	for _, key := range order {
		f := flakes[key]
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
	total, err := esCount(ctx, flakeIndex, map[string]any{"term": map[string]any{"type": "package"}}, 10*time.Second)
	if err != nil {
		return errMsg("Flake indices not found")
	}
	return fmt.Sprintf("NixOS Flakes Statistics:\n* Available packages: %s", comma(total))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
