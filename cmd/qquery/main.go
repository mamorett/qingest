package main

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/blacktop/go-termimg"
	"github.com/mamorett/qingest/internal/config"
	"github.com/mamorett/qingest/internal/embed"
	"github.com/mamorett/qingest/internal/qdrant"
)

//go:embed logo.png
var logoBytes []byte

func printLogo() {
	tmpFile, err := os.CreateTemp("", "qquery-logo-*.png")
	if err != nil {
		return
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(logoBytes); err != nil {
		tmpFile.Close()
		return
	}
	tmpFile.Close()

	img, err := termimg.Open(tmpFile.Name())
	if err == nil {
		_ = img.Width(60).Height(25).Print()
		fmt.Println()
	}
}

func main() {
	printLogo()

	cfg, err := config.LoadQueryConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// 1. Embed the query text
	fmt.Printf("Embedding query: '%s' using model '%s'...\n", cfg.Query, cfg.EmbedModel)
	vecs, err := embed.EmbedBatch([]string{cfg.Query}, cfg.EmbedURL, cfg.EmbedModel, 1, nil)
	if err != nil || len(vecs) == 0 {
		fmt.Fprintf(os.Stderr, "Failed to generate query embedding: %v\n", err)
		os.Exit(1)
	}
	queryVector := vecs[0]
	fmt.Printf("Successfully generated query embedding (dim=%d).\n", len(queryVector))

	// 2. Search Qdrant
	fmt.Printf("Searching Qdrant collection '%s' at %s (hybrid=%t)...\n", cfg.Collection, cfg.QdrantURL, cfg.Hybrid)
	qdrantClient := qdrant.NewClient(cfg.QdrantURL, cfg.QdrantAPIKey)

	results, err := qdrantClient.QueryPoints(cfg.Collection, queryVector, cfg.Query, cfg.Limit, cfg.Hybrid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to query Qdrant: %v\n", err)
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Println("No matching results found.")
		return
	}

	// 3. Filter by score threshold client-side
	var below []qdrant.SearchResult
	var above []qdrant.SearchResult

	for _, res := range results {
		if res.Score < cfg.ScoreThreshold {
			below = append(below, res)
		} else {
			above = append(above, res)
		}
	}

	if len(below) > 0 {
		fmt.Printf("\n%d result(s) discarded (score < %.4f):\n", len(below), cfg.ScoreThreshold)
		for _, p := range below {
			fp := "unknown"
			if p.Payload != nil {
				if pathVal, ok := p.Payload["file_path"].(string); ok && pathVal != "" {
					fp = pathVal
				}
			}
			fmt.Printf("  Score %.4f | %s\n", p.Score, fp)
		}
	}

	if len(above) == 0 {
		fmt.Printf("\nNo results above score threshold (%.4f).\n", cfg.ScoreThreshold)
		return
	}

	fmt.Printf("\n%d match(es) above threshold:\n%s\n", len(above), strings.Repeat("=", 80))
	for i, res := range above {
		fp := "unknown"
		content := ""
		if res.Payload != nil {
			if pathVal, ok := res.Payload["file_path"].(string); ok {
				fp = pathVal
			}
			if contentVal, ok := res.Payload["content"].(string); ok {
				content = strings.TrimSpace(contentVal)
			}
		}

		fmt.Printf("Result #%d | Score: %.4f | Source: %s\n", i+1, res.Score, fp)
		fmt.Println(strings.Repeat("-", 80))
		fmt.Println(content)
		fmt.Println(strings.Repeat("=", 80))
	}
}
