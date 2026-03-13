package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"odoo_method_analyzer/internal/model"
)

func Discover(root string) (model.ProjectPaths, error) {
	addonsOutput, err := getProjectData(root)
	if err == nil && addonsOutput != "" && !looksLikeError(addonsOutput) {
		paths := splitAddonPaths(root, addonsOutput)
		if len(paths.SourcePaths) > 0 {
			return paths, nil
		}
	}

	paths := fallbackPaths(root)
	if len(paths.SourcePaths) == 0 {
		return model.ProjectPaths{}, fmt.Errorf("no source addon paths found from %s", root)
	}
	return paths, nil
}

func getProjectData(root string) (string, error) {
	if _, err := exec.LookPath("get_project_data"); err != nil {
		return "", err
	}
	cmd := exec.Command("get_project_data", "-a")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func looksLikeError(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "not found") || strings.Contains(lower, "error")
}

func splitAddonPaths(root string, output string) model.ProjectPaths {
	var sourcePaths []string
	var odooPaths []string
	for _, rawPath := range strings.Split(output, ",") {
		trimmed := strings.TrimSpace(rawPath)
		if trimmed == "" {
			continue
		}
		resolved := resolvePath(root, trimmed)
		if strings.HasSuffix(filepath.ToSlash(resolved), "/customer") {
			sourcePaths = append(sourcePaths, resolved)
		} else {
			odooPaths = append(odooPaths, resolved)
		}
	}
	return model.ProjectPaths{SourcePaths: dedupeExisting(sourcePaths), OdooPaths: dedupeExisting(odooPaths)}
}

func fallbackPaths(root string) model.ProjectPaths {
	var sourcePaths []string
	var odooPaths []string

	potentialPaths := []string{
		"./code/modules/customer",
		"./code/modules/extra",
		"./modules/customer",
		"./modules/extra",
		"./addons",
	}
	for _, relative := range potentialPaths {
		resolved := resolvePath(root, relative)
		if !isDir(resolved) {
			continue
		}
		if strings.HasSuffix(filepath.ToSlash(resolved), "/customer") {
			sourcePaths = append(sourcePaths, resolved)
		} else {
			odooPaths = append(odooPaths, resolved)
		}
	}

	globPatterns := []string{
		"../odoo*/odoo/addons",
		"../odoo*/addons",
		"./odoo/addons",
		"../odoo*/enterprise",
		"./enterprise",
		"../odoo*/themes",
		"./themes",
	}
	for _, pattern := range globPatterns {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, match := range matches {
			if isDir(match) {
				odooPaths = append(odooPaths, filepath.Clean(match))
			}
		}
	}

	return model.ProjectPaths{SourcePaths: dedupeExisting(sourcePaths), OdooPaths: dedupeExisting(odooPaths)}
}

func resolvePath(root string, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(root, path))
}

func dedupeExisting(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		cleanPath := filepath.Clean(path)
		if _, ok := seen[cleanPath]; ok || !isDir(cleanPath) {
			continue
		}
		seen[cleanPath] = struct{}{}
		result = append(result, cleanPath)
	}
	sort.Strings(result)
	return result
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
