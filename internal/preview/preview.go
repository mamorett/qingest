package preview

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mamorett/qingest/internal/normalize"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// RunPreview previews text normalization diffs for up to limit files.
func RunPreview(files []string, directory string, limit int) {
	if limit <= 0 {
		limit = 10
	}

	if len(files) < limit {
		limit = len(files)
	}

	slog.Info(fmt.Sprintf("=== Normalization Preview (First %d Files) ===", limit))

	dmp := diffmatchpatch.New()

	for _, f := range files[:limit] {
		rel, err := filepath.Rel(directory, f)
		if err != nil {
			rel = f
		}

		rawBytes, err := os.ReadFile(f)
		if err != nil {
			slog.Error(fmt.Sprintf("Failed to read '%s': %v", rel, err))
			continue
		}

		orig := string(rawBytes)
		norm := normalize.NormalizeText(orig)

		origLines := strings.Split(orig, "\n")
		normLines := strings.Split(norm, "\n")

		origEmpty := 0
		for _, line := range origLines {
			if strings.TrimSpace(line) == "" {
				origEmpty++
			}
		}

		normEmpty := 0
		for _, line := range normLines {
			if strings.TrimSpace(line) == "" {
				normEmpty++
			}
		}

		fmt.Printf("\n%s\n", strings.Repeat("=", 80))
		fmt.Printf("File: %s\n", rel)
		fmt.Printf("Stats:\n")
		fmt.Printf("  Characters: %d -> %d (delta: %d)\n", len(orig), len(norm), len(norm)-len(orig))
		fmt.Printf("  Total Lines: %d -> %d (delta: %d)\n", len(origLines), len(normLines), len(normLines)-len(origLines))
		fmt.Printf("  Empty Lines: %d -> %d (delta: %d)\n", origEmpty, normEmpty, normEmpty-origEmpty)
		fmt.Printf("%s\n", strings.Repeat("=", 80))

		if orig != norm {
			patches := dmp.PatchMake(orig, norm)
			patchText := dmp.PatchToText(patches)

			if patchText != "" {
				fmt.Printf("Changes made by normalization:\n")
				fmt.Print(patchText)
				fmt.Printf("%s\n", strings.Repeat("-", 80))
			}
		}

		fmt.Printf("Normalized Content Preview (First 50 Lines):\n")
		previewLines := normLines
		if len(previewLines) > 50 {
			previewLines = previewLines[:50]
		}
		fmt.Println(strings.Join(previewLines, "\n"))
		fmt.Printf("%s\n", strings.Repeat("=", 80))
	}

	slog.Info("Preview finished. Exiting without database ingestion.")
}
