package app

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

const nixdevMaxMDBytes = 200 * 1024

func searchNixdev(ctx context.Context, query string, limit int) string {
	index, err := nixdevCache.get(ctx)
	if err != nil {
		return errCode("API_ERROR", err.Error())
	}
	ql := strings.ToLower(query)
	queryTerms := strings.Fields(ql)

	scores := map[int]int{}
	for _, term := range queryTerms {
		if docIDs, ok := index.Terms[term]; ok {
			for _, id := range docIDs {
				scores[id] += 2
			}
		}
		for it, docIDs := range index.Terms {
			if it != term && strings.Contains(it, term) {
				for _, id := range docIDs {
					scores[id]++
				}
			}
		}
	}
	for i, title := range index.Titles {
		if strings.Contains(strings.ToLower(title), ql) {
			scores[i] += 5
		}
	}
	if len(scores) == 0 {
		return fmt.Sprintf("No nix.dev documentation found matching '%s'", query)
	}

	type ds struct {
		id, score int
	}
	sorted := make([]ds, 0, len(scores))
	for id, sc := range scores {
		sorted = append(sorted, ds{id, sc})
	}
	// Python sorts only by -score; ties keep dict order (nondeterministic). Use
	// id as a stable secondary key for reproducibility.
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].score != sorted[j].score {
			return sorted[i].score > sorted[j].score
		}
		return sorted[i].id < sorted[j].id
	})
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}

	results := []string{fmt.Sprintf("Found %d nix.dev docs matching '%s':\n", len(sorted), query)}
	for _, d := range sorted {
		if d.id < len(index.Titles) && d.id < len(index.Docnames) {
			results = append(results, "* "+index.Titles[d.id])
			results = append(results, "  "+nixdevBaseURL+"/"+index.Docnames[d.id])
			results = append(results, "")
		}
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}

func normalizeNixdevDocname(query string) string {
	name, err := url.QueryUnescape(strings.TrimSpace(query))
	if err != nil {
		name = strings.TrimSpace(query)
	}
	name = strings.TrimPrefix(name, nixdevBaseURL)
	for _, sep := range []string{"#", "?"} {
		if i := strings.Index(name, sep); i >= 0 {
			name = name[:i]
		}
	}
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimSuffix(name, ".html")
	return name
}

func extractNixdevTitle(body, fallback string) string {
	for raw := range strings.SplitSeq(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "# ") {
			if t := strings.TrimSpace(line[2:]); t != "" {
				return t
			}
			return fallback
		}
	}
	return fallback
}

func infoNixdev(ctx context.Context, query string) string {
	if strings.TrimSpace(query) == "" {
		return errMsg("Query required for nix-dev info (docname or URL)")
	}
	docname := normalizeNixdevDocname(query)
	if docname == "" {
		return errMsg("Empty docname after normalization")
	}
	if slices.Contains(strings.Split(docname, "/"), "..") {
		return errMsg("Invalid docname: path traversal not allowed")
	}

	url := nixdevBaseURL + "/_sources/" + docname + ".md"
	canonical := nixdevBaseURL + "/" + docname + ".html"

	status, body, err := httpGet(ctx, url, nil, nil, 15*time.Second)
	if err != nil {
		return errCode(apiErrCode(err), "nix.dev request failed: "+err.Error())
	}
	if status == 404 {
		return errCode("NOT_FOUND", "nix.dev page not found: "+docname)
	}
	if status < 200 || status >= 300 {
		return errCode("API_ERROR", fmt.Sprintf("nix.dev request failed: HTTP %d", status))
	}

	truncated := len(body) > nixdevMaxMDBytes
	if truncated {
		body = body[:nixdevMaxMDBytes]
	}
	text := strings.ToValidUTF8(string(body), "")
	title := extractNixdevTitle(text, docname)

	lines := []string{
		"Title: " + title,
		"Source: " + canonical,
		"Docname: " + docname,
		"",
		strings.TrimRight(text, " \t\r\n"),
	}
	if truncated {
		lines = append(lines, "", "[truncated]")
	}
	return strings.Join(lines, "\n")
}
