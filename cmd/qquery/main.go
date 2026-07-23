package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/blacktop/go-termimg"
	"github.com/mamorett/qingest/internal/config"
	"github.com/mamorett/qingest/internal/embed"
)

//go:embed logo.png
var logoBytes []byte

func printLogo() {
	tmpFile, err := os.CreateTemp("", "qquery-logo-*.png")
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

type QueryResult struct {
	ID      string         `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload"`
}

func queryQdrantDirect(cfg *config.QueryConfig, queryVector []float32) ([]QueryResult, error) {
	baseURL := strings.TrimRight(cfg.QdrantURL, "/")
	infoURL := fmt.Sprintf("%s/collections/%s", baseURL, cfg.Collection)

	denseName := ""
	sparseName := ""

	// Attempt to detect vector configuration
	req, err := http.NewRequest("GET", infoURL, nil)
	if err == nil {
		if cfg.QdrantAPIKey != "" {
			req.Header.Set("api-key", cfg.QdrantAPIKey)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if bodyBytes, err := io.ReadAll(resp.Body); err == nil {
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
				if err := json.Unmarshal(bodyBytes, &info); err == nil {
					if info.Result.Config.Params.Vectors != nil {
						if vMap, ok := info.Result.Config.Params.Vectors.(map[string]any); ok {
							if _, hasSize := vMap["size"]; !hasSize {
								if _, exists := vMap["dense"]; exists {
									denseName = "dense"
								} else {
									for k := range vMap {
										denseName = k
										break
									}
								}
							}
						}
					}
					if info.Result.Config.Params.SparseVectors != nil {
						if svMap, ok := info.Result.Config.Params.SparseVectors.(map[string]any); ok {
							if len(svMap) > 0 {
								if _, exists := svMap["sparse"]; exists {
									sparseName = "sparse"
								} else {
									for k := range svMap {
										sparseName = k
										break
									}
								}
							}
						}
					}
				}
			}
		}
	}

	var queryBody map[string]any
	if cfg.Hybrid {
		dName := "dense"
		if denseName != "" {
			dName = denseName
		}
		sName := "sparse"
		if sparseName != "" {
			sName = sparseName
		}

		prefetch := []any{
			map[string]any{
				"query": queryVector,
				"using": dName,
				"limit": cfg.Limit * 2,
			},
		}

		if cfg.Query != "" {
			sparseVec := embed.GenerateSparseVector(cfg.Query)
			prefetch = append(prefetch, map[string]any{
				"query": sparseVec,
				"using": sName,
				"limit": cfg.Limit * 2,
			})
		}

		queryBody = map[string]any{
			"prefetch":     prefetch,
			"query":        map[string]any{"fusion": "rrf"},
			"limit":        cfg.Limit,
			"with_payload": true,
		}
	} else {
		queryBody = map[string]any{
			"query":        queryVector,
			"limit":        cfg.Limit,
			"with_payload": true,
		}
		if denseName != "" {
			queryBody["using"] = denseName
		}
	}

	jsonBytes, err := json.Marshal(queryBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query body: %w", err)
	}

	queryURL := fmt.Sprintf("%s/collections/%s/points/query", baseURL, cfg.Collection)
	qReq, err := http.NewRequest("POST", queryURL, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create query request: %w", err)
	}
	qReq.Header.Set("Content-Type", "application/json")
	if cfg.QdrantAPIKey != "" {
		qReq.Header.Set("api-key", cfg.QdrantAPIKey)
	}

	qResp, err := http.DefaultClient.Do(qReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query request: %w", err)
	}
	defer qResp.Body.Close()

	respBytes, err := io.ReadAll(qResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read query response: %w", err)
	}

	if qResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Qdrant query returned status %d: %s", qResp.StatusCode, string(respBytes))
	}

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

	var results []QueryResult
	for _, raw := range rawPoints {
		ptMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		// Extract ID (can be string UUID or numeric)
		var id string
		switch v := ptMap["id"].(type) {
		case string:
			id = v
		case float64:
			id = fmt.Sprintf("%.0f", v)
		}

		var score float64
		switch s := ptMap["score"].(type) {
		case float64:
			score = s
		case float32:
			score = float64(s)
		}

		// payload is always at ptMap["payload"]; fall back to ptMap itself
		// if the type assertion fails (some Qdrant versions flatten the payload).
		payload, _ := ptMap["payload"].(map[string]any)
		if payload == nil {
			payload = ptMap
		}

		results = append(results, QueryResult{
			ID:      id,
			Score:   score,
			Payload: payload,
		})
	}

	return results, nil
}

// contentFieldNames are the known field names that hold text content.
var contentFieldNames = []string{"content", "text", "page_content", "body", "chunk", "paragraph", "document"}

