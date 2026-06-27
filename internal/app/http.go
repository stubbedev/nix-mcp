package app

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultHTTPAddr = "127.0.0.1:8765"
	defaultHTTPPath = "/mcp"

	sessionTTL = 30 * time.Minute
)

// authMiddleware enforces the bearer token (NIX_MCP_TOKEN) when configured.
// When no token is set it is a no-op, preserving the zero-config local flow.
func authMiddleware(next http.Handler) http.Handler {
	want := cfg.AuthToken
	if want == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			got = r.Header.Get("X-Mcp-Token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func ensureLeadingSlash(p string) string {
	if p == "" {
		return defaultHTTPPath
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

// serveHTTP runs the MCP server over the SDK's Streamable HTTP transport on a
// single endpoint, with idle-session reaping. Request headers are exposed to
// tool handlers so a proxy can pin the workspace root per request
// (X-Mcp-Root), letting many clients share one server. Shuts down when ctx is
// cancelled.
func serveHTTP(ctx context.Context, getServer func(*http.Request) *mcp.Server, addr, path string) error {
	handler := mcp.NewStreamableHTTPHandler(
		getServer,
		&mcp.StreamableHTTPOptions{SessionTimeout: sessionTTL},
	)

	mux := http.NewServeMux()
	mux.Handle(path, authMiddleware(handler))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	httpSrv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	logf("listening on http://%s%s (MCP Streamable HTTP)", addr, path)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
