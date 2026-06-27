package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

func msFirstStr(m map[string]any, key string) string {
	if v, ok := m[key].([]any); ok && len(v) > 0 {
		if s, ok := v[0].(string); ok {
			return s
		}
	}
	return ""
}

func searchNixvim(ctx context.Context, query string, limit int) string {
	options, err := nixvimCache.get(ctx)
	if err != nil {
		return errCode("API_ERROR", err.Error())
	}
	ql := strings.ToLower(query)
	type match struct{ name, typ, desc string }
	var matches []match
	for _, opt := range options {
		name := srcStr(opt, "name")
		desc := stripHTML(srcStr(opt, "description"))
		if strings.Contains(strings.ToLower(name), ql) || strings.Contains(strings.ToLower(desc), ql) {
			matches = append(matches, match{name, srcStr(opt, "type"), desc})
			if len(matches) >= limit {
				break
			}
		}
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No Nixvim options found matching '%s'", query)
	}
	results := []string{fmt.Sprintf("Found %d Nixvim options matching '%s':\n", len(matches), query)}
	for _, opt := range matches {
		results = append(results, "* "+opt.name)
		if opt.typ != "" {
			results = append(results, "  Type: "+opt.typ)
		}
		if opt.desc != "" {
			results = append(results, "  "+truncate(opt.desc, 200))
		}
		results = append(results, "")
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}

func infoNixvim(ctx context.Context, name string) string {
	options, err := nixvimCache.get(ctx)
	if err != nil {
		return errCode("API_ERROR", err.Error())
	}
	for _, opt := range options {
		if srcStr(opt, "name") == name {
			return formatNixvimOption(opt)
		}
	}
	nl := strings.ToLower(name)
	for _, opt := range options {
		if strings.ToLower(srcStr(opt, "name")) == nl {
			return formatNixvimOption(opt)
		}
	}
	var similar []string
	for _, opt := range options {
		if len(similar) >= 5 {
			break
		}
		if strings.Contains(strings.ToLower(srcStr(opt, "name")), nl) {
			similar = append(similar, srcStr(opt, "name"))
		}
	}
	if len(similar) > 0 {
		return errCode("NOT_FOUND", fmt.Sprintf("Option '%s' not found. Similar: %s", name, strings.Join(similar, ", ")))
	}
	return errCode("NOT_FOUND", fmt.Sprintf("Nixvim option '%s' not found", name))
}

func formatNixvimOption(opt map[string]any) string {
	lines := []string{"Nixvim Option: " + srcStr(opt, "name")}
	if t := srcStr(opt, "type"); t != "" {
		lines = append(lines, "Type: "+t)
	}
	if desc := stripHTML(srcStr(opt, "description")); desc != "" {
		lines = append(lines, "Description: "+desc)
	}
	if def := stripHTML(srcStr(opt, "default")); def != "" {
		lines = append(lines, "Default: "+def)
	}
	if ex := stripHTML(srcStr(opt, "example")); ex != "" {
		lines = append(lines, "Example: "+truncate(ex, 500))
	}
	if d := msFirstStr(opt, "declarations"); d != "" {
		lines = append(lines, "Declared in: "+d)
	}
	return strings.Join(lines, "\n")
}

func statsNixvim(ctx context.Context) string {
	options, err := nixvimCache.get(ctx)
	if err != nil {
		return errCode("API_ERROR", err.Error())
	}
	categories := map[string]int{}
	for _, opt := range options {
		name := srcStr(opt, "name")
		cat := name
		if before, _, ok := strings.Cut(name, "."); ok {
			cat = before
		}
		categories[cat]++
	}
	result := []string{
		"Nixvim Statistics:",
		"* Total options: " + comma(len(options)),
		fmt.Sprintf("* Categories: %d", len(categories)),
		"* Top categories:",
	}
	for _, kv := range topN(categories, 5) {
		result = append(result, fmt.Sprintf("  - %s: %s", kv.key, comma(kv.val)))
	}
	return strings.Join(result, "\n")
}

func browseNixvim(ctx context.Context, prefix string) string {
	options, err := nixvimCache.get(ctx)
	if err != nil {
		return errCode("API_ERROR", err.Error())
	}
	if prefix == "" {
		categories := map[string]int{}
		for _, opt := range options {
			name := srcStr(opt, "name")
			cat := name
			if before, _, ok := strings.Cut(name, "."); ok {
				cat = before
			}
			categories[cat]++
		}
		results := []string{fmt.Sprintf("Nixvim option categories (%d total):\n", len(categories))}
		for _, kv := range topN(categories, len(categories)) {
			results = append(results, fmt.Sprintf("* %s (%d options)", kv.key, kv.val))
		}
		return strings.Join(results, "\n")
	}

	prefixDot := prefix
	if !strings.HasSuffix(prefixDot, ".") {
		prefixDot += "."
	}
	type match struct{ name, typ, desc string }
	var matches []match
	for _, opt := range options {
		name := srcStr(opt, "name")
		if strings.HasPrefix(name, prefixDot) || name == prefix {
			matches = append(matches, match{name, srcStr(opt, "type"), stripHTML(srcStr(opt, "description"))})
		}
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No Nixvim options found with prefix '%s'", prefix)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].name < matches[j].name })
	results := []string{fmt.Sprintf("Nixvim options with prefix '%s' (%d found):\n", prefix, len(matches))}
	shown := matches
	if len(shown) > 100 {
		shown = shown[:100]
	}
	for _, opt := range shown {
		results = append(results, "* "+opt.name)
		if opt.typ != "" {
			results = append(results, "  Type: "+opt.typ)
		}
		if opt.desc != "" {
			results = append(results, "  "+truncate(opt.desc, 150))
		}
		results = append(results, "")
	}
	if len(matches) > 100 {
		results = append(results, fmt.Sprintf("... and %d more options", len(matches)-100))
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}
