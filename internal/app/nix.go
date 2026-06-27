package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// nixDispatch routes the unified `nix` tool (mirrors server.py's nix()).
func nixDispatch(ctx context.Context, req *mcp.CallToolRequest, args map[string]any) string {
	action := argString(args, "action")
	query := argString(args, "query")
	source := argStringOr(args, "source", "nixos")
	typ := argStringOr(args, "type", "packages")
	channel := argStringOr(args, "channel", "unstable")
	limit := argInt(args, "limit", 20)
	version := argStringOr(args, "version", "latest")
	system := argString(args, "system")

	// Limit validation: flake-inputs/store read allow up to maxLineLimit.
	switch {
	case action == "flake-inputs" && typ == "read":
		if limit < 1 || limit > maxLineLimit {
			return errMsg(fmt.Sprintf("Limit must be 1-%d for flake-inputs read", maxLineLimit))
		}
	case action == "store" && typ == "read":
		if limit < 1 || limit > maxLineLimit {
			return errMsg(fmt.Sprintf("Limit must be 1-%d for store read", maxLineLimit))
		}
	default:
		if limit < 1 || limit > 100 {
			return errMsg("Limit must be 1-100")
		}
	}

	// `options` is a legacy alias for `browse` (GH #125).
	if action == "options" {
		action = "browse"
	}

	switch action {
	case "search":
		return dispatchSearch(ctx, source, typ, query, limit, channel)
	case "info":
		return dispatchInfo(ctx, source, typ, query, channel)
	case "stats":
		return dispatchStats(ctx, source, channel)
	case "browse":
		return dispatchBrowse(ctx, source, query)
	case "channels":
		return listChannels(ctx)
	case "flake-inputs":
		return dispatchFlakeInputs(ctx, req, source, typ, query, limit)
	case "cache":
		if query == "" {
			return errMsg("Package name required for cache action")
		}
		return checkBinaryCache(ctx, query, version, system)
	case "store":
		return dispatchStore(ctx, typ, query, limit)
	default:
		return errMsg(fmt.Sprintf("Unknown action: %q. Must be one of: "+
			"search, info, stats, browse, channels, flake-inputs, cache, store. "+
			`Example: {"action": "search", "query": "firefox"}`, action))
	}
}

func dispatchSearch(ctx context.Context, source, typ, query string, limit int, channel string) string {
	if query == "" {
		return errMsg(`Query required for search. Example: {"action": "search", "query": "firefox"}`)
	}
	switch source {
	case "nixos":
		switch typ {
		case "packages", "options", "programs", "flakes":
		default:
			return errMsg("For source=nixos, type must be one of: packages, options, programs, flakes. " +
				`Example: {"action": "search", "query": "nginx", "type": "options"}`)
		}
		return searchNixOS(ctx, query, typ, limit, channel)
	case "home-manager":
		return searchHTMLSource(ctx, homeManagerURL, "Home Manager", query, limit)
	case "darwin":
		return searchHTMLSource(ctx, darwinURL, "nix-darwin", query, limit)
	case "flakes":
		return searchFlakes(ctx, query, limit)
	case "flakehub":
		return searchFlakehub(ctx, query, limit)
	case "nixvim":
		return searchNixvim(ctx, query, limit)
	case "wiki":
		return searchWiki(ctx, query, limit)
	case "nix-dev":
		return searchNixdev(ctx, query, limit)
	case "noogle":
		return searchNoogle(ctx, query, limit)
	case "nixhub":
		return searchNixhub(ctx, query, limit)
	default:
		return errMsg(fmt.Sprintf("Unknown source: %q. Must be one of: "+
			"nixos, home-manager, darwin, flakes, flakehub, nixvim, wiki, nix-dev, noogle, nixhub.", source))
	}
}

func dispatchInfo(ctx context.Context, source, typ, query, channel string) string {
	if query == "" {
		return errMsg(`Name required for info. Example: {"action": "info", "query": "firefox"}`)
	}
	switch source {
	case "flakes":
		example, _ := json.Marshal(map[string]any{"action": "search", "source": "flakes", "query": query})
		return errMsg(
			fmt.Sprintf("action=info is not supported for source=flakes. Use action=search instead. Example: %s.", string(example)),
		)
	case "nixos":
		switch typ {
		case "package", "packages", "option", "options":
		default:
			return errMsg("For source=nixos, type must be 'package' or 'option'. " +
				`Example: {"action": "info", "query": "services.nginx.enable", "type": "option"}`)
		}
		infoType := "package"
		if typ == "option" || typ == "options" {
			infoType = "option"
		}
		return infoNixOS(ctx, query, infoType, channel)
	case "home-manager":
		return infoHTMLSource(ctx, homeManagerURL, query)
	case "darwin":
		return infoHTMLSource(ctx, darwinURL, query)
	case "flakehub":
		return infoFlakehub(ctx, query)
	case "nixvim":
		return infoNixvim(ctx, query)
	case "wiki":
		return infoWiki(ctx, query)
	case "nix-dev":
		return infoNixdev(ctx, query)
	case "noogle":
		return infoNoogle(ctx, query)
	case "nixhub":
		return infoNixhub(ctx, query)
	default:
		return errMsg(fmt.Sprintf("Unknown source: %q. For action=info, must be one of: "+
			"nixos, home-manager, darwin, flakehub, nixvim, wiki, nix-dev, noogle, nixhub.", source))
	}
}

