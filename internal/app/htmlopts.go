package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type htmlOption struct {
	name string
	desc string
	typ  string
}

// ── x/net/html helpers ───────────────────────────────────────────────────────

func nodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(nodeAttr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

// rawText concatenates all descendant text.
func rawText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// collapsedText returns descendant text with whitespace collapsed to single
// spaces. Cleaner than BeautifulSoup's strip=True concatenation, which mashes
// adjacent inline runs together.
func collapsedText(n *html.Node) string {
	return strings.Join(strings.Fields(rawText(n)), " ")
}

// directText returns the first non-empty direct text child, trimmed.
func directText(n *html.Node) string {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			if t := strings.TrimSpace(c.Data); t != "" {
				return t
			}
		}
	}
	return ""
}

func findAll(root *html.Node, a atom.Atom) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == a {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// firstDescendant finds the first descendant element matching a.
func firstDescendant(root *html.Node, a atom.Atom) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.DataAtom == a {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
	return found
}

// firstDescendantAnchorWithID finds the first <a> with a non-empty id.
func firstDescendantAnchorWithID(root *html.Node) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.A && nodeAttr(n, "id") != "" {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}

func nextSiblingElement(n *html.Node, a atom.Atom) *html.Node {
	for s := n.NextSibling; s != nil; s = s.NextSibling {
		if s.Type == html.ElementNode && s.DataAtom == a {
			return s
		}
	}
	return nil
}

// findSpanTerm returns the first <span class="term"> descendant.
func findSpanTerm(root *html.Node) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Span && hasClass(n, "term") {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}

// parseHTMLOptions fetches and parses an options page (home-manager / darwin),
// mirroring utils.parse_html_options.
func parseHTMLOptions(ctx context.Context, pageURL, query, prefix string, limit int) ([]htmlOption, error) {
	status, body, err := httpGet(ctx, pageURL, nil, nil, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch docs: %v", err)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("Failed to fetch docs: HTTP %d", status)
	}
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch docs: %v", err)
	}

	isHM := strings.Contains(pageURL, "home-manager")
	var options []htmlOption
	for _, dt := range findAll(doc, atom.Dt) {
		name := ""
		if isHM {
			if anchor := firstDescendantAnchorWithID(dt); anchor != nil {
				id := nodeAttr(anchor, "id")
				if strings.HasPrefix(id, "opt-") {
					name = strings.ReplaceAll(id[4:], "_name_", "<name>")
				}
			} else if dxt := directText(dt); dxt != "" {
				name = dxt
			} else {
				name = collapsedText(dt)
			}
		} else {
			name = collapsedText(dt)
		}

		if !strings.Contains(name, ".") && len(strings.Fields(name)) > 1 {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(query)) {
			continue
		}
		if prefix != "" && !(strings.HasPrefix(name, prefix+".") || name == prefix) {
			continue
		}

		dd := nextSiblingElement(dt, atom.Dd)
		if dd == nil {
			continue
		}

		var description string
		if p := firstDescendant(dd, atom.P); p != nil {
			description = collapsedText(p)
		} else {
			text := collapsedText(dd)
			description = text // already collapsed; first "line"
		}

		typeInfo := ""
		if term := findSpanTerm(dd); term != nil && strings.Contains(rawText(term), "Type:") {
			typeInfo = strings.TrimSpace(strings.ReplaceAll(collapsedText(term), "Type:", ""))
		} else {
			ddText := rawText(dd)
			if idx := strings.Index(ddText, "Type:"); idx >= 0 {
				rest := ddText[idx+5:]
				if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
					rest = rest[:nl]
				}
				typeInfo = strings.TrimSpace(rest)
			}
		}

		options = append(options, htmlOption{name: name, desc: truncate(description, 200), typ: typeInfo})
		if len(options) >= limit {
			break
		}
	}
	return options, nil
}

// ── home-manager / darwin tools ──────────────────────────────────────────────

