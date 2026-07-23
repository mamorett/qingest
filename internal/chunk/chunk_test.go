package chunk

import (
	"testing"
)

func TestExtractHeading(t *testing.T) {
	text := "Some intro text\n\n# Main Title\n\nSome body text\n## Section 1\nMore text"
	heading := ExtractHeading(text)
	if heading != "# Main Title" {
		t.Errorf("expected '# Main Title', got %q", heading)
	}

	noHeading := ExtractHeading("Just some plain text without headings")
	if noHeading != "" {
		t.Errorf("expected empty string, got %q", noHeading)
	}
}

func TestChunkMarkdownText(t *testing.T) {
	text := "# Welcome to QIngest\n\nThis is a sample document for testing chunking.\n\n## Section 1\n\nHere is some content under section 1. It should be split cleanly by headings and paragraph boundaries.\n\n## Section 2\n\nHere is content under section 2."

	chunks, err := ChunkMarkdownText("test.md", text, "hash123", 100, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatalf("expected non-zero chunks")
	}

	for i, c := range chunks {
		if c.FilePath != "test.md" {
			t.Errorf("chunk %d: expected FilePath 'test.md', got %q", i, c.FilePath)
		}
		if c.FileHash != "hash123" {
			t.Errorf("chunk %d: expected FileHash 'hash123', got %q", i, c.FileHash)
		}
		if c.ChunkIndex != i {
			t.Errorf("chunk %d: expected ChunkIndex %d, got %d", i, i, c.ChunkIndex)
		}
		if c.Metadata["source_file"] != "test.md" {
			t.Errorf("chunk %d: metadata source_file error", i)
		}
	}
}
