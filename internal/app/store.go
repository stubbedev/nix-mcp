package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// validateStoreQuery checks a user-supplied store path. Returns (path, errString).
func validateStoreQuery(query string) (string, string) {
	if strings.TrimSpace(query) == "" {
		return "", errCode("INVALID_PATH", "Store path required (e.g., '/nix/store/<hash>-<name>')")
	}
	path := strings.TrimSpace(query)
	if !strings.HasPrefix(path, "/") {
		return "", errCode("INVALID_PATH", fmt.Sprintf("Absolute store path required, got %q", query))
	}
	if !validateStorePath(path) {
		return "", errCode("INVALID_PATH", fmt.Sprintf("Invalid store path: must stay within /nix/store/, got %q", query))
	}
	return path, ""
}

type fileEntry struct {
	name    string
	size    int64
	hasSize bool
}

func scanDirectory(target string) (dirs []string, files []fileEntry, err error) {
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		st, statErr := os.Stat(filepath.Join(target, name))
		if statErr != nil {
			files = append(files, fileEntry{name: name})
			continue
		}
		if st.IsDir() {
			dirs = append(dirs, name)
		} else {
			files = append(files, fileEntry{name: name, size: st.Size(), hasSize: true})
		}
	}
	return dirs, files, nil
}

func storeLs(_ context.Context, query string, limit int) string {
	target, errStr := validateStoreQuery(query)
	if errStr != "" {
		return errStr
	}
	info, err := os.Stat(target)
	if err != nil {
		return errCode("NOT_FOUND", "Path not found: "+target)
	}
	if !info.IsDir() {
		return errCode("NOT_DIRECTORY", "Not a directory: "+target)
	}
	dirs, files, err := scanDirectory(target)
	if err != nil {
		if os.IsPermission(err) {
			return errCode("PERMISSION_ERROR", "Permission denied: "+target)
		}
		return errCode("OS_ERROR", "Cannot list directory: "+err.Error())
	}
	totalDirs, totalFiles := len(dirs), len(files)
	total := totalDirs + totalFiles
	if total == 0 {
		return fmt.Sprintf("Directory '%s' is empty.", target)
	}

	shownDirs := dirs
	if len(shownDirs) > limit {
		shownDirs = shownDirs[:limit]
	}
	remaining := limit - len(shownDirs)
	if remaining < 0 {
		remaining = 0
	}
	shownFiles := files
	if len(shownFiles) > remaining {
		shownFiles = shownFiles[:remaining]
	}
	shownTotal := len(shownDirs) + len(shownFiles)

	header := fmt.Sprintf("Contents of %s (%d dirs, %d files):", target, totalDirs, totalFiles)
	if shownTotal < total {
		header += fmt.Sprintf(" showing %d of %d", shownTotal, total)
	}
	lines := []string{header, ""}
	for _, d := range shownDirs {
		lines = append(lines, "  "+d+"/")
	}
	for _, f := range shownFiles {
		if f.hasSize {
			lines = append(lines, fmt.Sprintf("  %s (%s)", f.name, formatSize(f.size)))
		} else {
			lines = append(lines, "  "+f.name)
		}
	}
	return strings.Join(lines, "\n")
}

func storeRead(_ context.Context, query string, limit int) string {
	target, errStr := validateStoreQuery(query)
	if errStr != "" {
		return errStr
	}
	info, err := os.Stat(target)
	if err != nil {
		if os.IsPermission(err) {
			return errCode("PERMISSION_ERROR", "Permission denied: "+target)
		}
		return errCode("NOT_FOUND", "File not found: "+target)
	}
	if info.IsDir() {
		return errCode("IS_DIRECTORY", fmt.Sprintf("'%s' is a directory. Use type='ls' to list contents.", target))
	}
	if info.Size() > maxFileSize {
		return errCode("FILE_TOO_LARGE", fmt.Sprintf("File too large: %s (max %s)", formatSize(info.Size()), formatSize(maxFileSize)))
	}
	if isBinaryFile(target) {
		return errCode("BINARY_FILE", fmt.Sprintf("Binary file detected: %s (%s)", target, formatSize(info.Size())))
	}
	lines, totalLines, err := readFileWithLimit(target, limit)
	if err != nil {
		if os.IsPermission(err) {
			return errCode("PERMISSION_ERROR", "Permission denied: "+target)
		}
		return errCode("OS_ERROR", "Cannot read file: "+err.Error())
	}
	header := []string{"File: " + target, "Size: " + formatSize(info.Size()), ""}
	if totalLines > limit {
		header = append(header, fmt.Sprintf("(Showing %d of %d lines)", limit, totalLines), "")
	}
	return strings.Join(append(header, lines...), "\n")
}
