package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/pflag"
)

type EndpointConfig struct {
	QdrantURL    string `json:"qdrant_url"`
	QdrantAPIKey string `json:"qdrant_api_key"`
	Collection   string `json:"collection"`
	EmbedURL     string `json:"embed_url"`
	EmbedModel   string `json:"embed_model"`
	IsDefault    bool   `json:"default,omitempty"`
}

func (e *EndpointConfig) UnmarshalJSON(data []byte) error {
	type Alias EndpointConfig
	aux := &struct {
		QdrantURLAlt    string `json:"qdrantUrl"`
		QdrantAPIKeyAlt string `json:"qdrantApiKey"`
		EmbedURLAlt     string `json:"embedUrl"`
		EmbedModelAlt   string `json:"embedModel"`
		*Alias
	}{
		Alias: (*Alias)(e),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if e.QdrantURL == "" && aux.QdrantURLAlt != "" {
		e.QdrantURL = aux.QdrantURLAlt
	}
	if e.QdrantAPIKey == "" && aux.QdrantAPIKeyAlt != "" {
		e.QdrantAPIKey = aux.QdrantAPIKeyAlt
	}
	if e.EmbedURL == "" && aux.EmbedURLAlt != "" {
		e.EmbedURL = aux.EmbedURLAlt
	}
	if e.EmbedModel == "" && aux.EmbedModelAlt != "" {
		e.EmbedModel = aux.EmbedModelAlt
	}
	return nil
}

type FileConfig struct {
	DefaultEndpoint string                    `json:"default_endpoint,omitempty"`
	Default         string                    `json:"default,omitempty"`
	Endpoints       map[string]EndpointConfig `json:"endpoints"`
}

type Config struct {
	Endpoint         string
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
	ChunkBatchSize   int
	DocBatchSize     int
	Force            bool
	Normalize        bool
	Hybrid           bool
	Preview          bool
	Verbose          bool
	MaxDocs          int
}

type QueryConfig struct {
	Endpoint       string
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

// ExpandPath expands ~ at the beginning of a path to the user's home directory.
func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") || path == "~" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			if path == "~" {
				return homeDir
			}
			return filepath.Join(homeDir, path[2:])
		}
	}
	return path
}

// GetConfigFilePath returns the path to config.json (~/.config/qingest/config.json or QINGEST_CONFIG env).
func GetConfigFilePath() string {
	if envPath := os.Getenv("QINGEST_CONFIG"); envPath != "" {
		return ExpandPath(envPath)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = os.Getenv("HOME")
	}
	if homeDir == "" {
		return filepath.Join(".config", "qingest", "config.json")
	}
	return filepath.Join(homeDir, ".config", "qingest", "config.json")
}

// LoadFileConfig loads file config from configPath. If file doesn't exist and configPath is default path,
// it auto-creates a default config file with a 'local' endpoint.
func LoadFileConfig(configPath string) (*FileConfig, error) {
	configPath = ExpandPath(configPath)

	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			defaultPath := GetConfigFilePath()
			if configPath == defaultPath {
				// Create default config file if possible
				defaultCfg := &FileConfig{
					Default: "local",
					Endpoints: map[string]EndpointConfig{
						"local": {
							QdrantURL:    "http://localhost:6333",
							QdrantAPIKey: "",
							Collection:   "mdchunk",
							EmbedURL:     "http://127.0.0.1:8008/v1",
							EmbedModel:   "bge-m3",
						},
					},
				}
				if err := os.MkdirAll(filepath.Dir(configPath), 0755); err == nil {
					if bytes, err := json.MarshalIndent(defaultCfg, "", "  "); err == nil {
						_ = os.WriteFile(configPath, bytes, 0644)
						return defaultCfg, nil
					}
				}
			}
			return &FileConfig{Endpoints: make(map[string]EndpointConfig)}, nil
		}
		return nil, fmt.Errorf("failed to read config file '%s': %w", configPath, err)
	}

	var fileCfg FileConfig
	if err := json.Unmarshal(data, &fileCfg); err != nil {
		return nil, fmt.Errorf("failed to parse config JSON in '%s': %w", configPath, err)
	}
	if fileCfg.Endpoints == nil {
		fileCfg.Endpoints = make(map[string]EndpointConfig)
	}
	return &fileCfg, nil
}

