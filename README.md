# nix-mcp

A fast, low-footprint [MCP](https://modelcontextprotocol.io) server for the
Nix ecosystem, written in Go. It is a drop-in reimplementation of
[mcp-nixos](https://github.com/utensils/mcp-nixos) with the same two-tool
surface and output, but as a single static binary (~tens of MB RSS instead of
a ~150 MB Python runtime).

- **stdio and HTTP** transports — standalone or behind a proxy.
- **MCP roots** (incl. the `X-Mcp-Root` HTTP header) so one HTTP instance can
  serve many clients/worktrees; `flake-inputs` resolves the flake directory
  from the calling client's root.
- **Live data**: queries search.nixos.org (Elasticsearch), NixHub, FlakeHub,
  cache.nixos.org, the NixOS wiki, nix.dev, Noogle, Home Manager options,
  and the nix-darwin / Nixvim option docs — faster and more current than
  `nix search`.
- **No runtime deps** beyond `nix` itself, and only for `flake-inputs` (which
  shells out to `nix flake archive`); everything else is pure Go.

## Install

Nix (binary cache at `nix.stubbe.dev`):

```sh
nix run github:stubbedev/nix-mcp
```

Go:

```sh
go install github.com/stubbedev/nix-mcp@latest
```

## Usage

stdio (default):

```sh
nix-mcp
```

HTTP (multi-client; one shared server):

```sh
nix-mcp --http            # 127.0.0.1:8765/mcp
# or:  nix-mcp --http=0.0.0.0:9000  --http-path=/mcp
```

Behind a proxy, pin the workspace root per request so `flake-inputs` targets
the right project:

```
X-Mcp-Root: /path/to/flake-project
```

Example MCP client config (stdio):

```json
{
  "mcpServers": {
    "nix": {
      "command": "nix-mcp"
    }
  }
}
```

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `NIX_MCP_HTTP` | — | Truthy (`1`/`true`) enables HTTP on the default `127.0.0.1:8765`. |
| `NIX_MCP_HTTP_ADDR` | — | Enable HTTP on this address (or use `--http`). |
| `NIX_MCP_HTTP_PATH` | `/mcp` | HTTP endpoint path (`--http-path`). |
| `NIX_MCP_TOKEN` | — | If set, HTTP clients must send it as `Authorization: Bearer <token>` (or `X-Mcp-Token`). Ignored for stdio. |

For drop-in compatibility with the Python `mcp-nixos` service, the
`MCP_NIXOS_TRANSPORT=http` / `MCP_NIXOS_HOST` / `MCP_NIXOS_PORT` /
`MCP_NIXOS_PATH` env vars are also honored.

## Tools

Two tools are exposed (no gating — both are always available):

- **`nix`** — unified `action` dispatcher:
  - `action=search` — keyword search. `source` one of `nixos` (default),
    `home-manager`, `darwin`, `flakes`, `flakehub`, `nixvim`, `wiki`,
    `nix-dev`, `noogle`, `nixhub`. For `source=nixos`, `type` is
    `packages`/`options`/`programs`/`flakes`.
  - `action=info` — details for an exact name (package/option, HM/darwin/nixvim
    option, flakehub `org/project`, wiki page, nix.dev page, noogle function,
    nixhub package).
  - `action=stats` — counts per source.
  - `action=browse` — walk an option tree by prefix (`darwin`, `noogle`);
    for Home Manager option-name fragments or keywords, use `action=search`
    with `source=home-manager`.
  - `action=channels` — list NixOS channels with indexed/branch revisions.
  - `action=flake-inputs` — `type=list|ls|read` over a flake's inputs (uses the
    client root / `source` path; requires `nix` on PATH).
  - `action=cache` — binary-cache status for a package across systems.
  - `action=store` — `type=ls|read` of an absolute `/nix/store/` path.
- **`nix_versions`** — package version history from NixHub (which nixpkgs
  commit shipped version X, attribute path, platforms, dates).

### Source status

Improvements over the upstream `mcp-nixos` (whose scrapers for these had broken):

- **flakes** — the search index generation (`latest-N-group-manual`) is now
  discovered at runtime instead of hardcoded, so flake search/stats work again
  (upstream's index data is currently sparse, but the query is correct).
- **home-manager** — search, exact info, and stats use search.nixos.org's
  `home-manager-option` index. Legacy `action=browse` calls return guidance;
  Home Manager tree browse is not available.
- **nixvim** — upstream replaced its scrapeable docs with a binary
  WASM-decoded index, so it returns a clear "unavailable" message instead of
  silently empty results. **nixos** and **darwin** option search are unaffected
  and work.

## Development

```sh
just build    # compile
just test     # go test ./...
just lint     # format + vet + golangci-lint
just check    # everything CI runs
```

Releases are cut with `just release-patch|minor|major` (tags `vX.Y.Z`), which
trigger multi-arch binary builds, the nix cache push, and the GitHub release.

## License

MIT
