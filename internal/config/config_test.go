package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}
	return path
}

func TestLoadIngestConfig_SingleEndpointDefault(t *testing.T) {
	tempDir := t.TempDir()
	jsonContent := `{
		"endpoints": {
			"local": {
				"qdrant_url": "http://localhost:6333",
				"collection": "test_collection",
				"embed_url": "http://127.0.0.1:8008/v1",
				"embed_model": "bge-m3"
			}
		}
	}`
	cfgFile := createTestConfig(t, jsonContent)

	// Test default chunk-batch-size
	argsDefault := []string{"--dir", tempDir}
	cfgDefault, err := LoadIngestConfigFromFile(argsDefault, cfgFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfgDefault.ChunkBatchSize != 100 {
		t.Errorf("expected default chunk batch size 100, got %d", cfgDefault.ChunkBatchSize)
	}

	// Test explicit chunk-batch-size override
	args := []string{"--dir", tempDir, "--chunk-size", "1000", "--chunk-batch-size", "42", "-v"}
	cfg, err := LoadIngestConfigFromFile(args, cfgFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Endpoint != "local" {
		t.Errorf("expected endpoint 'local', got '%s'", cfg.Endpoint)
	}
	if cfg.Collection != "test_collection" {
		t.Errorf("expected collection 'test_collection', got '%s'", cfg.Collection)
	}
	if cfg.Dir != tempDir {
		t.Errorf("expected dir %s, got %s", tempDir, cfg.Dir)
	}
	if cfg.ChunkSize != 1000 {
		t.Errorf("expected chunk size 1000, got %d", cfg.ChunkSize)
	}
	if cfg.ChunkBatchSize != 42 {
		t.Errorf("expected chunk batch size 42, got %d", cfg.ChunkBatchSize)
	}
	if !cfg.Verbose {
		t.Errorf("expected verbose true, got false")
	}
}

func TestLoadIngestConfig_MultiEndpointExplicit(t *testing.T) {
	tempDir := t.TempDir()
	jsonContent := `{
		"default": "local",
		"endpoints": {
			"local": {
				"qdrant_url": "http://localhost:6333",
				"collection": "local_coll"
			},
			"prod": {
				"qdrant_url": "https://qdrant.prod:6333",
				"collection": "prod_coll",
				"qdrant_api_key": "secret123"
			}
		}
	}`
	cfgFile := createTestConfig(t, jsonContent)

	// Test default endpoint selection ("local")
	cfgDefault, err := LoadIngestConfigFromFile([]string{"--dir", tempDir}, cfgFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfgDefault.Endpoint != "local" || cfgDefault.Collection != "local_coll" {
		t.Errorf("expected default endpoint 'local' (coll: local_coll), got endpoint '%s' (coll: %s)", cfgDefault.Endpoint, cfgDefault.Collection)
	}

	// Test explicit endpoint selection via -e prod
	cfgProd, err := LoadIngestConfigFromFile([]string{"--dir", tempDir, "-e", "prod"}, cfgFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfgProd.Endpoint != "prod" {
		t.Errorf("expected endpoint 'prod', got '%s'", cfgProd.Endpoint)
	}
	if cfgProd.QdrantURL != "https://qdrant.prod:6333" {
		t.Errorf("expected QdrantURL 'https://qdrant.prod:6333', got '%s'", cfgProd.QdrantURL)
	}
	if cfgProd.QdrantAPIKey != "secret123" {
		t.Errorf("expected API key 'secret123', got '%s'", cfgProd.QdrantAPIKey)
	}
}

func TestLoadIngestConfig_EndpointNotFound(t *testing.T) {
	tempDir := t.TempDir()
	jsonContent := `{
		"endpoints": {
			"dev": {"collection": "dev_coll"},
			"prod": {"collection": "prod_coll"}
		}
	}`
	cfgFile := createTestConfig(t, jsonContent)

	_, err := LoadIngestConfigFromFile([]string{"--dir", tempDir, "--endpoint", "staging"}, cfgFile)
	if err == nil {
		t.Fatalf("expected error when requesting non-existent endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "staging") || !strings.Contains(err.Error(), "dev, prod") {
		t.Errorf("expected error message listing available endpoints 'dev, prod', got: %v", err)
	}
}

func TestLoadIngestConfig_NoDefaultSpecified(t *testing.T) {
	tempDir := t.TempDir()
	jsonContent := `{
		"endpoints": {
			"dev": {"collection": "dev_coll"},
			"prod": {"collection": "prod_coll"}
		}
	}`
	cfgFile := createTestConfig(t, jsonContent)

	_, err := LoadIngestConfigFromFile([]string{"--dir", tempDir}, cfgFile)
	if err == nil {
		t.Fatalf("expected error when no endpoint specified and no default set, got nil")
	}
	if !strings.Contains(err.Error(), "no default endpoint set") || !strings.Contains(err.Error(), "dev, prod") {
		t.Errorf("expected error reporting no default set and available endpoints 'dev, prod', got: %v", err)
	}
}

func TestLoadIngestConfig_CLIOverride(t *testing.T) {
	tempDir := t.TempDir()
	jsonContent := `{
		"endpoints": {
			"local": {
				"qdrant_url": "http://localhost:6333",
				"collection": "config_coll"
			}
		}
	}`
	cfgFile := createTestConfig(t, jsonContent)

	args := []string{"--dir", tempDir, "--collection", "override_coll", "--qdrant-url", "http://custom:6333"}
	cfg, err := LoadIngestConfigFromFile(args, cfgFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Collection != "override_coll" {
		t.Errorf("expected collection 'override_coll', got '%s'", cfg.Collection)
	}
	if cfg.QdrantURL != "http://custom:6333" {
		t.Errorf("expected qdrant-url 'http://custom:6333', got '%s'", cfg.QdrantURL)
	}
}

func TestLoadQueryConfig(t *testing.T) {
	jsonContent := `{
		"default": "search_ep",
		"endpoints": {
			"search_ep": {
				"qdrant_url": "http://qdrant.internal:6333",
				"collection": "knowledge_base"
			}
		}
	}`
	cfgFile := createTestConfig(t, jsonContent)

	args := []string{"how do I configure the server?", "--limit", "10"}
	cfg, err := LoadQueryConfigFromFile(args, cfgFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Endpoint != "search_ep" {
		t.Errorf("expected endpoint 'search_ep', got '%s'", cfg.Endpoint)
	}
	if cfg.QdrantURL != "http://qdrant.internal:6333" {
		t.Errorf("expected qdrant-url 'http://qdrant.internal:6333', got '%s'", cfg.QdrantURL)
	}
	if cfg.Collection != "knowledge_base" {
		t.Errorf("expected collection 'knowledge_base', got '%s'", cfg.Collection)
	}
	if cfg.Query != "how do I configure the server?" {
		t.Errorf("expected query 'how do I configure the server?', got '%s'", cfg.Query)
	}
	if cfg.Limit != 10 {
		t.Errorf("expected limit 10, got %d", cfg.Limit)
	}
}

func TestMissingDir(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := createTestConfig(t, `{"endpoints":{"local":{"collection":"c"}}}`)
	_, err := LoadIngestConfigFromFile([]string{}, cfgFile)
	if err == nil {
		t.Errorf("expected error when --dir is missing, got nil")
	}
	_ = tempDir
}

func TestInvalidDir(t *testing.T) {
	cfgFile := createTestConfig(t, `{"endpoints":{"local":{"collection":"c"}}}`)
	nonExistent := filepath.Join(os.TempDir(), "non-existent-dir-12345")
	_, err := LoadIngestConfigFromFile([]string{"--dir", nonExistent}, cfgFile)
	if err == nil {
		t.Errorf("expected error when --dir does not exist, got nil")
	}
}
