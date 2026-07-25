package main

import (
	"testing"
)

func TestExtractContent(t *testing.T) {
	payload := map[string]any{
		"content":   "This is the chunk content text.",
		"file_path": "docs/test.md",
	}
	content := extractContent(payload)
	if content != "This is the chunk content text." {
		t.Errorf("expected 'This is the chunk content text.', got %q", content)
	}
}

func TestExtractHeadingMetadata(t *testing.T) {
	payload := map[string]any{
		"metadata": map[string]any{
			"doc_title":       "# Guide",
			"heading_context": "## Section 1",
		},
	}
	docTitle, headingContext := extractHeadingMetadata(payload)
	if docTitle != "# Guide" {
		t.Errorf("expected '# Guide', got %q", docTitle)
	}
	if headingContext != "## Section 1" {
		t.Errorf("expected '## Section 1', got %q", headingContext)
	}
}

func TestExtractChunkPos(t *testing.T) {
	payload := map[string]any{
		"metadata": map[string]any{
			"chunk_index":  float64(2),
			"total_chunks": float64(10),
		},
	}
	chunkIdx, totalChunks, ok := extractChunkPos(payload)
	if !ok {
		t.Fatalf("expected ok true")
	}
	if chunkIdx != 2 || totalChunks != 10 {
		t.Errorf("expected chunk 2/10, got %d/%d", chunkIdx, totalChunks)
	}
}
