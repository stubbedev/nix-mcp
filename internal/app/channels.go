package app

import (
	"context"
	"encoding/base64"
	"errors"
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
	"unstable": "latest-48-nixos-unstable",
	"stable":   "latest-48-nixos-26.05",
	"26.05":    "latest-48-nixos-26.05",
	"25.11":    "latest-48-nixos-25.11",
	"beta":     "latest-48-nixos-26.05",
}

type channelData struct {
	available map[string]string // index -> "N documents"
	resolved  map[string]string // channel name -> index
}

// channelMemo discovers + resolves NixOS channels, refreshing on the TTL so a
// long-running server picks up new index generations without a restart.
var channelMemo = &memo[channelData]{ttl: cacheTTL, loader: loadChannels}

func loadChannels(ctx context.Context) (channelData, error) {
	available := discoverAvailable(ctx)
	if len(available) == 0 {
		// Don't cache a discovery failure — the next call retries, so a
		// transient blip can't pin the server to fallback channels.
		return channelData{}, errors.New("channel discovery failed")
	}
	return channelData{available: available, resolved: resolveChannels(available)}, nil
}

func discoverAvailable(ctx context.Context) map[string]string {
	aliases, err := discoverChannelAliases(ctx)
	if err != nil {
		return map[string]string{}
	}
	// Probe candidate indices concurrently — sequential _count calls
	// dominated cold-start latency; the result is a map so order is irrelevant.
	type res struct{ pattern, val string }
	ch := make(chan res, len(aliases))
	var wg sync.WaitGroup
	for _, pattern := range aliases {
		safeGo(&wg, func() {
			count, err := esCount(ctx, pattern, map[string]any{"match_all": map[string]any{}})
			if err == nil && count > 0 {
				ch <- res{pattern, comma(count) + " documents"}
			}
		})
	}
	wg.Wait()
	close(ch)
	available := map[string]string{}
	for r := range ch {
		available[r.pattern] = r.val
	}
	return available
}

var channelAliasRE = regexp.MustCompile(`^latest-([0-9]+)-nixos-(unstable|[0-9]+\.[0-9]+)$`)

func discoverChannelAliases(ctx context.Context) ([]string, error) {
	var entries []struct {
		Alias string `json:"alias"`
	}
	if err := getJSON(ctx, nixosAPI+"/_cat/aliases", map[string]string{"format": "json"}, map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(nixosAuth[0]+":"+nixosAuth[1])),
	}, 10*time.Second, &entries); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var aliases []string
	for _, entry := range entries {
		alias := strings.TrimSpace(entry.Alias)
		if channelAliasRE.MatchString(alias) && !seen[alias] {
			aliases = append(aliases, alias)
			seen[alias] = true
		}
	}
	return aliases, nil
}

func resolveChannels(available map[string]string) map[string]string {
	if len(available) == 0 {
		return cloneMap(fallbackChannels)
	}
	type cand struct {
		generation int
		pattern    string
	}
	byChannel := map[string]cand{}
	for pattern := range available {
		m := channelAliasRE.FindStringSubmatch(pattern)
		if m == nil {
			continue
		}
		gen, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		channel := m[2]
		if existing, ok := byChannel[channel]; !ok || gen > existing.generation {
			byChannel[channel] = cand{generation: gen, pattern: pattern}
		}
	}

	resolved := map[string]string{}
	if c, ok := byChannel["unstable"]; ok {
		resolved["unstable"] = c.pattern
	}
	releases := make([]string, 0, len(byChannel))
	for version := range byChannel {
		if version != "unstable" {
			releases = append(releases, version)
		}
	}
	sort.Slice(releases, func(i, j int) bool {
		ai, aj := strings.Split(releases[i], "."), strings.Split(releases[j], ".")
		majorI, _ := strconv.Atoi(ai[0])
		majorJ, _ := strconv.Atoi(aj[0])
		if majorI != majorJ {
			return majorI > majorJ
		}
		minorI, _ := strconv.Atoi(ai[1])
		minorJ, _ := strconv.Atoi(aj[1])
		return minorI > minorJ
	})
	for _, version := range releases {
		resolved[version] = byChannel[version].pattern
	}
	if len(releases) > 0 {
		resolved["stable"] = resolved[releases[0]]
		resolved["beta"] = resolved["stable"]
	}
	if len(resolved) == 0 {
		return cloneMap(fallbackChannels)
	}
	return resolved
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}

func getChannels(ctx context.Context) map[string]string {
	cd, err := channelMemo.get(ctx)
	if err != nil {
		return cloneMap(fallbackChannels) // transient last resort (not cached)
	}
	return cd.resolved
}

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
	cd, err := channelMemo.get(ctx)
	configured, available := cd.resolved, cd.available
	usingFallback := err != nil
	if usingFallback {
		configured, available = cloneMap(fallbackChannels), map[string]string{}
	}

	var results []string
	if usingFallback {
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