func searchHTMLSource(ctx context.Context, pageURL, label, query string, limit int) string {
	options, err := parseHTMLOptions(ctx, pageURL, query, "", limit)
	if err != nil {
		return errMsg(err.Error())
	}
	if len(options) == 0 {
		return fmt.Sprintf("No %s options found matching '%s'", label, query)
	}
	results := []string{fmt.Sprintf("Found %d %s options matching '%s':\n", len(options), label, query)}
	for _, opt := range options {
		results = append(results, "* "+opt.name)
		if opt.typ != "" {
			results = append(results, "  Type: "+opt.typ)
		}
		if opt.desc != "" {
			results = append(results, "  "+opt.desc)
		}
		results = append(results, "")
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}

func infoHTMLSource(ctx context.Context, pageURL, name string) string {
	options, err := parseHTMLOptions(ctx, pageURL, name, "", 100)
	if err != nil {
		return errMsg(err.Error())
	}
	for _, opt := range options {
		if opt.name == name {
			info := []string{"Option: " + name}
			if opt.typ != "" {
				info = append(info, "Type: "+opt.typ)
			}
			if opt.desc != "" {
				info = append(info, "Description: "+opt.desc)
			}
			return strings.Join(info, "\n")
		}
	}
	if len(options) > 0 {
		var suggestions []string
		for _, opt := range options {
			if len(suggestions) >= 5 {
				break
			}
			if strings.Contains(opt.name, name) {
				suggestions = append(suggestions, opt.name)
			}
		}
		if len(suggestions) > 0 {
			return errCode("NOT_FOUND", fmt.Sprintf("Option '%s' not found. Similar: %s", name, strings.Join(suggestions, ", ")))
		}
	}
	return errCode("NOT_FOUND", fmt.Sprintf("Option '%s' not found", name))
}

func statsHTMLSource(ctx context.Context, pageURL, label string, limit int) string {
	options, err := parseHTMLOptions(ctx, pageURL, "", "", limit)
	if err != nil {
		return errMsg(err.Error())
	}
	if len(options) == 0 {
		return errMsg(fmt.Sprintf("Failed to fetch %s statistics", label))
	}
	categories := map[string]int{}
	for _, opt := range options {
		cat := opt.name
		if i := strings.Index(cat, "."); i >= 0 {
			cat = cat[:i]
		}
		categories[cat]++
	}
	result := []string{
		label + " Statistics:",
		"* Total options: " + comma(len(options)),
		fmt.Sprintf("* Categories: %d", len(categories)),
		"* Top categories:",
	}
	for _, kv := range topNStable(categories, 5) {
		result = append(result, fmt.Sprintf("  - %s: %s", kv.key, comma(kv.val)))
	}
	return strings.Join(result, "\n")
}

// browseHTMLSource walks an option tree by prefix, or lists categories.
func browseHTMLSource(ctx context.Context, pageURL, label, prefix string) string {
	if prefix != "" {
		options, err := parseHTMLOptions(ctx, pageURL, "", prefix, 100)
		if err != nil {
			return errMsg(err.Error())
		}
		if len(options) == 0 {
			return fmt.Sprintf("No %s options found with prefix '%s'", label, prefix)
		}
		sort.Slice(options, func(i, j int) bool { return options[i].name < options[j].name })
		results := []string{fmt.Sprintf("%s options with prefix '%s' (%d found):\n", label, prefix, len(options))}
		for _, opt := range options {
			results = append(results, "* "+opt.name)
			if opt.desc != "" {
				results = append(results, "  "+opt.desc)
			}
			results = append(results, "")
		}
		return strings.TrimSpace(strings.Join(results, "\n"))
	}

	options, err := parseHTMLOptions(ctx, pageURL, "", "", 5000)
	if err != nil {
		return errMsg(err.Error())
	}
	categories := map[string]int{}
	for _, opt := range options {
		name := opt.name
		if name != "" && strings.Contains(name, ".") {
			cat := name[:strings.Index(name, ".")]
			if len(cat) > 1 && isIdentifier(cat) && cat == strings.ToLower(cat) {
				categories[cat]++
			}
		}
	}
	results := []string{fmt.Sprintf("%s categories (%d total):\n", label, len(categories))}
	for _, kv := range topNStable(categories, len(categories)) {
		results = append(results, fmt.Sprintf("* %s (%d options)", kv.key, kv.val))
	}
	return strings.Join(results, "\n")
}

// topNStable is topN but used where ties sort by (-count, name) — same as topN.
func topNStable(m map[string]int, n int) []kv { return topN(m, n) }

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			continue
		}
		if i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}
