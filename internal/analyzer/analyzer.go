package analyzer

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"odoo_method_analyzer/internal/model"
	"odoo_method_analyzer/internal/ui"
)

const maxFileSize = 1 << 20

var (
	classRe          = regexp.MustCompile(`^class\s+([A-Za-z_][A-Za-z0-9_]*)`)
	defRe            = regexp.MustCompile(`^\s*def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	routeDecoratorRe = regexp.MustCompile(`@(?:http\.)?route\b`)
	routeStringRe    = regexp.MustCompile(`['"](\/[^'"]*)['"]`)
	apiDecoratorRe   = regexp.MustCompile(`@api\.(onchange|constrains|depends)`)
	superCallRe      = regexp.MustCompile(`super\(\s*[^)]*\)\s*\.\s*([A-Za-z_][A-Za-z0-9_]*)\s*[\(\.]`)
	dynamicPercentRe = regexp.MustCompile(`getattr\s*\(\s*[^,]+,\s*['"]([A-Za-z_][A-Za-z0-9_]*)%s['"]`)
	dynamicFormatRe  = regexp.MustCompile(`getattr\s*\(\s*[^,]+,\s*['"]([A-Za-z_][A-Za-z0-9_]*)\{\}['"]\.format\(`)
	dynamicFStringRe = regexp.MustCompile(`getattr\s*\(\s*[^,]+,\s*f['"]([A-Za-z_][A-Za-z0-9_]*)\{[^}]*\}['"]`)
)

type searchFile struct {
	Path     string
	Language string
	Lines    []string
	Content  string
}

type dynamicPrefix struct {
	Prefix   string
	FilePath string
	Line     int
}

// langPattern holds a compiled regex check for a single usage pattern.
type langPattern struct {
	reason  string
	re      *regexp.Regexp
	skipDef bool // skip the method's own definition line (Python only)
}

// methodPatterns caches all compiled regex patterns for one method name.
// Build it once per method via buildMethodPatterns and reuse across all files.
type methodPatterns struct {
	python []langPattern
	js     []langPattern
	xml    []langPattern
}

// buildMethodPatterns compiles every regex needed to search for methodName
// across Python, JavaScript and XML files. Call once per method, not per file.
func buildMethodPatterns(methodName string) methodPatterns {
	q := regexp.QuoteMeta(methodName)
	return methodPatterns{
		python: []langPattern{
			{reason: "method_call", re: regexp.MustCompile(`\.` + q + `\s*\(`), skipDef: true},
			{reason: "method_assignment", re: regexp.MustCompile(`=.*\.` + q + `\s*($|[^\w(])`), skipDef: true},
			{reason: "direct_function_call", re: regexp.MustCompile(`\b` + q + `\s*\(`), skipDef: true},
			{reason: "string_reference", re: regexp.MustCompile(`(compute|inverse|search|default|onchange|constraint|depends)\s*=\s*['"]_?` + q + `['"]`)},
			{reason: "decorator_reference", re: regexp.MustCompile(`@api\.(depends|constrains|onchange)\s*\([^)]*['"]_?` + q + `['"]`)},
			{reason: "search_attribute", re: regexp.MustCompile(`search\s*=\s*['"]` + q + `['"]`)},
			{reason: "general_string_reference", re: regexp.MustCompile(`['"]_?` + q + `['"]`)},
			{reason: "default_assignment", re: regexp.MustCompile(`default\s*=\s*` + q + `\b`)},
		},
		js: []langPattern{
			{reason: "direct_call", re: regexp.MustCompile(`(?:\.|\b)` + q + `\s*(?:\(|:)`)},
			{reason: "string_reference", re: regexp.MustCompile(`["']` + q + `["']`)},
		},
		xml: []langPattern{
			{reason: "attribute_reference", re: regexp.MustCompile(`(name|string|context|attrs|invisible|readonly|required|domain)\s*=\s*["'][^"']*\b` + q + `\b`)},
			{reason: "text_reference", re: regexp.MustCompile(`\b` + q + `\b`)},
		},
	}
}

func Run(ctx context.Context, cfg model.Config, paths model.ProjectPaths, printer *ui.Printer) (model.Result, error) {
	result := model.Result{
		Timestamp:    time.Now(),
		Root:         cfg.Root,
		IncludeTests: cfg.IncludeTests,
		SourcePaths:  append([]string(nil), paths.SourcePaths...),
		OdooPaths:    append([]string(nil), paths.OdooPaths...),
	}

	printer.Step(1, 5, "Collecting source files")
	sourceFiles, err := collectFiles(ctx, paths.SourcePaths, cfg.IncludeTests, map[string]string{".py": "python"}, printer, "Loading source files")
	if err != nil {
		return result, err
	}
	printer.Success(fmt.Sprintf("Loaded %d source Python files", len(sourceFiles)))

	printer.Step(2, 5, "Extracting methods and super() calls")
	superCalls := findSuperCalls(sourceFiles)
	overrideSet := make(map[string]struct{}, len(superCalls))
	for _, call := range superCalls {
		overrideSet[methodKey(call.FilePath, call.MethodName)] = struct{}{}
	}
	methods := extractMethods(sourceFiles, overrideSet)
	// Partition route methods immediately — they are not searched for code usage.
	regularMethods := make([]model.MethodDef, 0, len(methods))
	routeMethodDefs := make([]model.MethodDef, 0)
	for _, m := range methods {
		if m.IsRoute {
			routeMethodDefs = append(routeMethodDefs, m)
		} else {
			regularMethods = append(regularMethods, m)
		}
	}
	result.TotalMethods = len(methods)
	if len(methods) == 0 {
		return result, nil
	}
	printer.Success(fmt.Sprintf("Found %d methods (%d routes)", len(methods), len(routeMethodDefs)))

	printer.Step(3, 5, "Building search corpus")
	// One combined walk over all paths for all extensions.
	// Source Python files (step 1) are merged in directly — no re-read.
	// Odoo Python files are derived by filtering the combined result by path prefix.
	allPaths := append(append([]string(nil), paths.SourcePaths...), paths.OdooPaths...)

	// Launch the dino game while the user waits for the corpus to be indexed.
	var cancelGame context.CancelFunc
	if printer.Interactive {
		var gameCtx context.Context
		gameCtx, cancelGame = context.WithCancel(ctx)
		go ui.RunDinoGame(gameCtx)
	}

	odooAndJsXmlFiles, err := collectFiles(ctx, allPaths, cfg.IncludeTests, map[string]string{".py": "python", ".js": "javascript", ".xml": "xml"}, printer, "Building search corpus")
	if cancelGame != nil {
		cancelGame() // corpus ready — signal game to show "loading done" and stop
	}
	if err != nil {
		return result, err
	}
	// Exclude source Python files (already in sourceFiles) to avoid duplicates,
	// then partition Odoo Python files for step 5.
	sourcePathSet := make(map[string]struct{}, len(paths.SourcePaths))
	for _, p := range paths.SourcePaths {
		sourcePathSet[filepath.Clean(p)] = struct{}{}
	}
	isSourcePath := func(filePath string) bool {
		for sp := range sourcePathSet {
			if strings.HasPrefix(filePath, sp+string(filepath.Separator)) || filePath == sp {
				return true
			}
		}
		return false
	}
	var odooPythonFiles []searchFile
	extraFiles := odooAndJsXmlFiles[:0:0] // reuse backing array type, but start fresh
	for _, f := range odooAndJsXmlFiles {
		if f.Language == "python" && isSourcePath(f.Path) {
			continue // already in sourceFiles
		}
		extraFiles = append(extraFiles, f)
		if f.Language == "python" {
			odooPythonFiles = append(odooPythonFiles, f)
		}
	}
	searchFiles := make([]searchFile, 0, len(sourceFiles)+len(extraFiles))
	searchFiles = append(searchFiles, sourceFiles...)
	searchFiles = append(searchFiles, extraFiles...)
	sort.Slice(searchFiles, func(i, j int) bool { return searchFiles[i].Path < searchFiles[j].Path })
	sourceByPath := make(map[string]searchFile, len(sourceFiles))
	for _, file := range sourceFiles {
		sourceByPath[file.Path] = file
	}
	printer.Success(fmt.Sprintf("Indexed %d searchable files", len(searchFiles)))

	printer.Step(4, 5, "Searching for method usage")
	prefixes := buildDynamicPrefixes(searchFiles)
	methodResults, err := analyzeMethodUsage(ctx, regularMethods, sourceByPath, searchFiles, prefixes, printer)
	if err != nil {
		return result, err
	}

	printer.Step(5, 5, "Checking orphaned super() calls")
	// Reuse the Odoo Python files already loaded in step 3 — no extra disk walk needed.
	odooMethodSet := buildMethodSet(odooPythonFiles)
	orphaned := findOrphanedSuperCalls(superCalls, odooMethodSet)
	printer.Success(fmt.Sprintf("Found %d orphaned super() calls", len(orphaned)))

	orphanedByMethod := make(map[string]struct{}, len(orphaned))
	for _, call := range orphaned {
		orphanedByMethod[methodKey(call.FilePath, call.MethodName)] = struct{}{}
	}

	used := make([]model.MethodResult, 0, len(methodResults))
	unused := make([]model.MethodResult, 0, len(methodResults))
	for index := range methodResults {
		_, hasOrphaned := orphanedByMethod[methodKey(methodResults[index].FilePath, methodResults[index].Name)]
		methodResults[index].HasOrphanedSuper = hasOrphaned
		if methodResults[index].IsUsed {
			used = append(used, methodResults[index])
		} else {
			unused = append(unused, methodResults[index])
		}
	}

	sortMethodResults(methodResults)
	sortMethodResults(used)
	sortMethodResults(unused)
	sortSuperCalls(orphaned)

	// Build RouteMethods from the partitioned route defs.
	routeMethodResults := make([]model.MethodResult, 0, len(routeMethodDefs))
	for _, m := range routeMethodDefs {
		routeMethodResults = append(routeMethodResults, model.MethodResult{
			Name:       m.Name,
			ClassName:  m.ClassName,
			FilePath:   m.FilePath,
			LineNumber: m.LineNumber,
			IsOverride: m.IsOverride,
			IsRoute:    true,
			Routes:     m.Routes,
		})
	}
	sortMethodResults(routeMethodResults)

	result.Methods = methodResults
	result.UsedMethods = used
	result.UnusedMethods = unused
	result.RouteMethods = routeMethodResults
	result.OrphanedSuperCalls = orphaned
	return result, nil
}

func collectFiles(ctx context.Context, roots []string, includeTests bool, extensions map[string]string, printer *ui.Printer, progressLabel string) ([]searchFile, error) {
	candidates, err := enumerateFiles(ctx, roots, includeTests, extensions)
	if err != nil {
		return nil, err
	}

	files := make([]searchFile, 0, len(candidates))
	for index, candidate := range candidates {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		content, err := os.ReadFile(candidate.Path)
		if err != nil {
			continue
		}
		normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
		files = append(files, searchFile{
			Path:     candidate.Path,
			Language: candidate.Language,
			Content:  normalized,
			Lines:    strings.Split(normalized, "\n"),
		})
		if printer != nil {
			printer.Progress(index+1, len(candidates), progressLabel)
		}
	}

	sort.Slice(files, func(i int, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

func enumerateFiles(ctx context.Context, roots []string, includeTests bool, extensions map[string]string) ([]searchFile, error) {
	files := make([]searchFile, 0)
	seen := make(map[string]struct{})
	for _, root := range roots {
		root = filepath.Clean(root)
		walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if entry.IsDir() {
				return nil
			}
			if !includeTests && strings.Contains(strings.ToLower(path), "test") {
				return nil
			}
			language, ok := extensions[strings.ToLower(filepath.Ext(path))]
			if !ok {
				return nil
			}
			cleanPath := filepath.Clean(path)
			if _, ok := seen[cleanPath]; ok {
				return nil
			}
			info, err := entry.Info()
			if err != nil || info.Size() > maxFileSize {
				return nil
			}
			seen[cleanPath] = struct{}{}
			files = append(files, searchFile{
				Path:     cleanPath,
				Language: language,
			})
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}
	sort.Slice(files, func(i int, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

func findSuperCalls(sourceFiles []searchFile) []model.SuperCall {
	calls := make([]model.SuperCall, 0)
	seen := make(map[string]struct{})
	for _, file := range sourceFiles {
		for index, line := range file.Lines {
			match := superCallRe.FindStringSubmatch(line)
			if len(match) != 2 {
				continue
			}
			methodName := match[1]
			if strings.HasPrefix(methodName, "__") {
				continue
			}
			key := fmt.Sprintf("%s|%d|%s", file.Path, index+1, methodName)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			calls = append(calls, model.SuperCall{
				MethodName: methodName,
				ClassName:  nearestClassName(file.Lines, index+1),
				FilePath:   file.Path,
				LineNumber: index + 1,
			})
		}
	}
	return calls
}

// leadingSpaces counts the number of leading space/tab characters in line.
func leadingSpaces(line string) int {
	n := 0
	for _, ch := range line {
		if ch == ' ' || ch == '\t' {
			n++
		} else {
			break
		}
	}
	return n
}

// isClassLevelDef returns true when the def at lines[defIdx] is a direct
// class method, not a nested/inner function. It walks backwards to find the
// first line whose indentation is strictly less than the def's indentation:
//   - if that line is a class declaration → class method (true)
//   - if it is another def (or anything else) → inner function (false)
//   - if no such line exists → top-level function (false)
func isClassLevelDef(lines []string, defIdx int) bool {
	defIndent := leadingSpaces(lines[defIdx])
	for i := defIdx - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if leadingSpaces(lines[i]) >= defIndent {
			continue // same or deeper indent: decorators, body of outer block
		}
		// First line with strictly less indentation.
		return classRe.MatchString(lines[i])
	}
	return false
}

func extractMethods(sourceFiles []searchFile, overrideSet map[string]struct{}) []model.MethodDef {
	methods := make([]model.MethodDef, 0)
	for _, file := range sourceFiles {
		for index, line := range file.Lines {
			match := defRe.FindStringSubmatch(line)
			if len(match) != 2 {
				continue
			}
			methodName := match[1]
			if shouldSkipMethod(methodName) || !isClassLevelDef(file.Lines, index) {
				continue
			}
			lineNumber := index + 1
			_, isOverride := overrideSet[methodKey(file.Path, methodName)]
			isRoute := hasRouteDecorator(file.Lines, lineNumber)
			def := model.MethodDef{
				Name:       methodName,
				ClassName:  nearestClassName(file.Lines, lineNumber),
				FilePath:   file.Path,
				LineNumber: lineNumber,
				IsOverride: isOverride,
				IsRoute:    isRoute,
			}
			if isRoute {
				def.Routes = extractRouteURLs(file.Lines, lineNumber)
			}
			methods = append(methods, def)
		}
	}
	return methods
}

func analyzeMethodUsage(ctx context.Context, methods []model.MethodDef, sourceByPath map[string]searchFile, searchFiles []searchFile, prefixes []dynamicPrefix, printer *ui.Printer) ([]model.MethodResult, error) {
	results := make([]model.MethodResult, 0, len(methods))
	for index, method := range methods {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}
		if printer.Verbose && index > 0 && index%50 == 0 {
			printer.Debug(fmt.Sprintf("Processed %d/%d methods", index, len(methods)))
		}

		result := model.MethodResult{
			Name:       method.Name,
			ClassName:  method.ClassName,
			FilePath:   method.FilePath,
			LineNumber: method.LineNumber,
			IsOverride: method.IsOverride,
		}

		if sourceFile, ok := sourceByPath[method.FilePath]; ok && hasAPIDecorator(sourceFile.Lines, method.LineNumber) {
			result.UsageHits = []model.UsageHit{{
				Language: "python",
				Reason:   "api_decorator",
				FilePath: method.FilePath,
				Line:     method.LineNumber,
			}}
		} else {
			// Build regexes once for this method name, then scan all files.
			patterns := buildMethodPatterns(method.Name)
			for _, file := range searchFiles {
				if !strings.Contains(file.Content, method.Name) {
					continue
				}
				if hit, ok := findUsage(method, file, patterns); ok {
					result.UsageHits = []model.UsageHit{hit}
					break
				}
			}
		}

		if len(result.UsageHits) == 0 {
			if prefix, ok := matchesDynamicPrefix(method.Name, prefixes); ok {
				result.UsageHits = []model.UsageHit{{
					Language: "python",
					Reason:   "dynamic_getattr_pattern",
					FilePath: prefix.FilePath,
					Line:     prefix.Line,
				}}
			}
		}

		result.UsageCount = len(result.UsageHits)
		result.IsUsed = result.UsageCount > 0
		results = append(results, result)
	}
	return results, nil
}

func findUsage(method model.MethodDef, file searchFile, patterns methodPatterns) (model.UsageHit, bool) {
	switch file.Language {
	case "python":
		return findPythonUsage(method, file, patterns.python)
	case "javascript":
		return findJavaScriptUsage(file, patterns.js)
	case "xml":
		return findXMLUsage(file, patterns.xml)
	default:
		return model.UsageHit{}, false
	}
}

func findPythonUsage(method model.MethodDef, file searchFile, checks []langPattern) (model.UsageHit, bool) {
	skipCallable := func(line string, lineNumber int) bool {
		if file.Path == method.FilePath && lineNumber == method.LineNumber {
			return true
		}
		return defRe.MatchString(line)
	}

	for _, check := range checks {
		var skip func(string, int) bool
		if check.skipDef {
			skip = skipCallable
		}
		if lineNumber, ok := firstMatchingLine(file.Lines, check.re, skip); ok {
			return model.UsageHit{Language: "python", Reason: check.reason, FilePath: file.Path, Line: lineNumber}, true
		}
	}
	return model.UsageHit{}, false
}

func findJavaScriptUsage(file searchFile, checks []langPattern) (model.UsageHit, bool) {
	for _, check := range checks {
		if lineNumber, ok := firstMatchingLine(file.Lines, check.re, nil); ok {
			return model.UsageHit{Language: "javascript", Reason: check.reason, FilePath: file.Path, Line: lineNumber}, true
		}
	}
	return model.UsageHit{}, false
}

func findXMLUsage(file searchFile, checks []langPattern) (model.UsageHit, bool) {
	for _, check := range checks {
		if lineNumber, ok := firstMatchingLine(file.Lines, check.re, nil); ok {
			return model.UsageHit{Language: "xml", Reason: check.reason, FilePath: file.Path, Line: lineNumber}, true
		}
	}
	return model.UsageHit{}, false
}

func buildMethodSet(files []searchFile) map[string]struct{} {
	methods := make(map[string]struct{})
	for _, file := range files {
		for _, line := range file.Lines {
			match := defRe.FindStringSubmatch(line)
			if len(match) == 2 {
				methods[match[1]] = struct{}{}
			}
		}
	}
	return methods
}

func findOrphanedSuperCalls(superCalls []model.SuperCall, odooMethodSet map[string]struct{}) []model.SuperCall {
	result := make([]model.SuperCall, 0)
	seen := make(map[string]struct{})
	for _, call := range superCalls {
		if _, ok := odooMethodSet[call.MethodName]; ok {
			continue
		}
		key := fmt.Sprintf("%s|%d|%s", call.FilePath, call.LineNumber, call.MethodName)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, call)
	}
	return result
}

func buildDynamicPrefixes(searchFiles []searchFile) []dynamicPrefix {
	prefixes := make([]dynamicPrefix, 0)
	seen := make(map[string]struct{})
	for _, file := range searchFiles {
		if file.Language != "python" {
			continue
		}
		for index, line := range file.Lines {
			for _, re := range []*regexp.Regexp{dynamicPercentRe, dynamicFormatRe, dynamicFStringRe} {
				match := re.FindStringSubmatch(line)
				if len(match) != 2 {
					continue
				}
				key := fmt.Sprintf("%s|%s|%d", match[1], file.Path, index+1)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				prefixes = append(prefixes, dynamicPrefix{Prefix: match[1], FilePath: file.Path, Line: index + 1})
			}
		}
	}
	return prefixes
}

func matchesDynamicPrefix(methodName string, prefixes []dynamicPrefix) (dynamicPrefix, bool) {
	for _, prefix := range prefixes {
		if strings.HasPrefix(methodName, prefix.Prefix) {
			return prefix, true
		}
	}
	return dynamicPrefix{}, false
}

func hasAPIDecorator(lines []string, lineNumber int) bool {
	start := lineNumber - 6
	if start < 0 {
		start = 0
	}
	end := lineNumber - 1
	for index := start; index < end && index < len(lines); index++ {
		if apiDecoratorRe.MatchString(lines[index]) {
			return true
		}
	}
	return false
}

func hasRouteDecorator(lines []string, lineNumber int) bool {
	start := lineNumber - 16
	if start < 0 {
		start = 0
	}
	end := lineNumber - 1
	for index := start; index < end && index < len(lines); index++ {
		if routeDecoratorRe.MatchString(lines[index]) {
			return true
		}
	}
	return false
}

// extractRouteURLs returns the URL path(s) declared in the @route / @http.route
// decorator that precedes the def at lineNumber (1-based). It handles single
// strings, lists of strings, and multi-line decorator spans.
func extractRouteURLs(lines []string, lineNumber int) []string {
	// Locate the decorator line (search up to 20 lines back).
	start := lineNumber - 20
	if start < 0 {
		start = 0
	}
	decoratorStart := -1
	for i := lineNumber - 2; i >= start; i-- {
		if routeDecoratorRe.MatchString(lines[i]) {
			decoratorStart = i
			break
		}
	}
	if decoratorStart < 0 {
		return nil
	}

	// Collect the full decorator text (walk forward until parens balance).
	var sb strings.Builder
	depth := 0
	for i := decoratorStart; i < len(lines) && i < lineNumber-1; i++ {
		sb.WriteString(lines[i])
		for _, ch := range lines[i] {
			switch ch {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		if depth == 0 && i > decoratorStart {
			break
		}
	}

	// Extract all quoted strings that look like URL paths (start with /).
	matches := routeStringRe.FindAllStringSubmatch(sb.String(), -1)
	routes := make([]string, 0, len(matches))
	for _, m := range matches {
		routes = append(routes, m[1])
	}
	return routes
}

func nearestClassName(lines []string, lineNumber int) string {
	for index := min(lineNumber-1, len(lines)-1); index >= 0; index-- {
		match := classRe.FindStringSubmatch(lines[index])
		if len(match) == 2 {
			return match[1]
		}
	}
	return "Unknown"
}

func shouldSkipMethod(methodName string) bool {
	if strings.HasPrefix(methodName, "__") {
		return true
	}
	if strings.HasPrefix(methodName, "_default") {
		return true
	}
	return methodName == "_table_query"
}

func firstMatchingLine(lines []string, re *regexp.Regexp, skip func(string, int) bool) (int, bool) {
	for index, line := range lines {
		lineNumber := index + 1
		if skip != nil && skip(line, lineNumber) {
			continue
		}
		if re.MatchString(line) {
			return lineNumber, true
		}
	}
	return 0, false
}

func methodKey(filePath string, methodName string) string {
	return filePath + "|" + methodName
}

func sortMethodResults(results []model.MethodResult) {
	sort.Slice(results, func(i int, j int) bool {
		if results[i].FilePath == results[j].FilePath {
			return results[i].LineNumber < results[j].LineNumber
		}
		return results[i].FilePath < results[j].FilePath
	})
}

func sortSuperCalls(calls []model.SuperCall) {
	sort.Slice(calls, func(i int, j int) bool {
		if calls[i].FilePath == calls[j].FilePath {
			return calls[i].LineNumber < calls[j].LineNumber
		}
		return calls[i].FilePath < calls[j].FilePath
	})
}

func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
