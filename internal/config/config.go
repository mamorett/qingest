package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/spf13/pflag"
)

type Config struct {
	Dir              string
	EmbedURL         string
	EmbedModel       string
	QdrantURL        string
	QdrantAPIKey     string
	Collection       string
	NoRecursive      bool
	ChunkSize        int
	ChunkOverlap     int
	CreateCollection bool
	DryRun           bool
	BatchSize        int
	DocBatchSize     int
	Force            bool
	Normalize        bool
	Hybrid           bool
	Preview          bool
	Verbose          bool
	MaxDocs          int
}

type QueryConfig struct {
	Query          string
	QdrantURL      string
	QdrantAPIKey   string
	Collection     string
	EmbedURL       string
	EmbedModel     string
	Hybrid         bool
	Limit          int
	ScoreThreshold float64
}

func getEnvOrDefault(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return fallback
}

// LoadIngestConfig loads .env and parses flags for qingest CLI
func LoadIngestConfig(args []string) (*Config, error) {
	_ = godotenv.Load()

	fs := pflag.NewFlagSet("qingest", pflag.ContinueOnError)

	cfg := &Config{}

	fs.StringVarP(&cfg.Dir, "dir", "d", "", "Root directory containing .md files.")
	fs.StringVar(&cfg.EmbedURL, "embed-url", getEnvOrDefault("QDRANT_EMBED_URL", "http://127.0.0.1:8008/v1"), "Base URL of the OpenAI-compatible embedding API.")
	fs.StringVar(&cfg.EmbedModel, "embed-model", getEnvOrDefault("QDRANT_EMBED_MODEL", "bge-m3"), "Embedding model name.")
	fs.StringVar(&cfg.QdrantURL, "qdrant-url", getEnvOrDefault("QDRANT_URL", "http://localhost:6333"), "Qdrant API URL.")
	fs.StringVar(&cfg.QdrantAPIKey, "qdrant-api-key", getEnvOrDefault("QDRANT_API_KEY", ""), "Qdrant API Key (optional).")
	fs.StringVar(&cfg.Collection, "collection", getEnvOrDefault("QDRANT_COLLECTION", "mdchunk"), "Qdrant collection to store chunks into.")
	fs.BoolVar(&cfg.NoRecursive, "no-recursive", false, "Only scan the top-level directory (no subdirectories).")
	fs.IntVar(&cfg.ChunkSize, "chunk-size", 800, "Target chunk size in characters.")
	fs.IntVar(&cfg.ChunkOverlap, "chunk-overlap", 200, "Overlap between chunks in characters.")
	fs.BoolVar(&cfg.CreateCollection, "create-collection", false, "Create the target collection if it doesn't exist.")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "Walk files, chunk, embed, but do NOT write to Qdrant.")
	fs.IntVar(&cfg.BatchSize, "batch-size", 128, "Number of texts to embed per API call.")
	fs.IntVar(&cfg.DocBatchSize, "doc-batch-size", 5, "Number of documents to process and ingest as a single batch (default: 5).")
	fs.BoolVarP(&cfg.Force, "force", "f", false, "Re-embed and re-insert files that are already in the DB (delete old records first).")
	fs.BoolVar(&cfg.Normalize, "normalize", false, "Normalize text (removes non-printing characters, collapses multi-newlines).")
	fs.BoolVar(&cfg.Hybrid, "hybrid", false, "Enable hybrid retrieval support (creates named vectors and content indexes).")
	fs.BoolVar(&cfg.Preview, "preview", false, "Preview normalization diffs for the first 5 markdown files without actual ingestion.")
	fs.BoolVarP(&cfg.Verbose, "verbose", "v", false, "Verbose (debug) logging.")
	fs.IntVarP(&cfg.MaxDocs, "max-docs", "n", 0, "Limit the maximum number of documents to process/ingest (0 for unlimited).")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			os.Exit(0)
		}
		return nil, err
	}

	if cfg.Dir == "" {
		return nil, errors.New("error: required flag \"dir\" not set")
	}

	absDir, err := filepath.Abs(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("invalid directory path '%s': %w", cfg.Dir, err)
	}
	cfg.Dir = absDir

	info, err := os.Stat(cfg.Dir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("directory '%s' does not exist", cfg.Dir)
	}

	return cfg, nil
}

// LoadQueryConfig loads .env and parses flags for qquery CLI
func LoadQueryConfig(args []string) (*QueryConfig, error) {
	_ = godotenv.Load()

	fs := pflag.NewFlagSet("qquery", pflag.ContinueOnError)

	cfg := &QueryConfig{}

	fs.StringVar(&cfg.QdrantURL, "qdrant-url", getEnvOrDefault("QDRANT_URL", "http://localhost:6333"), "Qdrant API URL.")
	fs.StringVar(&cfg.QdrantAPIKey, "qdrant-api-key", getEnvOrDefault("QDRANT_API_KEY", ""), "Qdrant API Key (optional).")
	fs.StringVar(&cfg.Collection, "collection", getEnvOrDefault("QDRANT_COLLECTION", "mdchunk"), "Qdrant collection to query.")
	fs.StringVar(&cfg.EmbedURL, "embed-url", getEnvOrDefault("QDRANT_EMBED_URL", "http://127.0.0.1:8008/v1"), "Base URL of the OpenAI-compatible embedding API.")
	fs.StringVar(&cfg.EmbedModel, "embed-model", getEnvOrDefault("QDRANT_EMBED_MODEL", "bge-m3"), "Embedding model name.")
	fs.BoolVar(&cfg.Hybrid, "hybrid", false, "Enable hybrid retrieval (requires the collection to have named vectors 'dense' and 'sparse').")
	fs.IntVar(&cfg.Limit, "limit", 5, "Number of results to return.")
	fs.Float64Var(&cfg.ScoreThreshold, "score-threshold", 0.3, "Minimum similarity score (0.0-1.0). Lower scores are discarded.")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			os.Exit(0)
		}
		return nil, err
	}

	positional := fs.Args()
	if len(positional) < 1 {
		return nil, errors.New("error: query positional argument required")
	}
	cfg.Query = positional[0]

	return cfg, nil
}
