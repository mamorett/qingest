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

	var finalSplitTexts []string
	for _, chunkText := range splitTexts {
		runes := []rune(chunkText)
		hasCJK := false
		for _, r := range runes {
			if (r >= 0x4E00 && r <= 0x9FFF) || 
				(r >= 0x3400 && r <= 0x4DBF) || 
				(r >= 0xF900 && r <= 0xFAFF) || 
				(r >= 0x3040 && r <= 0x309F) || 
				(r >= 0x30A0 && r <= 0x30FF) || 
				(r >= 0xAC00 && r <= 0xD7AF) || 
				(r >= 0xFF00 && r <= 0xFFEF) || 
				(r >= 0x3000 && r <= 0x303F) {
				hasCJK = true
				break
			}
		}
		safeLimit := 700
		if hasCJK {
			safeLimit = 400
		}
		if len(runes) > safeLimit {
			chunkText = string(runes[:safeLimit])
		}
		finalSplitTexts = append(finalSplitTexts, chunkText)
	}

	var chunks []Chunk
	chunkIndex := 0
	totalSplits := len(finalSplitTexts)

	for _, chunkText := range finalSplitTexts {
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

// splitLongText recursively splits a long text block into smaller chunks
// of at most maxLen characters (runes), trying to split at space/newline boundaries,
// and preserving overlap.
func splitLongText(text string, maxLen, overlap int) []string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}
	if maxLen <= overlap {
		overlap = maxLen / 2
	}
	if overlap < 0 {
		overlap = 0
	}

	var result []string
	start := 0
	for start < len(runes) {
		end := start + maxLen
		if end >= len(runes) {
			result = append(result, string(runes[start:]))
			break
		}

		// Try to find a space or newline in the last 20% of the chunk to split cleanly
		lookback := maxLen / 5
		if lookback < 10 {
			lookback = 10
		}
		if lookback > overlap {
			lookback = overlap
		}

		splitAt := end
		for j := end - 1; j >= end-lookback && j > start; j-- {
			if runes[j] == ' ' || runes[j] == '\n' || runes[j] == '\t' {
				splitAt = j
				break
			}
		}

		chunkRunes := runes[start:splitAt]
		result = append(result, string(chunkRunes))

		// Move start forward, accounting for overlap
		start = splitAt - overlap
		if start < 0 {
			start = 0
		}
		// Prevent infinite loops if we aren't making progress
		if start <= splitAt-maxLen {
			start = splitAt
		}
	}

	return result
}
