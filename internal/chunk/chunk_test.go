package chunk

import (
	"strings"
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
		if len(c.Content) < MinChunkLength {
			t.Errorf("chunk %d: stored content below MinChunkLength (%d chars): %q", i, len(c.Content), c.Content)
		}
	}
}

func TestChunkMarkdownTextRejectsHeadingOnly(t *testing.T) {
	// A document of consecutive headings must not produce "title but no
	// content" chunks. With no body text at all, the result is zero chunks.
	text := "# Title Only\n## Section A\n## Section B\n### Subsection"

	chunks, err := ChunkMarkdownText("headings.md", text, "hash123", 800, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, c := range chunks {
		if isHeadingOnly(c.Content) {
			t.Errorf("chunk %d is heading-only and must have been dropped: %q", i, c.Content)
		}
	}
}

func TestChunkMarkdownTextTrimsAndRejectsShort(t *testing.T) {
	// Short fragments surrounded by whitespace must be trimmed in storage
	// and dropped entirely if below MinChunkLength.
	text := "This is a long enough chunk of body text to survive validation.\n\n   \n\nTiny."

	chunks, err := ChunkMarkdownText("mixed.md", text, "hash123", 800, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, c := range chunks {
		if c.Content != strings.TrimSpace(c.Content) {
			t.Errorf("chunk %d was stored untrimmed: %q", i, c.Content)
		}
		if c.Content == "Tiny." {
			t.Errorf("chunk %d: sub-minimum fragment was not dropped", i)
		}
	}
}

func TestIsValidChunk(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", "", false},
		{"whitespace", "   \n\t  ", false},
		{"too short", "short", false},
		{"heading only", "# Just a Heading Here", false},
		{"multiple headings only", "# One\n## Two\n### Three", false},
		{"heading with body", "# Heading\n\nThis is real body content that is long enough.", true},
		{"plain body", "This is real body content that is long enough.", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidChunk(tc.input); got != tc.want {
				t.Errorf("isValidChunk(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
