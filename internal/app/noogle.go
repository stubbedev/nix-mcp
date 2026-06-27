package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

func getMap(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

func getList(m map[string]any, key string) []any {
	if v, ok := m[key].([]any); ok {
		return v
	}
	return nil
}

func joinAnyDotted(parts []any) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, fmt.Sprint(p))
	}
	return strings.Join(out, ".")
}

func noogleFunctionPath(doc map[string]any) string {
	meta := getMap(doc, "meta")
	if meta != nil {
		if path := getList(meta, "path"); len(path) > 0 {
			return joinAnyDotted(path)
		}
		if title, ok := meta["title"].(string); ok {
			return title
		}
	}
	return ""
}

func noogleTypeSignature(doc map[string]any) string {
	content := getMap(doc, "content")
	if content == nil {
		return ""
	}
	if s, ok := content["signature"].(string); ok && s != "" {
		return s
	}
	if t, ok := content["type"].(string); ok && t != "" {
		return t
	}
	return ""
}

func noogleAliases(doc map[string]any) []string {
	meta := getMap(doc, "meta")
	if meta == nil {
		return nil
	}
	aliases := getList(meta, "aliases")
	if aliases == nil {
		return nil
	}
	out := make([]string, 0, len(aliases))
	for _, a := range aliases {
		if list, ok := a.([]any); ok {
			out = append(out, joinAnyDotted(list))
		} else {
			out = append(out, fmt.Sprint(a))
		}
	}
	return out
}

func noogleDescription(doc map[string]any) string {
	content := getMap(doc, "content")
	if content == nil {
		return ""
	}
	if d, ok := content["content"].(string); ok && d != "" {
		return stripHTML(d)
	}
	if lambda := getMap(content, "lambda"); lambda != nil {
		if d, ok := lambda["content"].(string); ok && d != "" {
			return stripHTML(d)
		}
	}
	return ""
}

func searchNoogle(ctx context.Context, query string, limit int) string {
	data, err := noogleCache.get(ctx)
	if err != nil {
		return errCode("API_ERROR", err.Error())
	}
	ql := strings.ToLower(query)

	type scored struct {
		score int
		path  string
		doc   map[string]any
	}
	var matches []scored
	for _, doc := range data {
		path := noogleFunctionPath(doc)
		pl := strings.ToLower(path)
		desc := strings.ToLower(noogleDescription(doc))
		aliases := noogleAliases(doc)

		score := 0
		switch {
		case pl == ql:
			score = 100
		case strings.Contains(pl, ql):
			if strings.HasSuffix(pl, ql) || strings.HasSuffix(pl, "."+ql) {
				score = 50
			} else {
				score = 30
			}
		default:
			aliasHit := false
			for _, a := range aliases {
				if strings.Contains(strings.ToLower(a), ql) {
					aliasHit = true
					break
				}
			}
			if aliasHit {
				score = 40
			} else if strings.Contains(desc, ql) {
				score = 10
			}
		}
		if score > 0 {
			matches = append(matches, scored{score, path, doc})
		}
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No Noogle functions found matching '%s'", query)
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].path < matches[j].path
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}

	results := []string{fmt.Sprintf("Found %d Noogle functions matching '%s':\n", len(matches), query)}
	for _, m := range matches {
		results = append(results, "* "+m.path)
		if sig := noogleTypeSignature(m.doc); sig != "" {
			results = append(results, "  Type: "+truncate(sig, 100))
		}
		if desc := noogleDescription(m.doc); desc != "" {
			results = append(results, "  "+truncate(desc, 200))
		}
		if aliases := noogleAliases(m.doc); len(aliases) > 0 {
			n := len(aliases)
			if n > 3 {
				n = 3
			}
			results = append(results, "  Aliases: "+strings.Join(aliases[:n], ", "))
		}
		results = append(results, "")
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}