func resolveEndpoint(fileCfg *FileConfig, requestedEndpoint, configPath string) (string, EndpointConfig, error) {
	names := make([]string, 0, len(fileCfg.Endpoints))
	for name := range fileCfg.Endpoints {
		names = append(names, name)
	}
	sort.Strings(names)

	availStr := "none"
	if len(names) > 0 {
		availStr = strings.Join(names, ", ")
	}

	// 1. Explicit endpoint specified
	if requestedEndpoint != "" {
		ep, ok := fileCfg.Endpoints[requestedEndpoint]
		if !ok {
			return "", EndpointConfig{}, fmt.Errorf("endpoint %q not found in %s (available endpoints: %s)", requestedEndpoint, configPath, availStr)
		}
		return requestedEndpoint, ep, nil
	}

	// 2. Default endpoint search
	var defaultName string
	if fileCfg.DefaultEndpoint != "" {
		defaultName = fileCfg.DefaultEndpoint
	} else if fileCfg.Default != "" {
		defaultName = fileCfg.Default
	}

	if defaultName != "" {
		if ep, ok := fileCfg.Endpoints[defaultName]; ok {
			return defaultName, ep, nil
		}
	}

	for name, ep := range fileCfg.Endpoints {
		if ep.IsDefault {
			return name, ep, nil
		}
	}

	if ep, ok := fileCfg.Endpoints["default"]; ok {
		return "default", ep, nil
	}

	if len(fileCfg.Endpoints) == 1 {
		return names[0], fileCfg.Endpoints[names[0]], nil
	}

	if len(fileCfg.Endpoints) == 0 {
		return "", EndpointConfig{}, fmt.Errorf("no endpoint specified and configuration file %s has no endpoints (available endpoints: %s)", configPath, availStr)
	}

	return "", EndpointConfig{}, fmt.Errorf("no endpoint specified and no default endpoint set in %s (available endpoints: %s)", configPath, availStr)
}

// LoadIngestConfig loads config from config file and flags for qingest CLI
func LoadIngestConfig(args []string) (*Config, error) {
	return LoadIngestConfigFromFile(args, GetConfigFilePath())
}

