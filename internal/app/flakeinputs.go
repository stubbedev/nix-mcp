package app

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func checkNixAvailable() bool {
	_, err := exec.LookPath("nix")
	return err == nil
}

// runNixCommand runs `nix --extra-experimental-features "nix-command flakes" <args...>`
// in cwd. Returns (ok, stdout, stderr).
func runNixCommand(ctx context.Context, cwd string, args ...string) (bool, string, string) {
	full := append([]string{"--extra-experimental-features", "nix-command flakes"}, args...)
	cmd := exec.CommandContext(ctx, "nix", full...)
	cmd.Dir = cwd
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return false, "", "Command timed out"
	}
	return err == nil, stdout.String(), stderr.String()
}

type flakeArchive struct {
	Path   string                  `json:"path"`
	Inputs map[string]flakeArchive `json:"inputs"`
}

func getFlakeInputs(ctx context.Context, flakeDir string) (*flakeArchive, string) {
	if _, err := os.Stat(filepath.Join(flakeDir, "flake.nix")); err != nil {
		return nil, fmt.Sprintf("Not a flake directory: %s (no flake.nix found)", flakeDir)
	}
	ok, stdout, stderr := runNixCommand(ctx, flakeDir, "flake", "archive", "--json")
	if !ok {
		low := strings.ToLower(stderr)
		switch {
		case strings.Contains(low, "experimental feature"):
			return nil, "Flakes not enabled. Enable with: nix-command flakes experimental features"
		case strings.Contains(stderr, "does not provide attribute"):
			return nil, "Invalid flake: " + strings.TrimSpace(stderr)
		default:
			return nil, "Failed to get flake inputs: " + strings.TrimSpace(stderr)
		}
	}
	var data flakeArchive
	if err := json.Unmarshal([]byte(stdout), &data); err != nil {
		return nil, "Failed to parse flake archive output: " + err.Error()
	}
	return &data, ""
}

