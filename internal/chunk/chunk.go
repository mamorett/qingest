package chunk

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/tmc/langchaingo/textsplitter"
)

var headingRegex = regexp.MustCompile(`(?m)^#{1,6}\s+.*$`)

type Chunk struct {
	FilePath   string         `json:"file_path"`
	ChunkIndex int            `json:"chunk_index"`
	Content    string         `json:"content"`
	FileHash   string         `json:"file_hash"`
	Metadata   map[string]any `json:"metadata"`
}

// ExtractHeading returns the first markdown heading line found in text, or "" if none.
func ExtractHeading(text string) string {
	match := headingRegex.FindString(text)
	return strings.TrimSpace(match)
}

// ChunkMarkdownText splits markdown text into Chunk structures using langchaingo MarkdownTextSplitter.
func ChunkMarkdownText(filePath, text, fileHash string, chunkSize, chunkOverlap int) ([]Chunk, error) {
	splitter := textsplitter.NewMarkdownTextSplitter(
		textsplitter.WithChunkSize(chunkSize),
		textsplitter.WithChunkOverlap(chunkOverlap),
	)

	splitTexts, err := splitter.SplitText(text)
	if err != nil {
		return nil, fmt.Errorf("markdown split failed for %s: %w", filePath, err)
	}

	var chunks []Chunk
	chunkIndex := 0
	totalSplits := len(splitTexts)

	for _, chunkText := range splitTexts {
		trimmed := strings.TrimSpace(chunkText)
		if trimmed == "" {
			continue
		}

		chunks = append(chunks, Chunk{
			FilePath:   filePath,
			ChunkIndex: chunkIndex,
			Content:    chunkText,
			FileHash:   fileHash,
			Metadata: map[string]any{
				"source_file":     filePath,
				"total_chunks":    totalSplits,
				"chunk_index":     chunkIndex,
				"heading_context": ExtractHeading(chunkText),
				"char_count":      len(chunkText),
				"word_count":      len(strings.Fields(chunkText)),
			},
		})
		chunkIndex++
	}

	// Update total_chunks count in metadata to match actual non-empty chunks
	actualTotal := len(chunks)
	for i := range chunks {
		chunks[i].Metadata["total_chunks"] = actualTotal
	}

	return chunks, nil
}
