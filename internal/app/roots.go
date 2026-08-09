package app

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpRoot is a workspace root exposed by the MCP client (the "roots" feature):
// the flake directory the server should operate in for flake-inputs.
type mcpRoot struct {
	URI  string
	Name string
}

// path returns the filesystem path for a file:// root, or the raw value when
// it is already a plain path.
func (r mcpRoot) path() string {
	if strings.HasPrefix(r.URI, "file://") {
		if u, err := url.Parse(r.URI); err == nil && u.Path != "" {
			return u.Path
		}
	}
	return r.URI
}

func rootFromString(s string) (mcpRoot, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return mcpRoot{}, false
	}
	return mcpRoot{URI: s}, true
}

// rootHeaders are the request headers a proxy/harness may set to hand the
// server the workspace root(s) without the MCP roots round-trip. Values are
// file:// URIs or plain paths; multiple roots may be comma-separated.
// X-Repo-Root leads: it is the name the rest of this fleet reads and the one
// the Claude Code entries send, and headers are the only workspace signal that
// survives MCP 2026-07-28 (see resolveRoots).
var rootHeaders = []string{"X-Repo-Root", "X-Mcp-Roots", "X-Mcp-Root", "Mcp-Roots", "Mcp-Root"}

func parseRootHeaders(h http.Header) []mcpRoot {
	var roots []mcpRoot
	for _, name := range rootHeaders {
		for _, v := range h.Values(name) {
			for part := range strings.SplitSeq(v, ",") {
				if r, ok := rootFromString(part); ok {
					roots = append(roots, r)
				}
			}
		}
	}
	return roots
}

// resolveRoots returns the client's workspace roots for the in-flight call.
// Header-pinned roots (set by a proxy/harness over HTTP) take precedence; else
// the roots are fetched from the client session via roots/list.
func resolveRoots(ctx context.Context, req *mcp.CallToolRequest) []mcpRoot {
	if req == nil {
		return nil
	}
	if req.Extra != nil && req.Extra.Header != nil {
		if roots := parseRootHeaders(req.Extra.Header); len(roots) > 0 {
			return roots
		}
	}
	if req.Session == nil {
		return nil
	}
	// Only ask for roots when the client advertised the capability — otherwise
	// ListRoots blocks until timeout against clients that don't support it —
	// and only below rootsRemovedFrom, where a server may still ask at all.
	if !rootsAllowed(req.Session.InitializeParams()) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := req.Session.ListRoots(ctx, &mcp.ListRootsParams{})
	if err != nil || res == nil {
		return nil
	}
	out := make([]mcpRoot, 0, len(res.Roots))
	for _, r := range res.Roots {
		out = append(out, mcpRoot{URI: r.URI, Name: r.Name})
	}
	return out
}

// rootDir resolves the flake directory for flake-inputs. An explicit path
// argument wins; otherwise the first client root; otherwise the process cwd
// (".") as the Python implementation did.
func rootDir(ctx context.Context, req *mcp.CallToolRequest, explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, r := range resolveRoots(ctx, req) {
		if p := r.path(); p != "" {
			return p
		}
	}
	return "."
}

// rootsRemovedFrom is the first protocol revision that forbids server-initiated
// JSON-RPC requests (SEP-2322 / SEP-2575): from there on roots/list is not
// something a server can ask for, only something a tool handler can request via
// InputRequests. Clients on that revision must pin the workspace with one of
// the rootHeaders instead. ISO dates compare correctly as strings.
const rootsRemovedFrom = "2026-07-28"

// rootsAllowed reports whether the client behind these initialize params may
// still be asked for its roots: it advertised the capability, on a protocol
// version that still allows the question.
func rootsAllowed(ip *mcp.InitializeParams) bool {
	if ip == nil || ip.Capabilities == nil || ip.ProtocolVersion >= rootsRemovedFrom {
		return false
	}
	return ip.Capabilities.RootsV2 != nil
}
