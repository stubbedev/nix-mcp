package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func wikiPageURL(title string) string {
	return "https://wiki.nixos.org/wiki/" + url.PathEscape(strings.ReplaceAll(title, " ", "_"))
}

func searchWiki(ctx context.Context, query string, limit int) string {
	normalized := strings.ReplaceAll(query, "-", " ")
	var data struct {
		Query struct {
			Search []struct {
				Title     string `json:"title"`
				Snippet   string `json:"snippet"`
				Wordcount int    `json:"wordcount"`
			} `json:"search"`
		} `json:"query"`
	}
	err := getJSON(ctx, wikiAPI, map[string]string{
		"action": "query", "list": "search", "srsearch": normalized,
		"format": "json", "utf8": "1", "srlimit": strconv.Itoa(limit),
	}, nil, 15*time.Second, &data)
	if err != nil {
		return errCode(apiErrCode(err), "Wiki API error: "+err.Error())
	}
	if len(data.Query.Search) == 0 {
		return fmt.Sprintf("No wiki articles found matching '%s'", query)
	}
	results := []string{fmt.Sprintf("Found %d wiki articles matching '%s':\n", len(data.Query.Search), query)}
	for _, item := range data.Query.Search {
		results = append(results, "* "+item.Title)
		results = append(results, "  "+wikiPageURL(item.Title))
		if snip := stripHTML(item.Snippet); snip != "" {
			results = append(results, "  "+truncate(snip, 200))
		}
		if item.Wordcount > 0 {
			results = append(results, fmt.Sprintf("  (%s words)", comma(item.Wordcount)))
		}
		results = append(results, "")
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}

func infoWiki(ctx context.Context, title string) string {
	var data struct {
		Query struct {
			Pages map[string]json.RawMessage `json:"pages"`
		} `json:"query"`
	}
	err := getJSON(ctx, wikiAPI, map[string]string{
		"action": "query", "titles": title, "prop": "extracts|info",
		"exintro": "1", "explaintext": "1", "format": "json",
	}, nil, 15*time.Second, &data)
	if err != nil {
		return errCode(apiErrCode(err), "Wiki API error: "+err.Error())
	}
	if len(data.Query.Pages) == 0 {
		return errCode("NOT_FOUND", fmt.Sprintf("Wiki page '%s' not found", title))
	}
	var page struct {
		Title   string  `json:"title"`
		Extract string  `json:"extract"`
		Missing *string `json:"missing"`
	}
	for _, raw := range data.Query.Pages {
		_ = json.Unmarshal(raw, &page)
		// MediaWiki returns a "missing" key for absent pages.
		var probe map[string]json.RawMessage
		_ = json.Unmarshal(raw, &probe)
		if _, ok := probe["missing"]; ok {
			return errCode("NOT_FOUND", fmt.Sprintf("Wiki page '%s' not found", title))
		}
		break
	}
	pageTitle := page.Title
	if pageTitle == "" {
		pageTitle = title
	}
	results := []string{
		"Wiki: " + pageTitle,
		"URL: " + wikiPageURL(pageTitle),
		"",
	}
	if extract := page.Extract; extract != "" {
		results = append(results, truncate(extract, 1500))
	}
	return strings.Join(results, "\n")
}
