package preview

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunPreview(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "doc.md")
	if err := os.WriteFile(f1, []byte("# Header\n\n\n\nSome text.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Run preview, verify it executes cleanly without panic
	RunPreview([]string{f1}, tempDir, 5)
}
