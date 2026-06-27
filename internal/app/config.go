package app

import (
	"os"
	"strings"
)

// Config holds resolved server-wide settings. Per-call inputs (channel,
// source, query) come from tool arguments, not here.
type Config struct {
	// AuthToken, when set, requires HTTP clients to present it as a bearer
	// token (Authorization: Bearer <token>) or X-Mcp-Token header. Ignored
	// for stdio.
	AuthToken string
}

func loadConfig() Config {
	return Config{
		AuthToken: os.Getenv("NIX_MCP_TOKEN"),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on", "y":
		return true
	}
	return false
}
