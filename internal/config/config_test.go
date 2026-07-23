package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadIngestConfig(t *testing.T) {
	tempDir := t.TempDir()

	args := []string{"--dir", tempDir, "--chunk-size", "1000", "-v"}
	cfg, err := LoadIngestConfig(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Dir != tempDir {
		t.Errorf("expected dir %s, got %s", tempDir, cfg.Dir)
	}
	if cfg.ChunkSize != 1000 {
		t.Errorf("expected chunk size 1000, got %d", cfg.ChunkSize)
	}
	if !cfg.Verbose {
		t.Errorf("expected verbose true, got false")
	}
	if cfg.MaxDocs != 0 {
		t.Errorf("expected default max docs 0, got %d", cfg.MaxDocs)
	}

	// Default check
	if cfg.EmbedModel != "bge-m3" {
		t.Errorf("expected embed model bge-m3, got %s", cfg.EmbedModel)
	}

	// Max docs check
	argsWithMaxDocs := []string{"--dir", tempDir, "-n", "42"}
	cfgWithMaxDocs, err := LoadIngestConfig(argsWithMaxDocs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfgWithMaxDocs.MaxDocs != 42 {
		t.Errorf("expected max docs 42, got %d", cfgWithMaxDocs.MaxDocs)
	}
}

func TestLoadQueryConfig(t *testing.T) {
	args := []string{"how do I configure the server?", "--limit", "10"}
	cfg, err := LoadQueryConfig(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Query != "how do I configure the server?" {
		t.Errorf("expected query 'how do I configure the server?', got '%s'", cfg.Query)
	}
	if cfg.Limit != 10 {
		t.Errorf("expected limit 10, got %d", cfg.Limit)
	}
}

func TestMissingDir(t *testing.T) {
	_, err := LoadIngestConfig([]string{})
	if err == nil {
		t.Errorf("expected error when --dir is missing, got nil")
	}
}

func TestInvalidDir(t *testing.T) {
	nonExistent := filepath.Join(os.TempDir(), "non-existent-dir-12345")
	_, err := LoadIngestConfig([]string{"--dir", nonExistent})
	if err == nil {
		t.Errorf("expected error when --dir does not exist, got nil")
	}
}
