package chunk

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/tmc/langchaingo/textsplitter"
)

var headingRegex = regexp.MustCompile(`(?m)^#{1,6}\s+.*$`)

// headingLineRegex matches a single line consisting of a markdown heading.
var headingLineRegex = regexp.MustCompile(`^#{1,6}[ \t]+`)

// MinChunkLength is the minimum number of non-whitespace characters a chunk
// must contain to be stored. Anything shorter carries no retrievable meaning.
const MinChunkLength = 20

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

// isValidChunk reports whether a chunk carries actual body text worth
// embedding and retrieving. It rejects empty chunks, chunks shorter than
// MinChunkLength, and chunks consisting solely of markdown heading lines
// (which produce the "title but empty content" symptom at query time).
func isValidChunk(trimmed string) bool {
	if len(trimmed) < MinChunkLength {
		return false
	}
	return !isHeadingOnly(trimmed)
}

// isHeadingOnly reports whether every non-blank line of the text is a
// markdown heading — i.e. the chunk has a title but no body.
func isHeadingOnly(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !headingLineRegex.MatchString(line) {
			return false
		}
	}
	return true
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
		if !isValidChunk(trimmed) {
			continue
		}

		chunks = append(chunks, Chunk{
			FilePath:   filePath,
			ChunkIndex: chunkIndex,
			Content:    trimmed,
			FileHash:   fileHash,
			Metadata: map[string]any{
				"source_file":     filePath,
				"total_chunks":    totalSplits,
				"chunk_index":     chunkIndex,
				"heading_context": ExtractHeading(trimmed),
				"char_count":      len(trimmed),
				"word_count":      len(strings.Fields(trimmed)),
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
