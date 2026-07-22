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
func EmbedBatch(texts []string, embedURL, model string, batchSize int) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if batchSize <= 0 {
		batchSize = 128
	}

	httpClient := NewClient()
	endpoint := fmt.Sprintf("%s/embeddings", strings.TrimRight(embedURL, "/"))

	allEmbeddings := make([][]float32, 0, len(texts))

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[i:end]

		slog.Info(fmt.Sprintf("Embedding batch %d–%d / %d", i+1, end, len(texts)))

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

		for _, item := range parsedResp.Data {
			allEmbeddings = append(allEmbeddings, item.Embedding)
		}
	}

	return allEmbeddings, nil
}
