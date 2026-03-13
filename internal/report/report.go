package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"odoo_method_analyzer/internal/model"
	"odoo_method_analyzer/internal/ui"
)

func Print(printer *ui.Printer, root string, result model.Result) {
	printer.Success("Analysis complete")
	fmt.Println()
	fmt.Printf("Used methods: %d\n", len(result.UsedMethods))
	fmt.Printf("Unused methods: %d\n", len(result.UnusedMethods))
	fmt.Printf("Orphaned super() calls: %d\n", len(result.OrphanedSuperCalls))

	if len(result.OrphanedSuperCalls) > 0 {
		fmt.Println()
		fmt.Println("Orphaned super() calls:")
		for _, call := range result.OrphanedSuperCalls {
			fmt.Printf("  - super().%s in %s (%s:%d)\n", call.MethodName, call.ClassName, relativePath(root, call.FilePath), call.LineNumber)
		}
	}

	if len(result.UnusedMethods) > 0 {
		fmt.Println()
		fmt.Println("Unused methods:")
		for _, method := range result.UnusedMethods {
			overrideMarker := ""
			if method.IsOverride {
				overrideMarker = " [OVERRIDE]"
			}
			fmt.Printf("  - %s in %s%s (%s:%d)\n", method.Name, method.ClassName, overrideMarker, relativePath(root, method.FilePath), method.LineNumber)
		}
	}

	if printer.Verbose && len(result.UsedMethods) > 0 {
		fmt.Println()
		fmt.Println("Used methods:")
		for _, method := range result.UsedMethods {
			overrideMarker := ""
			if method.IsOverride {
				overrideMarker = " [OVERRIDE]"
			}
			fmt.Printf("  - %s in %s%s (hits: %d)\n", method.Name, method.ClassName, overrideMarker, method.UsageCount)
		}
	}
}

func WriteJSON(outputFile string, result model.Result) error {
	file, err := os.Create(outputFile)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func relativePath(root string, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
