package app

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var pkgNameRe = regexp.MustCompile(`^[a-zA-Z0-9\-_.]+$`)

func nixVersions(ctx context.Context, args map[string]any) string {
	pkg := strings.TrimSpace(argString(args, "package"))
	version := argString(args, "version")
	limit := argInt(args, "limit", 10)

	if pkg == "" {
		return errMsg("Package name required")
	}
	if !pkgNameRe.MatchString(pkg) {
		return errMsg("Invalid package name")
	}
	if limit < 1 || limit > 50 {
		return errMsg("Limit must be 1-50")
	}

	errStr, releases := fetchNixhubPkg(ctx, pkg)
	if errStr != "" {
		return errStr
	}
	if len(releases) == 0 {
		return errCode("NOT_FOUND", fmt.Sprintf("Package '%s' not found", pkg))
	}

	if version != "" {
		for _, release := range releases {
			if srcStr(release, "version") == version {
				lines := []string{fmt.Sprintf("Found %s version %s\n", pkg, version)}
				commit := srcStr(release, "commit_hash")
				if commit != "" && hexCommit.MatchString(commit) {
					lines = append(lines, "Nixpkgs commit: "+commit)
					if attr := firstAttrPath(release); attr != "" {
						lines = append(lines, "  Attribute: "+attr)
					}
				}
				return strings.Join(lines, "\n")
			}
		}
		var avail []string
		for i, r := range releases {
			if i >= limit {
				break
			}
			avail = append(avail, srcStr(r, "version"))
		}
		return fmt.Sprintf("Version %s not found for %s\nAvailable: %s", version, pkg, strings.Join(avail, ", "))
	}

	results := []string{"Package: " + pkg}
	latest := releases[0]
	if lic := srcStr(latest, "license"); lic != "" {
		results = append(results, "License: "+lic)
	}
	if hp := srcStr(latest, "homepage"); hp != "" {
		results = append(results, "Homepage: "+hp)
	}
	if progs := nixhubPrograms(latest); len(progs) > 0 {
		results = append(results, "Programs: "+formatProgramList(progs))
	}
	results = append(results, fmt.Sprintf("Total versions: %d", len(releases)))
	results = append(results, "")

	shown := releases
	if len(shown) > limit {
		shown = shown[:limit]
	}
	results = append(results, fmt.Sprintf("Recent versions (%d of %d):\n", len(shown), len(releases)))
	for _, release := range shown {
		results = append(results, formatRelease(release)...)
		results = append(results, "")
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}
