package app

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// fallbackChannels are used when API discovery fails (mirrors FALLBACK_CHANNELS).
var fallbackChannels = map[string]string{
	"unstable": "latest-44-nixos-unstable",
	"stable":   "latest-44-nixos-25.11",
	"25.05":    "latest-44-nixos-25.05",
	"25.11":    "latest-44-nixos-25.11",
	"beta":     "latest-44-nixos-25.11",
}

type chanCache struct {
	mu            sync.Mutex
	available     map[string]string // index -> "N documents"
	resolved      map[string]string // channel name -> index
	usingFallback bool
}

var channelCache = &chanCache{}

func (c *chanCache) getAvailable(ctx context.Context) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.available == nil {
		c.available = discoverAvailable(ctx)
	}
	return c.available
}

func (c *chanCache) getResolved(ctx context.Context) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.resolved != nil {
		return c.resolved
	}
	// getAvailable below re-locks; release first.
	c.mu.Unlock()
	avail := c.getAvailable(ctx)
	c.mu.Lock()
	c.resolved = resolveChannels(avail, &c.usingFallback)
	return c.resolved
}

func discoverAvailable(ctx context.Context) map[string]string {
	generations := []int{43, 44, 45, 46}
	versions := []string{"unstable", "25.05", "25.11", "26.05", "26.11"}
	// Probe all 20 candidate indices concurrently — sequential _count calls
	// dominated cold-start latency; the result is a map so order is irrelevant.
	type res struct{ pattern, val string }
	ch := make(chan res, len(generations)*len(versions))
	var wg sync.WaitGroup
	for _, gen := range generations {
		for _, v := range versions {
			pattern := fmt.Sprintf("latest-%d-nixos-%s", gen, v)
			safeGo(&wg, func() {
				count, err := esCount(ctx, pattern, map[string]any{"match_all": map[string]any{}})
				if err == nil && count > 0 {
					ch <- res{pattern, comma(count) + " documents"}
				}
			})
		}
	}
	wg.Wait()
	close(ch)
	available := map[string]string{}
	for r := range ch {
		available[r.pattern] = r.val
	}
	return available
}

func resolveChannels(available map[string]string, usingFallback *bool) map[string]string {
	if len(available) == 0 {
		*usingFallback = true
		return cloneMap(fallbackChannels)
	}
	resolved := map[string]string{}
	for pattern := range available {
		if strings.Contains(pattern, "unstable") {
			resolved["unstable"] = pattern
			break
		}
	}

	type cand struct {
		major, minor int
		version      string
		pattern      string
		count        int
	}
	var candidates []cand
	for pattern, countStr := range available {
		if strings.Contains(pattern, "unstable") {
			continue
		}
		parts := strings.Split(pattern, "-")
		if len(parts) < 4 {
			continue
		}
		version := parts[3]
		mm := strings.Split(version, ".")
		if len(mm) != 2 {
			continue
		}
		major, err1 := strconv.Atoi(mm[0])
		minor, err2 := strconv.Atoi(mm[1])
		count, err3 := strconv.Atoi(strings.ReplaceAll(strings.ReplaceAll(countStr, ",", ""), " documents", ""))
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		candidates = append(candidates, cand{major, minor, version, pattern, count})
	}

	if len(candidates) > 0 {
		sort.Slice(candidates, func(i, j int) bool {
			a, b := candidates[i], candidates[j]
			if a.major != b.major {
				return a.major > b.major
			}
			if a.minor != b.minor {
				return a.minor > b.minor
			}
			return a.count > b.count
		})
		cur := candidates[0]
		resolved["stable"] = cur.pattern
		resolved[cur.version] = cur.pattern

		best := map[string]cand{}
		for _, c := range candidates {
			if e, ok := best[c.version]; !ok || c.count > e.count {
				best[c.version] = c
			}
		}
		for version, c := range best {
			resolved[version] = c.pattern
		}
	}

	if s, ok := resolved["stable"]; ok {
		resolved["beta"] = s
	}
	if len(resolved) == 0 {
		*usingFallback = true
		return cloneMap(fallbackChannels)
	}
	return resolved
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}

func getChannels(ctx context.Context) map[string]string { return channelCache.getResolved(ctx) }

