// Command nix-mcp is a low-footprint MCP server for the Nix ecosystem
// (nixpkgs, NixOS / home-manager / nix-darwin / nixvim options, flakes,
// FlakeHub, NixHub, the binary cache, store paths, the NixOS wiki and
// nix.dev). All logic lives in internal/app; this is just the entrypoint.
package main

import (
	"os"

	"github.com/stubbedev/nix-mcp/internal/app"
)

func main() { os.Exit(app.Run()) }
