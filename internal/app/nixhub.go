package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var hexCommit = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

func nixhubHeaders() map[string]string {
	return map[string]string{"Accept": "application/json", "User-Agent": userAgent()}
}

// fetchNixhubPkg GETs v1/pkg (array of version records). Returns (errString, data).
func fetchNixhubPkg(ctx context.Context, name string) (string, []map[string]any) {
	status, body, err := httpGet(ctx, nixhubAPI+"/v1/pkg", map[string]string{"name": name}, nixhubHeaders(), 15*time.Second)
	if err != nil {
		return nixhubTransportErr(err), nil
	}
	switch {
	case status == 400 || status == 404:
		return errCode("NOT_FOUND", fmt.Sprintf("Package '%s' not found", name)), nil
	case status >= 500:
		return errCode("SERVICE_ERROR", "NixHub API temporarily unavailable"), nil
	case status < 200 || status >= 300:
		return errCode("API_ERROR", fmt.Sprintf("NixHub API error: HTTP %d", status)), nil
	}
	var data []map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return errMsg(err.Error()), nil
	}
	return "", data
}

func fetchNixhubResolve(ctx context.Context, name, version string) (map[string]any, int, error) {
	if version == "" {
		version = "latest"
	}
	status, body, err := httpGet(ctx, nixhubAPI+"/v2/resolve",
		map[string]string{"name": name, "version": version}, nixhubHeaders(), 15*time.Second)
	if err != nil {
		return nil, 0, err
	}
	var data map[string]any
	if status >= 200 && status < 300 {
		_ = json.Unmarshal(body, &data)
	}
	return data, status, nil
}

func nixhubTransportErr(err error) string {
	if apiErrCode(err) == "TIMEOUT" {
		return errCode("TIMEOUT", "NixHub API timed out")
	}
	return errCode("API_ERROR", "NixHub API error: "+err.Error())
}