func channelSuggestions(ctx context.Context, invalid string) string {
	channels := getChannels(ctx)
	available := make([]string, 0, len(channels))
	for k := range channels {
		available = append(available, k)
	}
	sort.Strings(available)
	var suggestions []string
	inv := strings.ToLower(invalid)
	for _, ch := range available {
		l := strings.ToLower(ch)
		if strings.Contains(l, inv) || strings.Contains(inv, l) {
			suggestions = append(suggestions, ch)
		}
	}
	if len(suggestions) == 0 {
		common := []string{"unstable", "stable", "beta"}
		for _, ch := range available {
			if strings.Contains(ch, ".") && isDigits(strings.ReplaceAll(ch, ".", "")) {
				common = append(common, ch)
			}
		}
		set := map[string]bool{}
		for _, ch := range available {
			set[ch] = true
		}
		for _, ch := range common {
			if set[ch] {
				suggestions = append(suggestions, ch)
			}
		}
		if len(suggestions) == 0 {
			if len(available) > 4 {
				suggestions = available[:4]
			} else {
				suggestions = available
			}
		}
	}
	return "Available channels: " + strings.Join(suggestions, ", ")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ── channel revision (commit provenance) ─────────────────────────────────────

var commitInIndex = regexp.MustCompile(`-([0-9a-f]{40})$`)

var (
	branchRevMu sync.Mutex
	branchRevs  = map[string]struct {
		sha string
		at  time.Time
	}{}
	branchRevTTL = 10 * time.Minute
)

func channelToBranch(name, index string) string {
	if strings.Contains(name, "unstable") || strings.Contains(index, "unstable") {
		return "nixos-unstable"
	}
	if name == "stable" || name == "beta" {
		parts := strings.Split(index, "-")
		if len(parts) >= 4 && regexp.MustCompile(`^\d+\.\d+$`).MatchString(parts[3]) {
			return "nixos-" + parts[3]
		}
		return ""
	}
	if regexp.MustCompile(`^\d+\.\d+$`).MatchString(name) {
		return "nixos-" + name
	}
	return ""
}

// channelRevision returns (sha, source) where source is "indexed", "branch_head", or "".
func channelRevision(ctx context.Context, name, index string) (string, string) {
	if m := commitInIndex.FindStringSubmatch(index); m != nil {
		return m[1], "indexed"
	}
	branch := channelToBranch(name, index)
	if branch == "" {
		return "", ""
	}
	branchRevMu.Lock()
	cached, ok := branchRevs[branch]
	branchRevMu.Unlock()
	if ok && time.Since(cached.at) < branchRevTTL {
		return cached.sha, "branch_head"
	}

	var data struct {
		SHA string `json:"sha"`
	}
	err := getJSON(ctx,
		"https://api.github.com/repos/NixOS/nixpkgs/commits/"+branch,
		nil,
		map[string]string{"Accept": "application/vnd.github+json"},
		4*time.Second, &data)
	if err == nil && data.SHA != "" {
		branchRevMu.Lock()
		branchRevs[branch] = struct {
			sha string
			at  time.Time
		}{data.SHA, time.Now()}
		branchRevMu.Unlock()
		return data.SHA, "branch_head"
	}
	if ok {
		return cached.sha, "branch_head"
	}
	return "", ""
}

func listChannels(ctx context.Context) string {
	configured := getChannels(ctx)
	available := channelCache.getAvailable(ctx)

	var results []string
	if channelCache.usingFallback {
		results = append(results, "WARNING: Using fallback channels (API discovery failed)\n")
	}
	results = append(results, "NixOS Channels:\n")

	names := make([]string, 0, len(configured))
	for k := range configured {
		names = append(names, k)
	}
	sort.Strings(names)

	for _, name := range names {
		index := configured[name]
		status := "Unavailable"
		docCount := "Unknown"
		if dc, ok := available[index]; ok {
			status = "Available"
			docCount = dc
		}
		label := "* " + name
		if name == "stable" {
			parts := strings.Split(index, "-")
			if len(parts) >= 4 {
				label = fmt.Sprintf("* %s (current: %s)", name, parts[3])
			}
		}
		results = append(results, fmt.Sprintf("%s -> %s", label, index))
		results = append(results, fmt.Sprintf("  Status: %s (%s)", status, docCount))
		rev, source := channelRevision(ctx, name, index)
		branch := channelToBranch(name, index)
		if branch != "" {
			results = append(results, "  Branch: "+branch)
		}
		if rev != "" && source == "indexed" {
			results = append(results, "  Revision (indexed): "+rev)
		} else if rev != "" && source == "branch_head" {
			results = append(results, "  Branch HEAD: "+rev+" (upstream; may be ahead of indexed data)")
		}
		results = append(results, "")
	}

	results = append(results,
		"Note: 'stable' always points to current stable release. "+
			"'Revision (indexed)' is the exact commit the search index was built from "+
			"(safe to compare against `nix_versions`). 'Branch HEAD' is the upstream "+
			"branch tip, fetched best-effort from GitHub and cached for up to "+
			"10 minutes per process — it may be a few commits ahead of the "+
			"indexed data or a few minutes stale from the upstream ref.")
	return strings.TrimSpace(strings.Join(results, "\n"))
}
