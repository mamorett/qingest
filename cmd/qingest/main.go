package main

import (
	"crypto/sha256"

	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"strings"

	"github.com/blacktop/go-termimg"
	"github.com/mamorett/qingest/internal/chunk"
	"github.com/mamorett/qingest/internal/config"
	"github.com/mamorett/qingest/internal/discover"
	"github.com/mamorett/qingest/internal/embed"
	"github.com/mamorett/qingest/internal/normalize"
	"github.com/mamorett/qingest/internal/preview"
	"github.com/mamorett/qingest/internal/progress"
	"github.com/mamorett/qingest/internal/qdrant"
	"golang.org/x/sync/errgroup"
)

//go:embed logo.png
var logoBytes []byte

func printLogo() {
	tmpFile, err := os.CreateTemp("", "qingest-logo-*.png")
	if err != nil {
		return
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(logoBytes); err != nil {
		tmpFile.Close()
		return
	}
	tmpFile.Close()

	img, err := termimg.Open(tmpFile.Name())
	if err == nil {
		_ = img.Width(60).Height(25).Print()
		fmt.Println()
	}
}

type FileCheckResult struct {
	AbsPath       string
	RelPath       string
	Hash          string
	ShouldProcess bool
	Reason        string
}

func checkFileNeedsProcessing(absPath, directory string, norm, force bool, indexedHashes map[string]string) (*FileCheckResult, error) {
	relPath, err := filepath.Rel(directory, absPath)
	if err != nil {
		relPath = absPath
	}

	storedHash, existsInDB := indexedHashes[relPath]
	if !existsInDB {
		return &FileCheckResult{
			AbsPath:       absPath,
			RelPath:       relPath,
			ShouldProcess: true,
			Reason:        "New file",
		}, nil
	}

	if force {
		return &FileCheckResult{
			AbsPath:       absPath,
			RelPath:       relPath,
			ShouldProcess: true,
			Reason:        "Force replace (--force)",
		}, nil
	}

	if storedHash == "__legacy__" {
		return &FileCheckResult{
			AbsPath:       absPath,
			RelPath:       relPath,
			ShouldProcess: false,
			Reason:        "Legacy record (skipped)",
		}, nil
	}

	rawBytes, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file '%s': %w", absPath, err)
	}

	content := string(rawBytes)
	if norm {
		content = normalize.NormalizeText(content)
	}

	h := sha256.Sum256([]byte(content))
	currentHash := fmt.Sprintf("%x", h)

	if storedHash != currentHash {
		return &FileCheckResult{
			AbsPath:       absPath,
			RelPath:       relPath,
			Hash:          currentHash,
			ShouldProcess: true,
			Reason:        fmt.Sprintf("Modified (hash mismatch: stored=%s, current=%s)", storedHash, currentHash),
		}, nil
	}

	return &FileCheckResult{
		AbsPath:       absPath,
		RelPath:       relPath,
		Hash:          currentHash,
		ShouldProcess: false,
		Reason:        "Identical (hash match)",
	}, nil
}