func dispatchStats(ctx context.Context, source, channel string) string {
	switch source {
	case "nixos":
		return statsNixOS(ctx, channel)
	case "home-manager":
		return statsHTMLSource(ctx, homeManagerURL, "Home Manager", 5000)
	case "darwin":
		return statsHTMLSource(ctx, darwinURL, "nix-darwin", 3000)
	case "flakes":
		return statsFlakes(ctx)
	case "flakehub":
		return statsFlakehub(ctx)
	case "nixvim":
		return statsNixvim(ctx)
	case "noogle":
		return statsNoogle(ctx)
	case "wiki", "nix-dev", "nixhub":
		return errMsg(fmt.Sprintf("Stats not available for source=%s.", source))
	default:
		return errMsg(fmt.Sprintf("Unknown source: %q. For action=stats, must be one of: "+
			"nixos, home-manager, darwin, flakes, flakehub, nixvim, noogle.", source))
	}
}

func dispatchBrowse(ctx context.Context, source, query string) string {
	switch source {
	case "nixos":
		return errMsg("action=browse is not for NixOS. To search NixOS options, use: " +
			`{"action": "search", "query": "nginx", "type": "options"}. ` +
			"To get a specific option's details, use: " +
			`{"action": "info", "query": "services.nginx.enable", "type": "option"}.`)
	case "home-manager":
		return browseHTMLSource(ctx, homeManagerURL, "Home Manager", query)
	case "darwin":
		return browseHTMLSource(ctx, darwinURL, "nix-darwin", query)
	case "nixvim":
		return browseNixvim(ctx, query)
	case "noogle":
		return browseNoogle(ctx, query)
	default:
		return errMsg("action=browse only supports source in: home-manager, darwin, nixvim, noogle. " +
			`Example: {"action": "browse", "query": "programs", "source": "home-manager"}`)
	}
}

func dispatchFlakeInputs(ctx context.Context, req *mcp.CallToolRequest, source, typ, query string, limit int) string {
	// source may be a flake directory path when it is not a known source name.
	explicit := ""
	if !knownSources[source] && source != "nixos" {
		explicit = source
	}
	flakeDir := rootDir(ctx, req, explicit)

	switch typ {
	case "list", "ls", "read", "packages":
	default:
		return errMsg("Type must be one of: list, ls, read for flake-inputs")
	}

	readLimit := limit
	if typ == "read" {
		if limit == 20 {
			readLimit = defaultLineLimit
		}
		if readLimit > maxLineLimit {
			readLimit = maxLineLimit
		}
	}

	switch typ {
	case "list", "packages":
		return flakeInputsList(ctx, flakeDir)
	case "ls":
		if query == "" {
			return errMsg("Query required for ls (input name or input:path)")
		}
		return flakeInputsLs(ctx, flakeDir, query)
	case "read":
		if query == "" {
			return errMsg("Query required for read (input:path format)")
		}
		return flakeInputsRead(ctx, flakeDir, query, readLimit)
	}
	return errMsg("Type must be one of: list, ls, read for flake-inputs")
}

func dispatchStore(ctx context.Context, typ, query string, limit int) string {
	switch typ {
	case "ls", "read":
	default:
		return errMsg("Type must be one of: ls, read for store. " +
			`Example: {"action": "store", "type": "ls", "query": "/nix/store/<hash>-<name>"}`)
	}
	if query == "" {
		return errMsg("Query required for store (absolute /nix/store/ path). " +
			`Example: {"action": "store", "type": "ls", "query": "/nix/store/<hash>-<name>"}`)
	}
	effLimit := limit
	if limit == 20 {
		effLimit = defaultLineLimit
	}
	if effLimit > maxLineLimit {
		effLimit = maxLineLimit
	}
	if typ == "ls" {
		return storeLs(ctx, query, effLimit)
	}
	return storeRead(ctx, query, effLimit)
}
