package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/stubbedev/nix-mcp/version"
)

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[nix-mcp] "+format+"\n", args...)
}

// cfg is the process-wide configuration, set once at startup.
var cfg Config

// Run starts the MCP server (stdio or HTTP) and returns a process exit code.
func Run() int {
	opt, err := parseCLI(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		logf("invalid arguments: %v", err)
		return 2
	}
	if opt.version {
		fmt.Println("nix-mcp " + version.Version)
		return 0
	}

	cfg = loadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if opt.httpOn {
		// A fresh server per session so each client gets its own request
		// context (and header-pinned roots) over the shared HTTP endpoint.
		getServer := func(*http.Request) *mcp.Server { return newServer() }
		if err := serveHTTP(ctx, getServer, opt.httpAddr, opt.httpPath); err != nil {
			logf("http server error: %v", err)
			return 1
		}
		return 0
	}

	if err := newServer().Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		logf("stdio server error: %v", err)
		return 1
	}
	return 0
}

// newServer builds an MCP server with the two tools registered.
func newServer() *mcp.Server {
	srv := mcp.NewServer(
		&mcp.Implementation{Name: "nix-mcp", Version: version.Version},
		&mcp.ServerOptions{Instructions: serverInstructions},
	)
	for _, d := range toolDefs() {
		srv.AddTool(&mcp.Tool{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema}, dispatch)
	}
	return srv
}

// toolCallTimeout caps total wall-clock for one tool call. flake-inputs may
// shell out to `nix flake archive`, which can download inputs, so this is
// generous.
const toolCallTimeout = 120 * time.Second

// dispatch routes one tools/call to its handler. Every handler returns a
// plain-text string (matching the Python mcp-nixos output), including its own
// "Error (CODE): ..." strings, so MCP-level errors are reserved for genuinely
// unexpected failures.
func dispatch(ctx context.Context, req *mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	ctx, cancel := context.WithTimeout(ctx, toolCallTimeout)
	defer cancel()

	args := map[string]any{}
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return textResult(errorf("invalid arguments: %v", err)), nil
		}
	}

	defer func() {
		if r := recover(); r != nil {
			result = textResult(errorf("internal error: %v", r))
			err = nil
		}
	}()

	var out string
	switch req.Params.Name {
	case "nix":
		out = nixDispatch(ctx, req, args)
	case "nix_versions":
		out = nixVersions(ctx, args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", req.Params.Name)
	}
	return textResult(out), nil
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// ── argument helpers ─────────────────────────────────────────────────────────

func argString(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func argStringOr(args map[string]any, key, def string) string {
	if s := argString(args, key); s != "" {
		return s
	}
	return def
}

func argInt(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := parseIntStrict(n); err == nil {
			return i
		}
	}
	return def
}
