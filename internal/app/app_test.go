package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestComma(t *testing.T) {
	cases := map[int]string{0: "0", 12: "12", 1234: "1,234", 1234567: "1,234,567", 100000: "100,000"}
	for in, want := range cases {
		if got := comma(in); got != want {
			t.Errorf("comma(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatSize(t *testing.T) {
	cases := map[int64]string{500: "500 B", 1024: "1.0 KB", 1536: "1.5 KB", 1048576: "1.0 MB"}
	for in, want := range cases {
		if got := formatSize(in); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate long = %q", got)
	}
}

func TestParseNarInfo(t *testing.T) {
	in := "StorePath: /nix/store/x-hello\nURL: nar/abc.nar.xz\nCompression: xz\nFileSize: 12345\nNarSize: 67890\n"
	ni := parseNarInfo(in)
	if !ni.hasFileSize || ni.fileSize != 12345 {
		t.Errorf("fileSize = %d (has=%v)", ni.fileSize, ni.hasFileSize)
	}
	if !ni.hasNarSize || ni.narSize != 67890 {
		t.Errorf("narSize = %d", ni.narSize)
	}
	if ni.compression != "xz" {
		t.Errorf("compression = %q", ni.compression)
	}
}

func TestStripHTML(t *testing.T) {
	if got := stripHTML("<p>hello <code>world</code></p>"); got != "hello world" {
		t.Errorf("stripHTML = %q", got)
	}
}

func TestResolveChannels(t *testing.T) {
	avail := map[string]string{
		"latest-45-nixos-unstable": "158,988 documents",
		"latest-46-nixos-25.11":    "155,783 documents",
		"latest-44-nixos-25.05":    "150,000 documents",
	}
	r := resolveChannels(avail)
	if r["unstable"] != "latest-45-nixos-unstable" {
		t.Errorf("unstable = %q", r["unstable"])
	}
	// Highest version wins for "stable".
	if r["stable"] != "latest-46-nixos-25.11" {
		t.Errorf("stable = %q", r["stable"])
	}
	if r["beta"] != r["stable"] {
		t.Errorf("beta should mirror stable")
	}
	if r["25.05"] != "latest-44-nixos-25.05" {
		t.Errorf("25.05 = %q", r["25.05"])
	}
}

func TestResolveChannelsFallback(t *testing.T) {
	r := resolveChannels(map[string]string{})
	want := map[string]string{
		"unstable": "latest-48-nixos-unstable",
		"stable":   "latest-48-nixos-26.05",
		"26.05":    "latest-48-nixos-26.05",
		"25.11":    "latest-48-nixos-25.11",
		"beta":     "latest-48-nixos-26.05",
	}
	if len(r) != len(want) {
		t.Fatalf("fallback channels = %v, want exactly %v", r, want)
	}
	for channel, index := range want {
		if r[channel] != index {
			t.Fatalf("%s = %q, want %q (all fallback channels: %v)", channel, r[channel], index, r)
		}
	}
}

func TestDiscoverAvailableUsesAliases(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()

	countResponses := map[string]string{
		"/backend/latest-48-nixos-unstable/_count": `{"count":100}`,
		"/backend/latest-48-nixos-26.05/_count":    `{"count":200}`,
		"/backend/latest-99-nixos-99.99/_count":    `{"count":0}`,
	}
	var mu sync.Mutex
	requestedCounts := map[string]bool{}
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/backend/_cat/aliases":
			return testResponse(200, `[
				{"alias":"latest-48-nixos-unstable"},
				{"alias":"latest-48-nixos-26.05"},
				{"alias":"latest-99-nixos-99.99"},
				{"alias":".kibana"},
				{"alias":"latest-48-group-manual"}
			]`), nil
		case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/backend/"):
			mu.Lock()
			requestedCounts[req.URL.Path] = true
			mu.Unlock()
			if body, ok := countResponses[req.URL.Path]; ok {
				return testResponse(200, body), nil
			}
		}
		return testResponse(404, `{}`), nil
	})}

	available := discoverAvailable(context.Background())
	if _, ok := available["latest-48-nixos-unstable"]; !ok {
		t.Fatalf("generation 48 index not discovered: %v", available)
	}
	if _, ok := available["latest-48-nixos-26.05"]; !ok {
		t.Fatalf("release alias not discovered: %v", available)
	}
	if _, ok := available["latest-99-nixos-99.99"]; ok {
		t.Fatalf("zero-count alias discovered: %v", available)
	}
	for _, invalid := range []string{"/backend/.kibana/_count", "/backend/latest-48-group-manual/_count"} {
		if requestedCounts[invalid] {
			t.Fatalf("invalid alias was count-probed: %s", invalid)
		}
	}
}