func main() {
	printLogo()

	cfg, err := config.LoadIngestConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if cfg.Verbose {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	// 1. Discover files
	files, err := discover.DiscoverMDFiles(cfg.Dir, !cfg.NoRecursive, ".md")
	if err != nil {
		slog.Error(fmt.Sprintf("Failed to discover markdown files: %v", err))
		os.Exit(1)
	}

	slog.Info(fmt.Sprintf("Found %d markdown file(s) in '%s'.", len(files), cfg.Dir))

	if len(files) == 0 {
		slog.Warn("No .md files found. Exiting.")
		os.Exit(0)
	}

	// 2. Normalization Preview Mode
	if cfg.Preview {
		preview.RunPreview(files, cfg.Dir, 10)
		os.Exit(0)
	}

	slog.Info(fmt.Sprintf("Using Qdrant: %s (Collection: %s)", cfg.QdrantURL, cfg.Collection))

	qdrantClient := qdrant.NewClient(cfg.QdrantURL, cfg.QdrantAPIKey)
	collectionEnsured := false

	// 3. Check which files need processing
	totalFiles := len(files)
	allIndexedHashes := make(map[string]string)

	if !cfg.DryRun {
		relPaths := make([]string, len(files))
		for i, f := range files {
			rel, _ := filepath.Rel(cfg.Dir, f)
			relPaths[i] = rel
		}

		hashes, err := qdrantClient.GetAllIndexedHashes(cfg.Collection, relPaths, 500)
		if err != nil {
			slog.Warn(fmt.Sprintf("Could not query existing hashes from Qdrant: %v", err))
		} else {
			allIndexedHashes = hashes
		}
	}

	slog.Info("Checking which files need to be processed (using up to 8 threads)...")

	var (
		mu             sync.Mutex
		filesToProcess []FileCheckResult
		totalSkipped   int
	)

	var g errgroup.Group
	g.SetLimit(8)

	for _, f := range files {
		filePath := f
		g.Go(func() error {
			res, err := checkFileNeedsProcessing(filePath, cfg.Dir, cfg.Normalize, cfg.Force, allIndexedHashes)
			if err != nil {
				slog.Error(fmt.Sprintf("Error checking file '%s': %v", filePath, err))
				return nil
			}

			mu.Lock()
			defer mu.Unlock()

			if res.ShouldProcess {
				slog.Debug(fmt.Sprintf("🚀 Stage for ingestion: %s (%s)", res.RelPath, res.Reason))
				filesToProcess = append(filesToProcess, *res)
			} else {
				slog.Debug(fmt.Sprintf("Skip: %s (%s)", res.RelPath, res.Reason))
				totalSkipped++
			}
			return nil
		})
	}

	_ = g.Wait()

	if cfg.MaxDocs > 0 && len(filesToProcess) > cfg.MaxDocs {
		slog.Info(fmt.Sprintf("Limiting ingestion to the first %d document(s) (out of %d staged for ingestion) due to --max-docs limit.", cfg.MaxDocs, len(filesToProcess)))
		filesToProcess = filesToProcess[:cfg.MaxDocs]
	}

	totalToProcess := len(filesToProcess)
	totalInserted := 0

	if totalToProcess == 0 {
		slog.Info("All files are up-to-date. Nothing to ingest.")
		os.Exit(0)
	}

	slog.Info(fmt.Sprintf("Processing %d files (out of %d total discovered, skipped %d) in batches of %d documents.",
		totalToProcess, totalFiles, totalSkipped, cfg.DocBatchSize))

	pb := progress.NewProgressBar(totalToProcess, "Ingesting")

	// 4. Process in document batches
	docBatchSize := cfg.DocBatchSize
	for i := 0; i < totalToProcess; i += docBatchSize {
		end := i + docBatchSize
		if end > totalToProcess {
			end = totalToProcess
		}
		batchItems := filesToProcess[i:end]
		batchNum := (i / docBatchSize) + 1
		totalBatches := (totalToProcess + docBatchSize - 1) / docBatchSize

		slog.Debug(fmt.Sprintf("--- Ingestion Batch %d/%d (Files %d-%d of %d) ---",
			batchNum, totalBatches, i+1, end, totalToProcess))

		batchContents := make(map[string]string)
		batchHashes := make(map[string]string)
		var readOK []FileCheckResult

		for _, item := range batchItems {
			slog.Debug(fmt.Sprintf("Processing file: %s", item.RelPath))
			rawBytes, err := os.ReadFile(item.AbsPath)
			if err != nil {
				slog.Error(fmt.Sprintf("Failed to read file '%s': %v", item.AbsPath, err))
				pb.IncrementWithStatus(fmt.Sprintf("✗ %s (0 chunks)", item.RelPath))
				continue
			}

			content := string(rawBytes)
			if cfg.Normalize {
				content = normalize.NormalizeText(content)
			}

			batchContents[item.RelPath] = content

			fHash := item.Hash
			if fHash == "" {
				h := sha256.Sum256([]byte(content))
				fHash = fmt.Sprintf("%x", h)
			}
			batchHashes[item.RelPath] = fHash
			readOK = append(readOK, item)
		}

		if len(batchContents) == 0 {
			continue
		}

		// Chunk
		var batchChunks []chunk.Chunk
		fileChunksCount := make(map[string]int)
		for _, item := range readOK {
			content, ok := batchContents[item.RelPath]
			if !ok {
				continue
			}
			fHash := batchHashes[item.RelPath]
			chunks, err := chunk.ChunkMarkdownText(item.RelPath, content, fHash, cfg.ChunkSize, cfg.ChunkOverlap)
			if err != nil {
				slog.Error(fmt.Sprintf("Chunking failed for '%s': %v", item.RelPath, err))
				continue
			}
			fileChunksCount[item.RelPath] = len(chunks)
			batchChunks = append(batchChunks, chunks...)
			slog.Debug(fmt.Sprintf("File '%s' → %d chunk(s)", item.RelPath, len(chunks)))
		}

		if len(batchChunks) == 0 {
			for _, item := range batchItems {
				pb.IncrementWithStatus(fmt.Sprintf("SKIP %s (0 chunks)", item.RelPath))
			}
			continue
		}

		// Embed
		pb.Describe(formatBatchStatus(batchItems, fmt.Sprintf("Embedding %d chunks", len(batchChunks))))
		slog.Debug(fmt.Sprintf("Embedding %d chunks...", len(batchChunks)))
		texts := make([]string, len(batchChunks))
		for idx, c := range batchChunks {
			texts[idx] = c.Content
		}

		embeddings, err := embed.EmbedBatch(texts, cfg.EmbedURL, cfg.EmbedModel, cfg.BatchSize, func(current, total int) {
			pb.Describe(formatBatchStatus(batchItems, fmt.Sprintf("Embedding chunks %d/%d", current, total)))
		})
		if err != nil {
			slog.Error(fmt.Sprintf("Embedding failed for batch %d: %v", batchNum, err))
			for _, item := range batchItems {
				pb.IncrementWithStatus(fmt.Sprintf("✗ %s (%d chunks)", item.RelPath, fileChunksCount[item.RelPath]))
			}
			continue
		}

		// Ensure collection
		if cfg.CreateCollection && !collectionEnsured {
			vectorDim := len(embeddings[0])
			if err := qdrantClient.CreateCollectionIfNotExists(cfg.Collection, vectorDim, cfg.Hybrid); err != nil {
				slog.Error(fmt.Sprintf("Failed to create Qdrant collection: %v", err))
				os.Exit(1)
			}
			collectionEnsured = true
		}

		// Store
		if !cfg.DryRun {
			// Delete old points only for files that actually produced chunks
			// with valid embeddings, and only immediately before upserting
			// their replacements. Deleting earlier (e.g. before embedding)
			// would leave a document missing from the DB entirely if the
			// embedding call fails mid-run.
			var pathsToDelete []string
			for _, item := range readOK {
				if fileChunksCount[item.RelPath] > 0 {
					pathsToDelete = append(pathsToDelete, item.RelPath)
				}
			}
			if len(pathsToDelete) > 0 {
				slog.Debug(fmt.Sprintf("Cleaning old points from collection for: %v", pathsToDelete))
				_, _ = qdrantClient.DeleteByPaths(cfg.Collection, pathsToDelete)
			}

			pb.Describe(formatBatchStatus(batchItems, fmt.Sprintf("Storing %d chunks in Qdrant", len(batchChunks))))
			inserted, err := qdrantClient.StoreEmbeddings(cfg.Collection, batchChunks, embeddings, false, cfg.Hybrid)
			if err != nil {
				slog.Error(fmt.Sprintf("Failed to store embeddings for batch %d: %v", batchNum, err))
				for _, item := range batchItems {
					pb.IncrementWithStatus(fmt.Sprintf("✗ %s (%d chunks)", item.RelPath, fileChunksCount[item.RelPath]))
				}
				continue
			}
			totalInserted += inserted
			slog.Debug(fmt.Sprintf("Batch %d: Inserted %d record(s).", batchNum, inserted))
		} else {
			pb.Describe(formatBatchStatus(batchItems, fmt.Sprintf("Dry-run storing %d chunks", len(batchChunks))))
			_, _ = qdrantClient.StoreEmbeddings(cfg.Collection, batchChunks, embeddings, true, cfg.Hybrid)
			slog.Debug(fmt.Sprintf("Batch %d: Dry-run complete.", batchNum))
		}

		for _, item := range batchItems {
			pb.IncrementWithStatus(fmt.Sprintf("✓ %s (%d chunks)", item.RelPath, fileChunksCount[item.RelPath]))
		}
	}

	pb.Finish()

	if !cfg.DryRun {
		slog.Info(fmt.Sprintf("Total inserted: %d record(s), skipped: %d file(s).", totalInserted, totalSkipped))
	} else {
		slog.Info("Dry-run complete. No data written to Qdrant.")
	}

	slog.Info("Done.")
}

func formatBatchStatus(items []FileCheckResult, action string) string {
	if len(items) == 0 {
		return action
	}
	var paths []string
	for _, item := range items {
		paths = append(paths, item.RelPath)
	}
	return fmt.Sprintf("Processing: %s (%s)", strings.Join(paths, ", "), action)
}