// flattenInputs maps input names ("nixpkgs", "flake-parts.nixpkgs-lib") to store paths.
func flattenInputs(data *flakeArchive, prefix string) map[string]string {
	result := map[string]string{}
	for name, info := range data.Inputs {
		full := name
		if prefix != "" {
			full = prefix + "." + name
		}
		if info.Path != "" {
			result[full] = info.Path
		}
		if len(info.Inputs) > 0 {
			child := info
			maps.Copy(result, flattenInputs(&child, full))
		}
	}
	return result
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func availableInputsMsg(inputs map[string]string) string {
	keys := sortedKeys(inputs)
	n := len(keys)
	shown := keys
	if n > 10 {
		shown = keys[:10]
	}
	msg := strings.Join(shown, ", ")
	if n > 10 {
		msg += fmt.Sprintf(" ... and %d more", n-10)
	}
	return msg
}

func flakeInputsList(ctx context.Context, flakeDir string) string {
	if !checkNixAvailable() {
		return errCode("NIX_NOT_FOUND", "Nix is not installed or not in PATH")
	}
	data, errMsgStr := getFlakeInputs(ctx, flakeDir)
	if errMsgStr != "" {
		return errCode("FLAKE_ERROR", errMsgStr)
	}
	inputs := flattenInputs(data, "")
	if len(inputs) == 0 {
		return "No inputs found for this flake."
	}
	flakePath := data.Path
	if flakePath == "" {
		flakePath = flakeDir
	}
	lines := []string{fmt.Sprintf("Flake inputs (%d found):", len(inputs)), "Flake path: " + flakePath, ""}
	for _, name := range sortedKeys(inputs) {
		lines = append(lines, "* "+name, "  "+inputs[name], "")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func flakeInputsLs(ctx context.Context, flakeDir, query string) string {
	if !checkNixAvailable() {
		return errCode("NIX_NOT_FOUND", "Nix is not installed or not in PATH")
	}
	inputName, subpath := query, ""
	if before, after, ok0 := strings.Cut(query, ":"); ok0 {
		inputName = before
		subpath = strings.TrimLeft(after, "/")
	}
	data, errMsgStr := getFlakeInputs(ctx, flakeDir)
	if errMsgStr != "" {
		return errCode("FLAKE_ERROR", errMsgStr)
	}
	inputs := flattenInputs(data, "")
	storePath, ok := inputs[inputName]
	if !ok {
		return errCode("NOT_FOUND", fmt.Sprintf("Input '%s' not found. Available: %s", inputName, availableInputsMsg(inputs)))
	}
	target := storePath
	if subpath != "" {
		target = filepath.Join(storePath, subpath)
	}
	if !validateStorePath(target) {
		return errCode("SECURITY_ERROR", "Invalid path: must stay within /nix/store/")
	}
	info, err := os.Stat(target)
	if err != nil {
		return errCode("NOT_FOUND", fmt.Sprintf("Path not found: %s in %s", orSlash(subpath), inputName))
	}
	if !info.IsDir() {
		return errCode("NOT_DIRECTORY", fmt.Sprintf("Not a directory: %s in %s", orSlash(subpath), inputName))
	}
	dirs, files, err := scanDirectory(target)
	if err != nil {
		if os.IsPermission(err) {
			return errCode("PERMISSION_ERROR", "Permission denied: "+orSlash(subpath))
		}
		return errCode("OS_ERROR", "Cannot list directory: "+err.Error())
	}
	if len(dirs)+len(files) == 0 {
		return fmt.Sprintf("Directory '%s' in %s is empty.", orSlash(subpath), inputName)
	}
	display := inputName
	if subpath != "" {
		display = inputName + ":" + subpath
	}
	lines := []string{fmt.Sprintf("Contents of %s (%d dirs, %d files):", display, len(dirs), len(files)), ""}
	for _, d := range dirs {
		lines = append(lines, "  "+d+"/")
	}
	for _, f := range files {
		if f.hasSize {
			lines = append(lines, fmt.Sprintf("  %s (%s)", f.name, formatSize(f.size)))
		} else {
			lines = append(lines, "  "+f.name)
		}
	}
	return strings.Join(lines, "\n")
}

func flakeInputsRead(ctx context.Context, flakeDir, query string, limit int) string {
	if !checkNixAvailable() {
		return errCode("NIX_NOT_FOUND", "Nix is not installed or not in PATH")
	}
	inputName, rawPath, ok := strings.Cut(query, ":")
	if !ok {
		return errCode("INVALID_FORMAT", "Read requires 'input:path' format (e.g., 'nixpkgs:flake.nix')")
	}
	filePath := strings.TrimLeft(rawPath, "/")
	if filePath == "" {
		return errCode("INVALID_FORMAT", "File path required (e.g., 'nixpkgs:flake.nix')")
	}
	data, errMsgStr := getFlakeInputs(ctx, flakeDir)
	if errMsgStr != "" {
		return errCode("FLAKE_ERROR", errMsgStr)
	}
	inputs := flattenInputs(data, "")
	storePath, ok := inputs[inputName]
	if !ok {
		return errCode("NOT_FOUND", fmt.Sprintf("Input '%s' not found. Available: %s", inputName, availableInputsMsg(inputs)))
	}
	target := filepath.Join(storePath, filePath)
	if !validateStorePath(target) {
		return errCode("SECURITY_ERROR", "Invalid path: must stay within /nix/store/")
	}
	info, err := os.Stat(target)
	if err != nil {
		return errCode("NOT_FOUND", fmt.Sprintf("File not found: %s in %s", filePath, inputName))
	}
	if info.IsDir() {
		return errCode("IS_DIRECTORY", fmt.Sprintf("'%s' is a directory. Use type='ls' to list contents.", filePath))
	}
	if info.Size() > maxFileSize {
		return errCode("FILE_TOO_LARGE", fmt.Sprintf("File too large: %s (max %s)", formatSize(info.Size()), formatSize(maxFileSize)))
	}
	if isBinaryFile(target) {
		return errCode("BINARY_FILE", fmt.Sprintf("Binary file detected: %s (%s)", filePath, formatSize(info.Size())))
	}
	lines, totalLines, err := readFileWithLimit(target, limit)
	if err != nil {
		if os.IsPermission(err) {
			return errCode("PERMISSION_ERROR", "Permission denied: "+filePath)
		}
		return errCode("OS_ERROR", "Cannot read file: "+err.Error())
	}
	header := []string{fmt.Sprintf("File: %s:%s", inputName, filePath), "Size: " + formatSize(info.Size()), ""}
	if totalLines > limit {
		header = append(header, fmt.Sprintf("(Showing %d of %d lines)", limit, totalLines), "")
	}
	return strings.Join(append(header, lines...), "\n")
}

func orSlash(s string) string {
	if s == "" {
		return "/"
	}
	return s
}