// LoadIngestConfigFromFile allows specifying custom config file path (useful for testing)
func LoadIngestConfigFromFile(args []string, configPath string) (*Config, error) {
	fs := pflag.NewFlagSet("qingest", pflag.ContinueOnError)

	var (
		endpointFlag     string
		embedURLFlag     string
		embedModelFlag   string
		qdrantURLFlag    string
		qdrantAPIKeyFlag string
		collectionFlag   string
	)

	cfg := &Config{}

	fs.StringVarP(&cfg.Dir, "dir", "d", "", "Root directory containing .md files.")
	fs.StringVarP(&endpointFlag, "endpoint", "e", "", "Name of the endpoint config to use from ~/.config/qingest/config.json.")
	fs.StringVar(&embedURLFlag, "embed-url", "", "Base URL of the OpenAI-compatible embedding API.")
	fs.StringVar(&embedModelFlag, "embed-model", "", "Embedding model name.")
	fs.StringVar(&qdrantURLFlag, "qdrant-url", "", "Qdrant API URL.")
	fs.StringVar(&qdrantAPIKeyFlag, "qdrant-api-key", "", "Qdrant API Key (optional).")
	fs.StringVar(&collectionFlag, "collection", "", "Qdrant collection to store chunks into.")
	fs.BoolVar(&cfg.NoRecursive, "no-recursive", false, "Only scan the top-level directory (no subdirectories).")
	fs.IntVar(&cfg.ChunkSize, "chunk-size", 800, "Target chunk size in characters.")
	fs.IntVar(&cfg.ChunkOverlap, "chunk-overlap", 200, "Overlap between chunks in characters.")
	fs.BoolVar(&cfg.CreateCollection, "create-collection", false, "Create the target collection if it doesn't exist.")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "Walk files, chunk, embed, but do NOT write to Qdrant.")
	fs.IntVar(&cfg.BatchSize, "batch-size", 128, "Number of texts to embed per API call.")
	fs.IntVar(&cfg.ChunkBatchSize, "chunk-batch-size", 100, "Number of chunks to store in Qdrant per upsert call.")
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

	fileCfg, err := LoadFileConfig(configPath)
	if err != nil {
		return nil, err
	}

	epName, ep, err := resolveEndpoint(fileCfg, endpointFlag, configPath)
	if err != nil {
		return nil, err
	}

	cfg.Endpoint = epName

	if fs.Changed("qdrant-url") {
		cfg.QdrantURL = qdrantURLFlag
	} else if ep.QdrantURL != "" {
		cfg.QdrantURL = ep.QdrantURL
	} else {
		cfg.QdrantURL = "http://localhost:6333"
	}

	if fs.Changed("qdrant-api-key") {
		cfg.QdrantAPIKey = qdrantAPIKeyFlag
	} else {
		cfg.QdrantAPIKey = ep.QdrantAPIKey
	}

	if fs.Changed("collection") {
		cfg.Collection = collectionFlag
	} else if ep.Collection != "" {
		cfg.Collection = ep.Collection
	} else {
		cfg.Collection = "mdchunk"
	}

	if fs.Changed("embed-url") {
		cfg.EmbedURL = embedURLFlag
	} else if ep.EmbedURL != "" {
		cfg.EmbedURL = ep.EmbedURL
	} else {
		cfg.EmbedURL = "http://127.0.0.1:8008/v1"
	}

	if fs.Changed("embed-model") {
		cfg.EmbedModel = embedModelFlag
	} else if ep.EmbedModel != "" {
		cfg.EmbedModel = ep.EmbedModel
	} else {
		cfg.EmbedModel = "bge-m3"
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

// LoadQueryConfig loads config from config file and flags for qquery CLI
func LoadQueryConfig(args []string) (*QueryConfig, error) {
	return LoadQueryConfigFromFile(args, GetConfigFilePath())
}

// LoadQueryConfigFromFile allows specifying custom config file path (useful for testing)
func LoadQueryConfigFromFile(args []string, configPath string) (*QueryConfig, error) {
	fs := pflag.NewFlagSet("qquery", pflag.ContinueOnError)

	var (
		endpointFlag     string
		embedURLFlag     string
		embedModelFlag   string
		qdrantURLFlag    string
		qdrantAPIKeyFlag string
		collectionFlag   string
	)

	cfg := &QueryConfig{}

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage:\n  qquery [flags] \"<query_text>\"\n\n")
		fmt.Fprintf(os.Stderr, "Semantic search query utility for Qdrant.\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  # Standard dense semantic query\n")
		fmt.Fprintf(os.Stderr, "  qquery \"your search concept\"\n\n")
		fmt.Fprintf(os.Stderr, "  # Query specific endpoint defined in ~/.config/qingest/config.json\n")
		fmt.Fprintf(os.Stderr, "  qquery \"your search concept\" --endpoint prod\n\n")
		fmt.Fprintf(os.Stderr, "  # Query with a higher similarity threshold\n")
		fmt.Fprintf(os.Stderr, "  qquery \"your search concept\" --score-threshold 0.55\n\n")
		fmt.Fprintf(os.Stderr, "  # Query using hybrid search\n")
		fmt.Fprintf(os.Stderr, "  qquery \"your search concept\" --hybrid\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
	}

	fs.StringVarP(&endpointFlag, "endpoint", "e", "", "Name of the endpoint config to use from ~/.config/qingest/config.json.")
	fs.StringVar(&qdrantURLFlag, "qdrant-url", "", "Qdrant API URL.")
	fs.StringVar(&qdrantAPIKeyFlag, "qdrant-api-key", "", "Qdrant API Key (optional).")
	fs.StringVar(&collectionFlag, "collection", "", "Qdrant collection to query.")
	fs.StringVar(&embedURLFlag, "embed-url", "", "Base URL of the OpenAI-compatible embedding API.")
	fs.StringVar(&embedModelFlag, "embed-model", "", "Embedding model name.")
	fs.BoolVar(&cfg.Hybrid, "hybrid", false, "Enable hybrid retrieval (requires the collection to have named vectors 'dense' and 'sparse').")
	fs.IntVar(&cfg.Limit, "limit", 5, "Number of results to return.")
	fs.Float64Var(&cfg.ScoreThreshold, "score-threshold", 0.3, "Minimum similarity score (0.0-1.0). Lower scores are discarded.")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			fs.Usage()
			os.Exit(0)
		}
		return nil, err
	}

	positional := fs.Args()
	if len(positional) < 1 {
		fs.Usage()
		return nil, errors.New("query positional argument required")
	}
	cfg.Query = positional[0]

	fileCfg, err := LoadFileConfig(configPath)
	if err != nil {
		return nil, err
	}

	epName, ep, err := resolveEndpoint(fileCfg, endpointFlag, configPath)
	if err != nil {
		return nil, err
	}

	cfg.Endpoint = epName

	if fs.Changed("qdrant-url") {
		cfg.QdrantURL = qdrantURLFlag
	} else if ep.QdrantURL != "" {
		cfg.QdrantURL = ep.QdrantURL
	} else {
		cfg.QdrantURL = "http://localhost:6333"
	}

	if fs.Changed("qdrant-api-key") {
		cfg.QdrantAPIKey = qdrantAPIKeyFlag
	} else {
		cfg.QdrantAPIKey = ep.QdrantAPIKey
	}

	if fs.Changed("collection") {
		cfg.Collection = collectionFlag
	} else if ep.Collection != "" {
		cfg.Collection = ep.Collection
	} else {
		cfg.Collection = "mdchunk"
	}

	if fs.Changed("embed-url") {
		cfg.EmbedURL = embedURLFlag
	} else if ep.EmbedURL != "" {
		cfg.EmbedURL = ep.EmbedURL
	} else {
		cfg.EmbedURL = "http://127.0.0.1:8008/v1"
	}

	if fs.Changed("embed-model") {
		cfg.EmbedModel = embedModelFlag
	} else if ep.EmbedModel != "" {
		cfg.EmbedModel = ep.EmbedModel
	} else {
		cfg.EmbedModel = "bge-m3"
	}

	if cfg.Hybrid && !fs.Changed("score-threshold") {
		cfg.ScoreThreshold = 0.0
	}

	return cfg, nil
}
