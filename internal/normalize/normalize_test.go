package normalize

import (
	"testing"
)

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "BOM and CRLF",
			input:    "\ufeffHello\r\nworld\r\n",
			expected: "Hello world\n",
		},
		{
			name:     "Control characters",
			input:    "Hello \x07world\x1f!",
			expected: "Hello world!",
		},
		{
			name:     "Join line wrapping non-terminated",
			input:    "This is a sentence\nthat wraps across lines.",
			expected: "This is a sentence that wraps across lines.",
		},
		{
			name:     "Join line wrapping starting lowercase",
			input:    "This sentence ends with a dot.\nand continues lowercase.",
			expected: "This sentence ends with a dot. and continues lowercase.",
		},
		{
			name:     "Do not join sentence terminated uppercase",
			input:    "First sentence.\nSecond sentence.",
			expected: "First sentence.\nSecond sentence.",
		},
		{
			name:     "Do not join markdown list",
			input:    "- Item 1\n- Item 2\n# Heading",
			expected: "- Item 1\n- Item 2\n# Heading",
		},
		{
			name:     "Collapse 3+ newlines",
			input:    "Paragraph 1.\n\n\n\n\nParagraph 2.",
			expected: "Paragraph 1.\n\nParagraph 2.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeText(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeText() =\n%q\nwant:\n%q", got, tt.expected)
			}
		})
	}
}
