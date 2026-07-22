package discover

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverMDFiles(t *testing.T) {
	tempDir := t.TempDir()

	// Create test file structure:
	// tempDir/a.md
	// tempDir/b.txt
	// tempDir/sub/c.md
	// tempDir/sub/sub2/d.md

	if err := os.WriteFile(filepath.Join(tempDir, "a.md"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}

	subDir := filepath.Join(tempDir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "c.md"), []byte("c"), 0644); err != nil {
		t.Fatal(err)
	}

	subDir2 := filepath.Join(subDir, "sub2")
	if err := os.MkdirAll(subDir2, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir2, "d.md"), []byte("d"), 0644); err != nil {
		t.Fatal(err)
	}

	// Recursive test
	recFiles, err := DiscoverMDFiles(tempDir, true, ".md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedRec := []string{
		filepath.Join(tempDir, "a.md"),
		filepath.Join(subDir, "c.md"),
		filepath.Join(subDir2, "d.md"),
	}

	if !reflect.DeepEqual(recFiles, expectedRec) {
		t.Errorf("recursive expected %v, got %v", expectedRec, recFiles)
	}

	// Non-recursive test
	nonRecFiles, err := DiscoverMDFiles(tempDir, false, ".md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedNonRec := []string{
		filepath.Join(tempDir, "a.md"),
	}

	if !reflect.DeepEqual(nonRecFiles, expectedNonRec) {
		t.Errorf("non-recursive expected %v, got %v", expectedNonRec, nonRecFiles)
	}
}