func infoNoogle(ctx context.Context, name string) string {
	data, err := noogleCache.get(ctx)
	if err != nil {
		return errCode("API_ERROR", err.Error())
	}
	nl := strings.ToLower(name)

	var exact map[string]any
	type pm struct {
		path string
		doc  map[string]any
	}
	var partial []pm
	for _, doc := range data {
		path := noogleFunctionPath(doc)
		pl := strings.ToLower(path)
		aliasHit := false
		for _, a := range noogleAliases(doc) {
			if strings.ToLower(a) == nl {
				aliasHit = true
				break
			}
		}
		if pl == nl || aliasHit {
			exact = doc
			break
		} else if strings.Contains(pl, nl) {
			partial = append(partial, pm{path, doc})
		}
	}

	if exact == nil && len(partial) == 0 {
		return errCode("NOT_FOUND", fmt.Sprintf("Noogle function '%s' not found", name))
	}
	if exact == nil {
		var sugg []string
		for i, p := range partial {
			if i >= 5 {
				break
			}
			sugg = append(sugg, p.path)
		}
		return errCode("NOT_FOUND", fmt.Sprintf("Function '%s' not found. Similar: %s", name, strings.Join(sugg, ", ")))
	}

	doc := exact
	path := noogleFunctionPath(doc)
	meta := getMap(doc, "meta")
	content := getMap(doc, "content")

	results := []string{"Noogle Function: " + path}
	if sig := noogleTypeSignature(doc); sig != "" {
		results = append(results, "Type: "+sig)
	}
	results = append(results, "Path: "+path)
	if aliases := noogleAliases(doc); len(aliases) > 0 {
		results = append(results, "Aliases: "+strings.Join(aliases, ", "))
	}
	if meta != nil {
		if primop := getMap(meta, "primop_meta"); primop != nil {
			if arity, ok := primop["arity"]; ok && arity != nil {
				var args []string
				for _, a := range getList(primop, "args") {
					args = append(args, fmt.Sprint(a))
				}
				if len(args) > 0 {
					results = append(results, fmt.Sprintf("Primop: Yes (arity: %s, args: %s)", fmtNum(arity), strings.Join(args, ", ")))
				} else {
					results = append(results, fmt.Sprintf("Primop: Yes (arity: %s)", fmtNum(arity)))
				}
			}
		}
	}
	results = append(results, "")
	if desc := noogleDescription(doc); desc != "" {
		results = append(results, "Description:", desc, "")
	}
	if content != nil {
		if ex, ok := content["example"].(string); ok && ex != "" {
			results = append(results, "Example:", truncate(stripHTML(ex), 500), "")
		}
	}
	if meta != nil {
		if pos := getMap(meta, "position"); pos != nil {
			file, _ := pos["file"].(string)
			if file != "" {
				if line, ok := pos["line"]; ok && line != nil {
					results = append(results, fmt.Sprintf("Source: %s:%s", file, fmtNum(line)))
				} else {
					results = append(results, "Source: "+file)
				}
			}
		}
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}

func statsNoogle(ctx context.Context) string {
	data, err := noogleCache.get(ctx)
	if err != nil {
		return errCode("API_ERROR", err.Error())
	}
	categories := map[string]int{}
	withSignatures, withDocs := 0, 0
	for _, doc := range data {
		path := noogleFunctionPath(doc)
		cat := path
		if strings.Contains(path, ".") {
			parts := strings.Split(path, ".")
			if len(parts) >= 2 {
				cat = parts[0] + "." + parts[1]
			}
		}
		categories[cat]++
		if noogleTypeSignature(doc) != "" {
			withSignatures++
		}
		if noogleDescription(doc) != "" {
			withDocs++
		}
	}
	results := []string{
		"Noogle Statistics:",
		"* Total functions: " + comma(len(data)),
		"* With type signatures: " + comma(withSignatures),
		"* With documentation: " + comma(withDocs),
		fmt.Sprintf("* Categories: %d", len(categories)),
		"* Top categories:",
	}
	for _, kv := range topN(categories, 10) {
		results = append(results, fmt.Sprintf("  - %s: %d", kv.key, kv.val))
	}
	results = append(results, "", "Data source: noogle.dev (updated daily)")
	return strings.Join(results, "\n")
}

func browseNoogle(ctx context.Context, prefix string) string {
	data, err := noogleCache.get(ctx)
	if err != nil {
		return errCode("API_ERROR", err.Error())
	}
	if prefix == "" {
		categories := map[string]int{}
		for _, doc := range data {
			path := noogleFunctionPath(doc)
			cat := path
			if strings.Contains(path, ".") {
				parts := strings.Split(path, ".")
				if len(parts) >= 2 {
					cat = parts[0] + "." + parts[1]
				}
			}
			categories[cat]++
		}
		results := []string{fmt.Sprintf("Noogle function categories (%d total):\n", len(categories))}
		for _, kv := range topN(categories, len(categories)) {
			results = append(results, fmt.Sprintf("* %s (%d functions)", kv.key, kv.val))
		}
		return strings.Join(results, "\n")
	}

	pl := strings.ToLower(prefix)
	prefixDot := pl
	if !strings.HasSuffix(prefixDot, ".") {
		prefixDot += "."
	}
	type match struct{ path, typ, desc string }
	var matches []match
	for _, doc := range data {
		path := noogleFunctionPath(doc)
		p := strings.ToLower(path)
		if strings.HasPrefix(p, prefixDot) || p == pl {
			matches = append(matches, match{path, noogleTypeSignature(doc), noogleDescription(doc)})
		}
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No Noogle functions found with prefix '%s'", prefix)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].path < matches[j].path })
	results := []string{fmt.Sprintf("Noogle functions with prefix '%s' (%d found):\n", prefix, len(matches))}
	shown := matches
	if len(shown) > 100 {
		shown = shown[:100]
	}
	for _, f := range shown {
		results = append(results, "* "+f.path)
		if f.typ != "" {
			results = append(results, "  Type: "+truncate(f.typ, 80))
		}
		if f.desc != "" {
			results = append(results, "  "+truncate(f.desc, 150))
		}
		results = append(results, "")
	}
	if len(matches) > 100 {
		results = append(results, fmt.Sprintf("... and %d more functions", len(matches)-100))
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}

// fmtNum renders a JSON number (float64) without a trailing ".0".
func fmtNum(v any) string {
	switch n := v.(type) {
	case float64:
		if n == float64(int64(n)) {
			return fmt.Sprintf("%d", int64(n))
		}
		return fmt.Sprintf("%v", n)
	default:
		return fmt.Sprint(v)
	}
}
