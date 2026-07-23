package embed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingItem struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type embeddingResponse struct {
	Data []embeddingItem `json:"data"`
}

// NewClient returns a configured *http.Client with retry logic for embedding calls.
func NewClient() *http.Client {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 3
	retryClient.RetryWaitMin = 1 * time.Second
	retryClient.RetryWaitMax = 4 * time.Second
	retryClient.HTTPClient.Timeout = 300 * time.Second
	retryClient.Logger = nil // Silent logger for retries
	return retryClient.StandardClient()
}

// EmbedBatch calls an OpenAI-compatible /v1/embeddings endpoint in sub-batches.
func EmbedBatch(texts []string, embedURL, model string, batchSize int, onProgress func(current, total int)) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if batchSize <= 0 {
		batchSize = 128
	}

	if onProgress != nil {
		onProgress(0, len(texts))
	}

	httpClient := NewClient()
	endpoint := fmt.Sprintf("%s/embeddings", strings.TrimRight(embedURL, "/"))

	allEmbeddings := make([][]float32, 0, len(texts))
	globalDim := -1

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[i:end]

		slog.Debug(fmt.Sprintf("Embedding batch %d–%d / %d", i+1, end, len(texts)))

		reqBody, err := json.Marshal(embeddingRequest{
			Model: model,
			Input: batch,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
		}

		req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(reqBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create embedding request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("embedding request failed: %w", err)
		}

		respBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read embedding response body: %w", err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("embedding API returned status %d: %s", resp.StatusCode, string(respBytes))
		}

		var parsedResp embeddingResponse
		if err := json.Unmarshal(respBytes, &parsedResp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal embedding response: %w", err)
		}

		if len(parsedResp.Data) != len(batch) {
			return nil, fmt.Errorf("expected %d embeddings in response, got %d", len(batch), len(parsedResp.Data))
		}

		// Check if we have a valid index permutation from the response
		useIndex := true
		seen := make([]bool, len(batch))
		for _, item := range parsedResp.Data {
			if item.Index < 0 || item.Index >= len(batch) || seen[item.Index] {
				useIndex = false
				break
			}
			seen[item.Index] = true
		}

		batchEmbeddings := make([][]float32, len(batch))
		if useIndex {
			for _, item := range parsedResp.Data {
				batchEmbeddings[item.Index] = item.Embedding
			}
		} else {
			for idx, item := range parsedResp.Data {
				batchEmbeddings[idx] = item.Embedding
			}
		}

		// Validate every returned vector: reject nil/empty embeddings and
		// inconsistent dimensions. A silently empty vector stored in Qdrant
		// produces a retrievable point with no usable content — fail the
		// whole batch instead so the file is retried, not corrupted.
		batchDim := -1
		for idx, vec := range batchEmbeddings {
			if len(vec) == 0 {
				return nil, fmt.Errorf("embedding API returned an empty vector for input %d of batch starting at %d (text: %.40q)", idx, i, batch[idx])
			}
			if batchDim == -1 {
				batchDim = len(vec)
			} else if len(vec) != batchDim {
				return nil, fmt.Errorf("inconsistent embedding dimensions in batch starting at %d: input 0 has dim %d, input %d has dim %d", i, batchDim, idx, len(vec))
			}
		}
		if globalDim == -1 {
			globalDim = batchDim
		} else if batchDim != globalDim {
			return nil, fmt.Errorf("embedding dimension changed between batches: first batch had dim %d, batch starting at %d has dim %d", globalDim, i, batchDim)
		}

		allEmbeddings = append(allEmbeddings, batchEmbeddings...)

		if onProgress != nil {
			onProgress(end, len(texts))
		}
	}

	return allEmbeddings, nil
}
