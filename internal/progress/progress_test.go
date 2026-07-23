package progress

import (
	"testing"
)

func TestProgressBar(t *testing.T) {
	pb := NewProgressBar(10, "Testing")
	pb.IncrementWithStatus("✓ file1.md")
	pb.IncrementWithStatus("SKIP file2.md")
	pb.UpdateWithStatus("processing file3.md")
	pb.Finish()
}
