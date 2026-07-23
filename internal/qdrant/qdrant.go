package qdrant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mamorett/qingest/internal/chunk"
	"github.com/mamorett/qingest/internal/embed"
)

type Client struct {
	BaseURL            string
	APIKey             string
	HTTPClient         *http.Client
	denseVectorName    string
	sparseVectorName   string
	hasDetectedVectors bool
}

type SearchResult struct {
	ID      string         `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload"`
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *Client) doRequest(method, endpointPath string, body any) ([]byte, int, error) {
	url := fmt.Sprintf("%s%s", c.BaseURL, endpointPath)
	var bodyReader io.Reader
	if body != nil {
		jsonBytes, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonBytes)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("api-key", c.APIKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request to %s failed: %w", url, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response body: %w", err)
	}

	return respBytes, resp.StatusCode, nil
}

func (c *Client) detectVectors(collection string) error {
	if c.hasDetectedVectors {
		return nil
	}

	respBytes, status, err := c.doRequest("GET", fmt.Sprintf("/collections/%s", collection), nil)
	if err != nil {
		if status == http.StatusNotFound {
			// Collection does not exist yet.
			c.denseVectorName = ""
			c.sparseVectorName = ""
			return nil
		}
		return fmt.Errorf("failed to check collection info: %w", err)
	}

	var info struct {
		Result struct {
			Config struct {
				Params struct {
					Vectors       any `json:"vectors"`
					SparseVectors any `json:"sparse_vectors"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}

	if err := json.Unmarshal(respBytes, &info); err != nil {
		return fmt.Errorf("failed to unmarshal collection info: %w", err)
	}

	// 1. Detect dense vector name
	if info.Result.Config.Params.Vectors != nil {
		switch v := info.Result.Config.Params.Vectors.(type) {
		case map[string]any:
			if _, hasSize := v["size"]; hasSize {
				c.denseVectorName = ""
			} else {
				// It is a map of named vectors.
				if _, exists := v["dense"]; exists {
					c.denseVectorName = "dense"
				} else {
					for key, val := range v {
						if valMap, ok := val.(map[string]any); ok {
							if _, hasSize := valMap["size"]; hasSize {
								c.denseVectorName = key
								break
							}
						}
					}
				}
			}
		default:
			c.denseVectorName = ""
		}
	}

	// 2. Detect sparse vector name
	if info.Result.Config.Params.SparseVectors != nil {
		switch sv := info.Result.Config.Params.SparseVectors.(type) {
		case map[string]any:
			if len(sv) > 0 {
				if _, exists := sv["sparse"]; exists {
					c.sparseVectorName = "sparse"
				} else {
					for key := range sv {
						c.sparseVectorName = key
						break
					}
				}
			}
		}
	}

	c.hasDetectedVectors = true
	return nil
}

// CreateCollectionIfNotExists ensures collection exists and creates payload indexes.
func (c *Client) CreateCollectionIfNotExists(collection string, vectorDim int, hybrid bool) error {
	// Check existence
	_, status, err := c.doRequest("GET", fmt.Sprintf("/collections/%s", collection), nil)
	if err == nil && status == http.StatusOK {
		slog.Info(fmt.Sprintf("Collection '%s' already exists.", collection))
		return c.detectVectors(collection)
	}

	slog.Info(fmt.Sprintf("Creating collection '%s' (vector dim=%d, hybrid=%t).", collection, vectorDim, hybrid))

	var createBody map[string]any
	if hybrid {
		createBody = map[string]any{
			"vectors": map[string]any{
				"dense": map[string]any{
					"size":     vectorDim,
					"distance": "Cosine",
				},
			},
			"sparse_vectors": map[string]any{
				"sparse": map[string]any{},
			},
		}
	} else {
		createBody = map[string]any{
			"vectors": map[string]any{
				"size":     vectorDim,
				"distance": "Cosine",
			},
		}
	}

	respBytes, status, err := c.doRequest("PUT", fmt.Sprintf("/collections/%s", collection), createBody)
	if err != nil || (status != http.StatusOK && status != http.StatusCreated) {
		return fmt.Errorf("failed to create collection '%s' (status %d): %s (err: %v)", collection, status, string(respBytes), err)
	}

	if hybrid {
		c.denseVectorName = "dense"
		c.sparseVectorName = "sparse"
	} else {
		c.denseVectorName = ""
		c.sparseVectorName = ""
	}
	c.hasDetectedVectors = true

	// Create payload indexes
	indexes := []struct {
		field  string
		schema string
	}{
		{"file_path", "keyword"},
		{"content", "text"},
		{"chunk_index", "integer"},
		{"indexed_at", "keyword"},
	}

	for _, idx := range indexes {
		indexBody := map[string]any{
			"field_name":   idx.field,
			"field_schema": idx.schema,
		}
		indexResp, idxStatus, idxErr := c.doRequest("PUT", fmt.Sprintf("/collections/%s/index", collection), indexBody)
		if idxErr != nil || idxStatus != http.StatusOK {
			slog.Warn(fmt.Sprintf("Failed to create index for field '%s' (status %d): %s", idx.field, idxStatus, string(indexResp)))
		}
	}

	slog.Info(fmt.Sprintf("Collection '%s' created and payload indexes ensured.", collection))
	return nil
}

// GetAllIndexedHashes queries Qdrant for indexed file paths and their hashes in batches.
func (c *Client) GetAllIndexedHashes(collection string, filePaths []string, batchSize int) (map[string]string, error) {
	if err := c.detectVectors(collection); err != nil {
		slog.Warn(fmt.Sprintf("Failed to detect collection vectors: %v", err))
	}
	pathHashes := make(map[string]string)
	if len(filePaths) == 0 {
		return pathHashes, nil
	}

	if batchSize <= 0 {
		batchSize = 500
	}

	totalPaths := len(filePaths)
	slog.Debug(fmt.Sprintf("Querying Qdrant for %d indexed file paths to find already embedded documents...", totalPaths))

	totalBatches := (totalPaths + batchSize - 1) / batchSize

	for i := 0; i < totalPaths; i += batchSize {
		end := i + batchSize
		if end > totalPaths {
			end = totalPaths
		}
		batch := filePaths[i:end]
		batchNum := (i / batchSize) + 1

		slog.Debug(fmt.Sprintf("Querying DB hashes batch %d/%d (paths %d–%d)...", batchNum, totalBatches, i+1, end))

		var offset any = nil
		for {
			scrollBody := map[string]any{
				"filter": map[string]any{
					"must": []any{
						map[string]any{
							"key": "file_path",
							"match": map[string]any{
								"any": batch,
							},
						},
					},
				},
				"limit":        10000,
				"with_payload": []string{"file_path", "file_hash"},
				"with_vector":  false,
			}
			if offset != nil {
				scrollBody["offset"] = offset
			}

			respBytes, status, err := c.doRequest("POST", fmt.Sprintf("/collections/%s/points/scroll", collection), scrollBody)
			if err != nil || status != http.StatusOK {
				slog.Warn(fmt.Sprintf("Failed to query indexed paths from Qdrant (batch %d-%d): status %d, err: %v", i+1, end, status, err))
				break
			}

			var scrollResp struct {
				Result struct {
					Points []struct {
						Payload map[string]any `json:"payload"`
					} `json:"points"`
					NextPageOffset any `json:"next_page_offset"`
				} `json:"result"`
			}

			if err := json.Unmarshal(respBytes, &scrollResp); err != nil {
				slog.Warn(fmt.Sprintf("Failed to parse scroll response: %v", err))
				break
			}

			for _, pt := range scrollResp.Result.Points {
				if fp, ok := pt.Payload["file_path"].(string); ok && fp != "" {
					fh, _ := pt.Payload["file_hash"].(string)
					current := pathHashes[fp]
					if current == "" || current == "__legacy__" {
						if fh != "" {
							pathHashes[fp] = fh
						} else {
							pathHashes[fp] = "__legacy__"
						}
					}
				}
			}

			if scrollResp.Result.NextPageOffset == nil {
				break
			}
			offset = scrollResp.Result.NextPageOffset
		}
	}

	slog.Debug(fmt.Sprintf("Retrieved %d unique indexed file path(s) from Qdrant.", len(pathHashes)))
	return pathHashes, nil
}

// DeleteByPaths deletes all points matching any of the given file_path values.
func (c *Client) DeleteByPaths(collection string, filePaths []string) (int, error) {
	if len(filePaths) == 0 {
		return 0, nil
	}

	deleteBody := map[string]any{
		"filter": map[string]any{
			"must": []any{
				map[string]any{
					"key": "file_path",
					"match": map[string]any{
						"any": filePaths,
					},
				},
			},
		},
	}

	respBytes, status, err := c.doRequest("POST", fmt.Sprintf("/collections/%s/points/delete?wait=true", collection), deleteBody)
	if err != nil || status != http.StatusOK {
		slog.Error(fmt.Sprintf("Failed to delete records for file_paths in Qdrant (status %d): %s", status, string(respBytes)))
		return 0, err
	}

	return len(filePaths), nil
}

// StoreEmbeddings stores chunks + embeddings into Qdrant in batches.
func (c *Client) StoreEmbeddings(collection string, chunks []chunk.Chunk, embeddings [][]float32, dryRun, hybrid bool) (int, error) {
	if len(chunks) == 0 || len(embeddings) == 0 {
		return 0, nil
	}

	if dryRun {
		for _, ch := range chunks {
			slog.Info(fmt.Sprintf("[DRY-RUN] Would insert chunk from %s (idx %d)", ch.FilePath, ch.ChunkIndex))
		}
		return len(chunks), nil
	}

	if err := c.detectVectors(collection); err != nil {
		return 0, fmt.Errorf("failed to detect collection vectors: %w", err)
	}

	nowISO := time.Now().UTC().Format(time.RFC3339)
	points := make([]map[string]any, 0, len(chunks))

	for i, ch := range chunks {
		emb := embeddings[i]
		pointID := uuid.New().String()

		payload := map[string]any{
			"content":     ch.Content,
			"file_path":   ch.FilePath,
			"file_hash":   ch.FileHash,
			"chunk_index": ch.ChunkIndex,
			"metadata":    ch.Metadata,
			"indexed_at":  nowISO,
		}

		var vectorData any
		if c.denseVectorName != "" {
			vecMap := map[string]any{c.denseVectorName: emb}
			if c.sparseVectorName != "" {
				vecMap[c.sparseVectorName] = embed.GenerateSparseVector(ch.Content)
			}
			vectorData = vecMap
		} else {
			vectorData = emb
		}

		points = append(points, map[string]any{
			"id":      pointID,
			"vector":  vectorData,
			"payload": payload,
		})
	}

	upsertBatchSize := 100
	insertedCount := 0

	for idx := 0; idx < len(points); idx += upsertBatchSize {
		end := idx + upsertBatchSize
		if end > len(points) {
			end = len(points)
		}
		batch := points[idx:end]

		upsertBody := map[string]any{
			"points": batch,
		}

		respBytes, status, err := c.doRequest("PUT", fmt.Sprintf("/collections/%s/points?wait=true", collection), upsertBody)
		if err != nil || status != http.StatusOK {
			return insertedCount, fmt.Errorf("failed to upsert batch to Qdrant (status %d): %s", status, string(respBytes))
		}
		insertedCount += len(batch)
	}

	return insertedCount, nil
}

// QueryPoints performs standard or hybrid query on Qdrant.
func (c *Client) QueryPoints(collection string, queryVector []float32, queryText string, limit int, hybrid bool) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}

	if err := c.detectVectors(collection); err != nil {
		return nil, fmt.Errorf("failed to detect collection vectors: %w", err)
	}

	var queryBody map[string]any
	if hybrid {
		denseName := "dense"
		if c.denseVectorName != "" {
			denseName = c.denseVectorName
		}
		
		prefetch := []any{
			map[string]any{
				"query": queryVector,
				"using": denseName,
				"limit": limit * 2,
			},
		}

		if c.sparseVectorName != "" && queryText != "" {
			sparseVec := embed.GenerateSparseVector(queryText)
			prefetch = append(prefetch, map[string]any{
				"query": sparseVec,
				"using": c.sparseVectorName,
				"limit": limit * 2,
			})
		}

		queryBody = map[string]any{
			"prefetch": prefetch,
			"query": map[string]any{
				"fusion": "rrf",
			},
			"limit":        limit,
			"with_payload": true,
		}
	} else {
		queryBody = map[string]any{
			"query":        queryVector,
			"limit":        limit,
			"with_payload": true,
		}
		if c.denseVectorName != "" {
			queryBody["using"] = c.denseVectorName
		}
	}

	respBytes, status, err := c.doRequest("POST", fmt.Sprintf("/collections/%s/points/query", collection), queryBody)
	if err != nil || status != http.StatusOK {
		return nil, fmt.Errorf("failed to query Qdrant (status %d): %s", status, string(respBytes))
	}

	// Parse response
	var genericResp map[string]any
	if err := json.Unmarshal(respBytes, &genericResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Qdrant query response: %w", err)
	}

	resObj, ok := genericResp["result"]
	if !ok || resObj == nil {
		return nil, nil
	}

	var rawPoints []any
	switch v := resObj.(type) {
	case []any:
		rawPoints = v
	case map[string]any:
		if pts, exists := v["points"].([]any); exists {
			rawPoints = pts
		}
	}

	var results []SearchResult
	for _, raw := range rawPoints {
		ptMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		id, _ := ptMap["id"].(string)

		var score float64
		switch s := ptMap["score"].(type) {
		case float64:
			score = s
		case float32:
			score = float64(s)
		}

		payload, _ := ptMap["payload"].(map[string]any)

		results = append(results, SearchResult{
			ID:      id,
			Score:   score,
			Payload: payload,
		})
	}

	return results, nil
}