// pathFieldNames are the known field names that hold file paths.
var pathFieldNames = []string{"file_path", "source", "file", "path", "filename", "doc_id"}

// skipFieldsForFallback are field names that should never be treated as content.
var skipFieldsForFallback = map[string]bool{
	"file_path": true, "file_hash": true, "indexed_at": true,
	"source": true, "id": true, "file": true, "path": true,
	"filename": true, "doc_id": true, "chunk_index": true,
	"score": true, "version": true,
}

func parsePayloadString(val any) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var sb strings.Builder
		for _, item := range v {
			if s, ok := item.(string); ok {
				sb.WriteString(s)
				sb.WriteString("\n")
			}
		}
		return strings.TrimSpace(sb.String())
	case []string:
		return strings.TrimSpace(strings.Join(v, "\n"))
	}
	return ""
}

// extractContent extracts text content from a payload using a 4-tier strategy.
func extractContent(pMap map[string]any) string {
	if pMap == nil {
		return ""
	}

	// Tier 1: direct top-level known field names
	for _, key := range contentFieldNames {
		if s := parsePayloadString(pMap[key]); s != "" {
			return s
		}
	}

	// Tier 2: search inside any nested map (e.g. metadata)
	for _, val := range pMap {
		nested, ok := val.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range contentFieldNames {
			if s := parsePayloadString(nested[key]); s != "" {
				return s
			}
		}
	}

	// Tier 3: JSON-parse any string value that looks like a JSON object
	for _, val := range pMap {
		strVal, ok := val.(string)
		if !ok || !strings.HasPrefix(strings.TrimSpace(strVal), "{") {
			continue
		}
		var nested map[string]any
		if err := json.Unmarshal([]byte(strVal), &nested); err != nil {
			continue
		}
		for _, key := range contentFieldNames {
			if s := parsePayloadString(nested[key]); s != "" {
				return s
			}
		}
	}

	// Tier 4: nuclear — return the longest string in the payload
	longest := ""
	for key, val := range pMap {
		if skipFieldsForFallback[key] {
			continue
		}
		if s := parsePayloadString(val); len(s) > len(longest) {
			longest = s
		}
	}
	return longest
}

// extractFilePath extracts the source file path from a payload.
func extractFilePath(pMap map[string]any) string {
	if pMap == nil {
		return "unknown"
	}
	for _, key := range pathFieldNames {
		if s, ok := pMap[key].(string); ok && s != "" {
			return s
		}
	}
	// Check nested maps
	for _, val := range pMap {
		if nested, ok := val.(map[string]any); ok {
			for _, key := range pathFieldNames {
				if s, ok := nested[key].(string); ok && s != "" {
					return s
				}
			}
		}
	}
	return "unknown"
}

func main() {
	printLogo()

	cfg, err := config.LoadQueryConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Embedding query: '%s' using model '%s'...\n", cfg.Query, cfg.EmbedModel)
	vecs, err := embed.EmbedBatch([]string{cfg.Query}, cfg.EmbedURL, cfg.EmbedModel, 1, nil)
	if err != nil || len(vecs) == 0 {
		fmt.Fprintf(os.Stderr, "Failed to generate query embedding: %v\n", err)
		os.Exit(1)
	}
	queryVector := vecs[0]
	fmt.Printf("Successfully generated query embedding (dim=%d).\n", len(queryVector))

	fmt.Printf("Searching Qdrant collection '%s' at %s (hybrid=%t, score-threshold=%.4f)...\n",
		cfg.Collection, cfg.QdrantURL, cfg.Hybrid, cfg.ScoreThreshold)

	results, err := queryQdrantDirect(cfg, queryVector)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to query Qdrant: %v\n", err)
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Println("No matching results found.")
		return
	}

	fmt.Printf("\n%d result(s) returned:\n%s\n", len(results), strings.Repeat("=", 80))

	for i, res := range results {
		fp := extractFilePath(res.Payload)
		content := extractContent(res.Payload)

		// Mark low-confidence results clearly but still show their content
		qualifier := ""
		if res.Score < cfg.ScoreThreshold {
			qualifier = " [LOW SCORE — below threshold]"
		}

		if content == "" {
			rawBytes, _ := json.MarshalIndent(res.Payload, "", "  ")
			content = fmt.Sprintf("[ERROR: no content found in payload. Raw payload:\n%s\n]", string(rawBytes))
		}

		fmt.Printf("Result #%d | Score: %.4f%s | Source: %s\n", i+1, res.Score, qualifier, fp)
		fmt.Println(strings.Repeat("-", 80))
		fmt.Println(content)
		fmt.Println(strings.Repeat("=", 80))
	}
}
