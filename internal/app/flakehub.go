package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// apiErrCode maps a transport error to the Python error codes (TIMEOUT vs API_ERROR).
func apiErrCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "TIMEOUT"
	}
	return "API_ERROR"
}

func searchFlakehub(ctx context.Context, query string, limit int) string {
	status, body, err := httpGet(ctx, flakehubAPI+"/search", map[string]string{"q": query},
		map[string]string{"Accept": "application/json"}, 15*time.Second)
	if err != nil {
		return errCode(apiErrCode(err), "FlakeHub API error: "+err.Error())
	}
	if status < 200 || status >= 300 {
		return errCode("API_ERROR", fmt.Sprintf("FlakeHub API error: HTTP %d", status))
	}
	var flakes []struct {
		Org         string   `json:"org"`
		Project     string   `json:"project"`
		Description string   `json:"description"`
		Labels      []string `json:"labels"`
	}
	if err := json.Unmarshal(body, &flakes); err != nil {
		return errMsg(err.Error())
	}
	if len(flakes) == 0 {
		return fmt.Sprintf("No flakes found on FlakeHub matching '%s'", query)
	}
	if len(flakes) > limit {
		flakes = flakes[:limit]
	}

	results := []string{fmt.Sprintf("Found %d flakes on FlakeHub matching '%s':\n", len(flakes), query)}
	for _, f := range flakes {
		results = append(results, fmt.Sprintf("* %s/%s", f.Org, f.Project))
		if f.Description != "" {
			results = append(results, "  "+truncate(strings.Join(strings.Fields(f.Description), " "), 200))
		}
		if len(f.Labels) > 0 {
			n := min(len(f.Labels), 5)
			results = append(results, "  Labels: "+strings.Join(f.Labels[:n], ", "))
		}
		results = append(results, fmt.Sprintf("  https://flakehub.com/flake/%s/%s", f.Org, f.Project))
		results = append(results, "")
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}

func infoFlakehub(ctx context.Context, name string) string {
	if !strings.Contains(name, "/") {
		return errMsg("FlakeHub flake name must be in 'org/project' format (e.g., 'NixOS/nixpkgs')")
	}
	parts := strings.SplitN(name, "/", 2)
	org, project := parts[0], parts[1]

	status, body, err := httpGet(ctx, fmt.Sprintf("%s/version/%s/%s/*", flakehubAPI, org, project), nil,
		map[string]string{"Accept": "application/json"}, 15*time.Second)
	if err != nil {
		return errCode(apiErrCode(err), "FlakeHub API error: "+err.Error())
	}
	if status == 404 {
		return errCode("NOT_FOUND", fmt.Sprintf("Flake '%s' not found on FlakeHub", name))
	}
	if status < 200 || status >= 300 {
		return errCode("API_ERROR", fmt.Sprintf("FlakeHub API error: HTTP %d", status))
	}

	var vi struct {
		Description       string `json:"description"`
		SimplifiedVersion string `json:"simplified_version"`
		Version           string `json:"version"`
		Revision          string `json:"revision"`
		CommitCount       *int64 `json:"commit_count"`
		Visibility        string `json:"visibility"`
		PublishedAt       string `json:"published_at"`
		Mirrored          bool   `json:"mirrored"`
		PrettyDownloadURL string `json:"pretty_download_url"`
		DownloadURL       string `json:"download_url"`
	}
	if err := json.Unmarshal(body, &vi); err != nil {
		return errMsg(err.Error())
	}

	results := []string{fmt.Sprintf("FlakeHub Flake: %s/%s", org, project)}
	if vi.Description != "" {
		results = append(results, "Description: "+vi.Description)
	}
	if v := firstNonEmpty(vi.SimplifiedVersion, vi.Version); v != "" {
		results = append(results, "Latest Version: "+v)
	}
	if vi.Revision != "" {
		results = append(results, "Revision: "+vi.Revision)
	}
	if vi.CommitCount != nil && *vi.CommitCount != 0 {
		results = append(results, "Commits: "+comma(*vi.CommitCount))
	}
	if vi.Visibility != "" {
		results = append(results, "Visibility: "+vi.Visibility)
	}
	if vi.PublishedAt != "" {
		if t, ok := parseISOTime(vi.PublishedAt); ok {
			results = append(results, "Published: "+t.UTC().Format("2006-01-02 15:04")+" UTC")
		}
	}
	if vi.Mirrored {
		results = append(results, "Source: Mirrored from GitHub")
	}
	if dl := firstNonEmpty(vi.PrettyDownloadURL, vi.DownloadURL); dl != "" {
		results = append(results, "Download: "+dl)
	}
	results = append(results, fmt.Sprintf("FlakeHub URL: https://flakehub.com/flake/%s/%s", org, project))
	return strings.Join(results, "\n")
}

func statsFlakehub(ctx context.Context) string {
	status, body, err := httpGet(ctx, flakehubAPI+"/flakes", nil, map[string]string{"Accept": "application/json"}, 15*time.Second)
	if err != nil {
		return errCode(apiErrCode(err), "FlakeHub API error: "+err.Error())
	}
	if status < 200 || status >= 300 {
		return errCode("API_ERROR", fmt.Sprintf("FlakeHub API error: HTTP %d", status))
	}
	var flakes []struct {
		Org    string   `json:"org"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal(body, &flakes); err != nil {
		return errMsg(err.Error())
	}

	orgs := map[string]int{}
	labels := map[string]int{}
	for _, f := range flakes {
		org := f.Org
		if org == "" {
			org = "unknown"
		}
		orgs[org]++
		for _, l := range f.Labels {
			labels[l]++
		}
	}

	results := []string{
		"FlakeHub Statistics:",
		"* Total flakes: " + comma(len(flakes)),
		"* Organizations: " + comma(len(orgs)),
		"* Top organizations:",
	}
	for _, kv := range topN(orgs, 5) {
		results = append(results, fmt.Sprintf("  - %s: %s flakes", kv.key, comma(kv.val)))
	}
	if len(labels) > 0 {
		results = append(results, "* Top labels:")
		for _, kv := range topN(labels, 5) {
			results = append(results, fmt.Sprintf("  - %s: %s flakes", kv.key, comma(kv.val)))
		}
	}
	results = append(results, "\nFlakeHub URL: https://flakehub.com/")
	return strings.Join(results, "\n")
}

// ── small shared helpers for stats ───────────────────────────────────────────

type kv struct {
	key string
	val int
}

// topN returns the n highest-count entries, ties broken by descending count
// then ascending key (deterministic).
func topN(m map[string]int, n int) []kv {
	out := make([]kv, 0, len(m))
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].val != out[j].val {
			return out[i].val > out[j].val
		}
		return out[i].key < out[j].key
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
