package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var renderedHTMLTag = regexp.MustCompile(`<[^>]+>`)

func cleanOptionDesc(desc string) string {
	if strings.Contains(desc, "<rendered-html>") {
		desc = strings.ReplaceAll(desc, "<rendered-html>", "")
		desc = strings.ReplaceAll(desc, "</rendered-html>", "")
		desc = strings.TrimSpace(renderedHTMLTag.ReplaceAllString(desc, ""))
	}
	return desc
}

func searchNixOS(ctx context.Context, query, searchType string, limit int, channel string) string {
	if searchType == "flakes" {
		return searchFlakes(ctx, query, limit)
	}

	channels := getChannels(ctx)
	index, ok := channels[channel]
	if !ok {
		return errMsg(fmt.Sprintf("Invalid channel '%s'. %s", channel, channelSuggestions(ctx, channel)))
	}

	var q map[string]any
	switch searchType {
	case "packages":
		pnameQuery := query
		if i := strings.LastIndex(query, "."); i >= 0 {
			pnameQuery = query[i+1:]
		}
		q = map[string]any{"bool": map[string]any{
			"must": []any{map[string]any{"term": map[string]any{"type": "package"}}},
			"should": []any{
				map[string]any{"match": map[string]any{"package_pname": map[string]any{"query": pnameQuery, "boost": 3}}},
				map[string]any{"match": map[string]any{"package_attr_name": map[string]any{"query": query, "boost": 2}}},
				map[string]any{"match": map[string]any{"package_description": pnameQuery}},
			},
			"minimum_should_match": 1,
		}}
	case "options":
		q = map[string]any{"bool": map[string]any{
			"must": []any{map[string]any{"term": map[string]any{"type": "option"}}},
			"should": []any{
				map[string]any{"wildcard": map[string]any{"option_name": "*" + query + "*"}},
				map[string]any{"match": map[string]any{"option_description": query}},
			},
			"minimum_should_match": 1,
		}}
	default: // programs
		q = map[string]any{"bool": map[string]any{
			"must": []any{map[string]any{"term": map[string]any{"type": "package"}}},
			"should": []any{
				map[string]any{"match": map[string]any{"package_programs": map[string]any{"query": query, "boost": 2}}},
				map[string]any{"match": map[string]any{"package_pname": query}},
			},
			"minimum_should_match": 1,
		}}
	}

	hits, err := esQuery(ctx, index, q, limit)
	if err != nil {
		return errMsg(err.Error())
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No %s found matching '%s'", searchType, query)
	}

	results := []string{fmt.Sprintf("Found %d %s matching '%s':\n", len(hits), searchType, query)}
	for _, hit := range hits {
		src := hit.Source
		switch searchType {
		case "packages":
			name := srcStr(src, "package_pname")
			attr := srcStr(src, "package_attr_name")
			version := srcStr(src, "package_pversion")
			desc := srcStr(src, "package_description")
			display := name
			if attr != "" && attr != name {
				display = attr
			}
			results = append(results, fmt.Sprintf("* %s (%s)", display, version))
			if desc != "" {
				results = append(results, "  "+desc)
			}
			results = append(results, "")
		case "options":
			name := srcStr(src, "option_name")
			optType := srcStr(src, "option_type")
			desc := cleanOptionDesc(srcStr(src, "option_description"))
			results = append(results, "* "+name)
			if optType != "" {
				results = append(results, "  Type: "+optType)
			}
			if desc != "" {
				results = append(results, "  "+desc)
			}
			results = append(results, "")
		default: // programs
			pkgName := srcStr(src, "package_pname")
			ql := strings.ToLower(query)
			for _, prog := range srcStrList(src, "package_programs") {
				if strings.ToLower(prog) == ql {
					results = append(results, fmt.Sprintf("* %s (provided by %s)", prog, pkgName))
					results = append(results, "")
				}
			}
		}
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}

func infoNixOS(ctx context.Context, name, infoType, channel string) string {
	channels := getChannels(ctx)
	index, ok := channels[channel]
	if !ok {
		return errMsg(fmt.Sprintf("Invalid channel '%s'. %s", channel, channelSuggestions(ctx, channel)))
	}

	var hits []esHit
	var err error
	matchedVia := ""
	var pnameCandidates []esHit

	if infoType == "package" {
		attrQuery := map[string]any{"bool": map[string]any{"must": []any{
			map[string]any{"term": map[string]any{"type": "package"}},
			map[string]any{"term": map[string]any{"package_attr_name": name}},
		}}}
		hits, err = esQuery(ctx, index, attrQuery, 1)
		if err != nil {
			return errMsg(err.Error())
		}
		if len(hits) > 0 {
			matchedVia = "attribute"
		} else {
			pnameQuery := map[string]any{"bool": map[string]any{"must": []any{
				map[string]any{"term": map[string]any{"type": "package"}},
				map[string]any{"term": map[string]any{"package_pname": name}},
			}}}
			pnameCandidates, err = esQuery(ctx, index, pnameQuery, 5)
			if err != nil {
				return errMsg(err.Error())
			}
			if len(pnameCandidates) > 0 {
				var canonical []esHit
				for _, h := range pnameCandidates {
					if srcStr(h.Source, "package_attr_name") == srcStr(h.Source, "package_pname") {
						canonical = append(canonical, h)
					}
				}
				var chosen esHit
				if len(canonical) > 0 {
					chosen = canonical[0]
				} else {
					sorted := append([]esHit(nil), pnameCandidates...)
					sort.Slice(sorted, func(i, j int) bool {
						ai, aj := srcStr(sorted[i].Source, "package_attr_name"), srcStr(sorted[j].Source, "package_attr_name")
						if ai != aj {
							return ai < aj
						}
						return srcStr(sorted[i].Source, "package_pname") < srcStr(sorted[j].Source, "package_pname")
					})
					chosen = sorted[0]
				}
				hits = []esHit{chosen}
				matchedVia = "pname"
			}
		}
	} else {
		query := map[string]any{"bool": map[string]any{"must": []any{
			map[string]any{"term": map[string]any{"type": "option"}},
			map[string]any{"term": map[string]any{"option_name": name}},
		}}}
		hits, err = esQuery(ctx, index, query, 1)
		if err != nil {
			return errMsg(err.Error())
		}
	}

	if len(hits) == 0 {
		return errCode("NOT_FOUND", fmt.Sprintf("%s '%s' not found", capitalize(infoType), name))
	}

	src := hits[0].Source
	if infoType == "package" {
		attr := srcStr(src, "package_attr_name")
		pname := srcStr(src, "package_pname")
		info := []string{"Package: " + pname}
		if attr != "" && attr != pname {
			info = append(info, "Attribute: "+attr)
		}
		info = append(info, "Version: "+srcStr(src, "package_pversion"))
		if desc := srcStr(src, "package_description"); desc != "" {
			info = append(info, "Description: "+desc)
		}
		if hp := srcStrList(src, "package_homepage"); len(hp) > 0 {
			info = append(info, "Homepage: "+hp[0])
		} else if hp := srcStr(src, "package_homepage"); hp != "" {
			info = append(info, "Homepage: "+hp)
		}
		if lic := srcStrList(src, "package_license_set"); len(lic) > 0 {
			info = append(info, "License: "+strings.Join(lic, ", "))
		}
		if matchedVia == "pname" && len(pnameCandidates) > 1 {
			altSet := map[string]bool{}
			for _, h := range pnameCandidates {
				if a := srcStr(h.Source, "package_attr_name"); a != "" {
					altSet[a] = true
				}
			}
			chosenAttr := attr
			if chosenAttr == "" {
				chosenAttr = pname
			}
			var others []string
			for a := range altSet {
				if a != chosenAttr {
					others = append(others, a)
				}
			}
			sort.Strings(others)
			if len(others) > 0 {
				pickedCanonical := false
				for _, h := range pnameCandidates {
					if srcStr(h.Source, "package_attr_name") == srcStr(h.Source, "package_pname") &&
						srcStr(h.Source, "package_pname") == name {
						pickedCanonical = true
						break
					}
				}
				chosenLabel := "a representative entry"
				if pickedCanonical {
					chosenLabel = "the canonical entry"
				}
				retry, _ := json.Marshal(map[string]any{"action": "info", "query": others[0], "channel": channel})
				info = append(info, "")
				info = append(info, fmt.Sprintf(
					"Note: '%s' is a pname shared by multiple packages. Returned %s (%s). "+
						"Other attributes with the same pname: %s. Pass an exact attribute to disambiguate, e.g. %s.",
					name, chosenLabel, chosenAttr, strings.Join(others, ", "), string(retry)))
			}
		}
		return strings.Join(info, "\n")
	}

	info := []string{"Option: " + srcStr(src, "option_name")}
	if optType := srcStr(src, "option_type"); optType != "" {
		info = append(info, "Type: "+optType)
	}
	if desc := cleanOptionDesc(srcStr(src, "option_description")); desc != "" {
		info = append(info, "Description: "+desc)
	}
	if def := srcStr(src, "option_default"); def != "" {
		info = append(info, "Default: "+def)
	}
	if ex := srcStr(src, "option_example"); ex != "" {
		info = append(info, "Example: "+ex)
	}
	return strings.Join(info, "\n")
}

func statsNixOS(ctx context.Context, channel string) string {
	channels := getChannels(ctx)
	index, ok := channels[channel]
	if !ok {
		return errMsg(fmt.Sprintf("Invalid channel '%s'. %s", channel, channelSuggestions(ctx, channel)))
	}
	pkgCount, _ := esCount(ctx, index, map[string]any{"term": map[string]any{"type": "package"}}, 10*time.Second)
	optCount, _ := esCount(ctx, index, map[string]any{"term": map[string]any{"type": "option"}}, 10*time.Second)
	if pkgCount == 0 && optCount == 0 {
		return errMsg("Failed to retrieve statistics")
	}
	return fmt.Sprintf("NixOS Statistics (%s):\n* Packages: %s\n* Options: %s", channel, comma(pkgCount), comma(optCount))
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