func TestResolveChannelsPicksHighestGenerationPerChannel(t *testing.T) {
	available := map[string]string{
		"latest-46-nixos-unstable": "999,999 documents",
		"latest-48-nixos-unstable": "1 documents",
		"latest-46-nixos-25.11":    "999,999 documents",
		"latest-48-nixos-25.11":    "1 documents",
	}
	resolved := resolveChannels(available)
	if resolved["unstable"] != "latest-48-nixos-unstable" {
		t.Fatalf("unstable = %q", resolved["unstable"])
	}
	if resolved["25.11"] != "latest-48-nixos-25.11" {
		t.Fatalf("25.11 = %q", resolved["25.11"])
	}
	if resolved["stable"] != "latest-48-nixos-25.11" {
		t.Fatalf("stable = %q", resolved["stable"])
	}
}

func TestMemoTTL(t *testing.T) {
	calls := 0
	m := &memo[int]{ttl: 50 * time.Millisecond, loader: func(context.Context) (int, error) {
		calls++
		return calls, nil
	}}
	if v, _ := m.get(context.Background()); v != 1 {
		t.Fatalf("first get = %d", v)
	}
	if v, _ := m.get(context.Background()); v != 1 {
		t.Fatalf("cached get = %d (should reuse)", v)
	}
	time.Sleep(60 * time.Millisecond)
	if v, _ := m.get(context.Background()); v != 2 {
		t.Fatalf("post-TTL get = %d (should refresh)", v)
	}
}

func TestMemoServeStale(t *testing.T) {
	fail := false
	m := &memo[int]{ttl: time.Millisecond, loader: func(context.Context) (int, error) {
		if fail {
			return 0, errors.New("boom")
		}
		return 42, nil
	}}
	if v, err := m.get(context.Background()); v != 42 || err != nil {
		t.Fatalf("first get = %d, %v", v, err)
	}
	fail = true
	time.Sleep(2 * time.Millisecond)
	if v, err := m.get(context.Background()); v != 42 || err != nil {
		t.Fatalf("stale get = %d, %v (should serve last good value)", v, err)
	}
}

func TestFlattenInputs(t *testing.T) {
	data := &flakeArchive{
		Path: "/nix/store/root",
		Inputs: map[string]flakeArchive{
			"nixpkgs": {Path: "/nix/store/nixpkgs"},
			"flake-parts": {
				Path:   "/nix/store/fp",
				Inputs: map[string]flakeArchive{"nixpkgs-lib": {Path: "/nix/store/lib"}},
			},
		},
	}
	got := flattenInputs(data, "")
	if got["nixpkgs"] != "/nix/store/nixpkgs" {
		t.Errorf("nixpkgs = %q", got["nixpkgs"])
	}
	if got["flake-parts.nixpkgs-lib"] != "/nix/store/lib" {
		t.Errorf("nested = %q", got["flake-parts.nixpkgs-lib"])
	}
}

func TestNormalizeNixdevDocname(t *testing.T) {
	cases := map[string]string{
		"tutorials/nix-language":                 "tutorials/nix-language",
		"https://nix.dev/tutorials/nix-language": "tutorials/nix-language",
		"https://nix.dev/tutorials/x.html#frag":  "tutorials/x",
		"/tutorials/nix-language.html":           "tutorials/nix-language",
	}
	for in, want := range cases {
		if got := normalizeNixdevDocname(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