func searchNixhub(ctx context.Context, query string, limit int) string {
	status, body, err := httpGet(ctx, nixhubAPI+"/v2/search", map[string]string{"q": query}, nixhubHeaders(), 15*time.Second)
	if err != nil {
		return nixhubTransportErr(err)
	}
	if status >= 500 {
		return errCode("SERVICE_ERROR", "NixHub API temporarily unavailable")
	}
	if status < 200 || status >= 300 {
		return errCode("API_ERROR", fmt.Sprintf("NixHub API error: HTTP %d", status))
	}
	var data struct {
		TotalResults int              `json:"total_results"`
		Results      []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return errMsg(err.Error())
	}
	packages := data.Results
	if len(packages) == 0 {
		return fmt.Sprintf("No packages found on NixHub matching '%s'", query)
	}
	if len(packages) > limit {
		packages = packages[:limit]
	}
	total := data.TotalResults
	if total == 0 {
		total = len(packages)
	}
	results := []string{fmt.Sprintf("Found %d of %d packages on NixHub matching '%s':\n", len(packages), total, query)}
	for _, pkg := range packages {
		results = append(results, "* "+srcStr(pkg, "name"))
		if v := srcStr(pkg, "version"); v != "" {
			results = append(results, "  Version: "+v)
		}
		summary := srcStr(pkg, "summary")
		if summary == "" {
			summary = srcStr(pkg, "description")
		}
		if summary != "" {
			results = append(results, "  "+truncate(summary, 200))
		}
		if lu := srcStr(pkg, "last_updated"); lu != "" {
			if t, ok := parseISOTime(lu); ok {
				results = append(results, "  Updated: "+t.Format("2006-01-02"))
			}
		}
		results = append(results, "")
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}

func infoNixhub(ctx context.Context, name string) string {
	errStr, pkgArray := fetchNixhubPkg(ctx, name)
	if errStr != "" {
		return errStr
	}
	if len(pkgArray) == 0 {
		return errCode("NOT_FOUND", fmt.Sprintf("Package '%s' not found", name))
	}
	pkg := pkgArray[0]
	version := srcStr(pkg, "version")
	if version == "" {
		version = "latest"
	}

	flakeRef, storePaths := nixhubResolveRefs(ctx, name, version)

	results := []string{"Package: " + firstNonEmpty(srcStr(pkg, "name"), name)}
	if version != "" {
		results = append(results, "Version: "+version)
	}
	summary := srcStr(pkg, "summary")
	if summary != "" {
		results = append(results, "Summary: "+summary)
	}
	description := srcStr(pkg, "description")
	if description != "" && description != summary {
		results = append(results, "Description: "+truncate(description, 500))
	}
	results = append(results, "")
	if lic := srcStr(pkg, "license"); lic != "" {
		results = append(results, "License: "+lic)
	}
	if hp := srcStr(pkg, "homepage"); hp != "" {
		results = append(results, "Homepage: "+hp)
	}
	if progs := nixhubPrograms(pkg); len(progs) > 0 {
		results = append(results, "Programs: "+formatProgramList(progs))
	}
	if platforms := srcStrList(pkg, "platforms"); len(platforms) > 0 {
		sorted := append([]string(nil), platforms...)
		sort.Strings(sorted)
		results = append(results, "Platforms: "+strings.Join(sorted, ", "))
	}
	if flakeRef != "" {
		results = append(results, "", "Flake Reference:", "  "+flakeRef)
	}
	if len(storePaths) > 0 {
		results = append(results, "", "Store Paths:")
		keys := make([]string, 0, len(storePaths))
		for k := range storePaths {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			results = append(results, fmt.Sprintf("  %s: %s", k, storePaths[k]))
		}
	}
	return strings.Join(results, "\n")
}

// nixhubDefaultOutputPath returns the default (or first) output store path.
func nixhubDefaultOutputPath(sysInfo map[string]any) string {
	outputs := getList(sysInfo, "outputs")
	if len(outputs) == 0 {
		return ""
	}
	var first map[string]any
	for i, o := range outputs {
		om, _ := o.(map[string]any)
		if om == nil {
			continue
		}
		if i == 0 {
			first = om
		}
		if d, ok := om["default"].(bool); ok && d {
			return srcStr(om, "path")
		}
	}
	if first != nil {
		return srcStr(first, "path")
	}
	return ""
}

// nixhubResolveRefs fetches v2/resolve and extracts the flake reference and
// per-system store paths. Failures (network/non-2xx) yield empty results.
func nixhubResolveRefs(ctx context.Context, name, version string) (string, map[string]string) {
	storePaths := map[string]string{}
	resolve, status, _ := fetchNixhubResolve(ctx, name, version)
	if status < 200 || status >= 300 || resolve == nil {
		return "", storePaths
	}
	systems := getMap(resolve, "systems")
	if systems == nil {
		return "", storePaths
	}
	flakeRef := ""
	for sysName, v := range systems {
		sysInfo, _ := v.(map[string]any)
		if sysInfo == nil {
			continue
		}
		if flakeRef == "" {
			flakeRef = nixhubFlakeRef(sysInfo)
		}
		if path := nixhubDefaultOutputPath(sysInfo); path != "" {
			storePaths[sysName] = path
		}
	}
	return flakeRef, storePaths
}

// nixhubFlakeRef builds a github:owner/repo/rev#attr ref from a system's
// flake_installable, or "" when it isn't a github ref.
func nixhubFlakeRef(sysInfo map[string]any) string {
	fi := getMap(sysInfo, "flake_installable")
	if fi == nil {
		return ""
	}
	ref := getMap(fi, "ref")
	if ref == nil || srcStr(ref, "type") != "github" {
		return ""
	}
	owner, repo, rev := srcStr(ref, "owner"), srcStr(ref, "repo"), srcStr(ref, "rev")
	if len(rev) > 8 {
		rev = rev[:8]
	}
	if owner == "" || repo == "" {
		return ""
	}
	return fmt.Sprintf("github:%s/%s/%s#%s", owner, repo, rev, srcStr(fi, "attr_path"))
}

// nixhubPrograms pulls the program list from a pkg's systems dict.
func nixhubPrograms(pkg map[string]any) []string {
	systems := getMap(pkg, "systems")
	if systems == nil {
		return nil
	}
	for _, v := range systems {
		if sysInfo, ok := v.(map[string]any); ok {
			if progs := srcStrList(sysInfo, "programs"); len(progs) > 0 {
				return progs
			}
		}
	}
	return nil
}

func formatProgramList(programs []string) string {
	progs := programs
	if len(progs) > 10 {
		progs = progs[:10]
	}
	s := strings.Join(progs, ", ")
	if len(programs) > 10 {
		s += fmt.Sprintf(" ... (%d total)", len(programs))
	}
	return s
}

// ── binary cache check ───────────────────────────────────────────────────────

func checkBinaryCache(ctx context.Context, name, version, system string) string {
	if version == "" {
		version = "latest"
	}
	data, status, err := fetchNixhubResolve(ctx, name, version)
	if err != nil {
		return nixhubTransportErr(err)
	}
	switch {
	case status == 400 || status == 404:
		return errCode("NOT_FOUND", fmt.Sprintf("Package '%s' not found", name))
	case status >= 500:
		return errCode("SERVICE_ERROR", "NixHub API temporarily unavailable")
	case status < 200 || status >= 300:
		return errCode("API_ERROR", fmt.Sprintf("NixHub API error: HTTP %d", status))
	}
	if data == nil {
		return errCode("API_ERROR", "Invalid response from NixHub")
	}

	pkgName := firstNonEmpty(srcStr(data, "name"), name)
	pkgVersion := firstNonEmpty(srcStr(data, "version"), version)
	systemsData := getMap(data, "systems")
	if systemsData == nil {
		return errCode("API_ERROR", "Invalid systems data from NixHub")
	}

	type sysEntry struct{ name, storePath string }
	var systems []sysEntry
	allSysNames := make([]string, 0, len(systemsData))
	for sysName := range systemsData {
		allSysNames = append(allSysNames, sysName)
	}
	sort.Strings(allSysNames)
	for _, sysName := range allSysNames {
		sysInfo, _ := systemsData[sysName].(map[string]any)
		if sysInfo == nil {
			continue
		}
		systems = append(systems, sysEntry{sysName, nixhubDefaultOutputPath(sysInfo)})
	}
	if len(systems) == 0 {
		return errCode("NOT_FOUND", fmt.Sprintf("No systems found for %s@%s", name, pkgVersion))
	}
	if system != "" {
		filtered := systems[:0]
		for _, s := range systems {
			if s.name == system {
				filtered = append(filtered, s)
			}
		}
		systems = filtered
		if len(systems) == 0 {
			return errCode("NOT_FOUND", fmt.Sprintf("System '%s' not available. Available: %s", system, strings.Join(allSysNames, ", ")))
		}
	}

	results := []string{fmt.Sprintf("Binary Cache Status: %s@%s", pkgName, pkgVersion), ""}
	// Check each system's cache status concurrently (matches the Python
	// implementation); collect by index to keep the output order stable.
	sysResults := make([][]string, len(systems))
	var wg sync.WaitGroup
	for i, s := range systems {
		wg.Go(func() {
			sysResults[i] = checkSystemCache(ctx, s.name, s.storePath)
		})
	}
	wg.Wait()
	for _, sr := range sysResults {
		results = append(results, sr...)
	}
	return strings.TrimSpace(strings.Join(results, "\n"))
}

func checkSystemCache(ctx context.Context, sysName, storePath string) []string {
	results := []string{"System: " + sysName}
	if storePath == "" {
		return append(results, "  Store path: Not available", "  Status: UNKNOWN", "")
	}
	results = append(results, "  Store path: "+storePath)

	storeHash := ""
	parts := strings.Split(storePath, "/")
	if len(parts) >= 4 {
		storeHash = strings.SplitN(parts[3], "-", 2)[0]
	}
	if len(storeHash) != 32 {
		return append(results, "  Status: UNKNOWN (invalid store path)", "")
	}

	// One GET (not HEAD+GET): the narinfo body is small and gives us the sizes
	// in the same round-trip, halving requests per system.
	narURL := fmt.Sprintf("%s/%s.narinfo", cacheNixosOrg, storeHash)
	status, body, err := httpGet(ctx, narURL, nil, nil, 5*time.Second)
	switch {
	case err != nil:
		results = append(results, "  Status: UNKNOWN (cache check failed)")
	case status == 200:
		ni := parseNarInfo(string(body))
		results = append(results, "  Status: CACHED")
		if ni.hasFileSize {
			results = append(results, "  Download size: "+formatSize(ni.fileSize))
		}
		if ni.hasNarSize {
			results = append(results, "  Unpacked size: "+formatSize(ni.narSize))
		}
		if ni.compression != "" {
			results = append(results, "  Compression: "+ni.compression)
		}
	case status == 404:
		results = append(results, "  Status: NOT CACHED")
	default:
		results = append(results, fmt.Sprintf("  Status: UNKNOWN (HTTP %d)", status))
	}
	return append(results, "")
}

// ── release formatting (used by nix_versions) ────────────────────────────────

func formatRelease(release map[string]any) []string {
	var results []string
	version := srcStr(release, "version")
	if version == "" {
		version = "unknown"
	}
	results = append(results, "* "+version)

	if lu, ok := release["last_updated"]; ok && lu != nil {
		switch n := lu.(type) {
		case float64:
			results = append(results, "  Updated: "+time.Unix(int64(n), 0).UTC().Format("2006-01-02"))
		case string:
			if t, ok := parseISOTime(n); ok {
				results = append(results, "  Updated: "+t.Format("2006-01-02"))
			}
		}
	}

	platformSystems := map[string]bool{}
	for _, p := range getList(release, "platforms") {
		switch v := p.(type) {
		case string:
			platformSystems[v] = true
		case map[string]any:
			if s := srcStr(v, "system"); s != "" {
				platformSystems[s] = true
			}
		}
	}
	if len(platformSystems) > 0 {
		hasLinux, hasDarwin := false, false
		all := make([]string, 0, len(platformSystems))
		for s := range platformSystems {
			all = append(all, s)
			if strings.Contains(s, "linux") {
				hasLinux = true
			}
			if strings.Contains(s, "darwin") {
				hasDarwin = true
			}
		}
		switch {
		case hasLinux && hasDarwin:
			results = append(results, "  Platforms: Linux and macOS")
		case hasLinux:
			results = append(results, "  Platforms: Linux")
		case hasDarwin:
			results = append(results, "  Platforms: macOS")
		default:
			sort.Strings(all)
			results = append(results, "  Platforms: "+strings.Join(all, ", "))
		}
	}

	commit := srcStr(release, "commit_hash")
	if commit != "" && hexCommit.MatchString(commit) {
		results = append(results, "  Nixpkgs commit: "+commit)
		if attr := firstAttrPath(release); attr != "" {
			results = append(results, "  Attribute: "+attr)
		}
	}
	return results
}

// firstAttrPath returns the first attr_path from a release's systems dict.
func firstAttrPath(release map[string]any) string {
	systems := getMap(release, "systems")
	if systems == nil {
		return ""
	}
	for _, v := range systems {
		if sysInfo, ok := v.(map[string]any); ok {
			if ap := getList(sysInfo, "attr_paths"); len(ap) > 0 {
				return fmt.Sprint(ap[0])
			}
		}
	}
	return ""
}
