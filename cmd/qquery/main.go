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

	"github.com/mamorett/qingest/internal/config"
	"github.com/mamorett/qingest/internal/embed"
	"github.com/mamorett/qingest/internal/logo"
)

//go:embed logo.png
var logoBytes []byte

func printLogo() {
	logo.PrintLogo(logoBytes)
}

type QueryResult struct {
	ID      string         `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload"`
}

func executeQdrantQueryPost(baseURL, collection, apiKey string, queryBody map[string]any) ([]QueryResult, error) {
	jsonBytes, err := json.Marshal(queryBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query body: %w", err)
	}

	queryURL := fmt.Sprintf("%s/collections/%s/points/query", baseURL, collection)
	qReq, err := http.NewRequest("POST", queryURL, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create query request: %w", err)
	}
	qReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		qReq.Header.Set("api-key", apiKey)
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

func queryQdrantDirect(cfg *config.QueryConfig, queryVector []float32) ([]QueryResult, error) {
	baseURL := strings.TrimRight(cfg.QdrantURL, "/")
	infoURL := fmt.Sprintf("%s/collections/%s", baseURL, cfg.Collection)

	denseName := ""
	sparseName := ""

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
	useHybrid := cfg.Hybrid || sparseName != ""
	if useHybrid {
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

	results, err := executeQdrantQueryPost(baseURL, cfg.Collection, cfg.QdrantAPIKey, queryBody)
	if err == nil && len(results) > 0 {
		return results, nil
	}

	// Fallback to simple dense query if hybrid failed (e.g. collection parameter mismatch)
	if useHybrid {
		fallbackBody := map[string]any{
			"query":        queryVector,
			"limit":        cfg.Limit,
			"with_payload": true,
		}
		if denseName != "" {
			fallbackBody["using"] = denseName
		}
		return executeQdrantQueryPost(baseURL, cfg.Collection, cfg.QdrantAPIKey, fallbackBody)
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

func extractContent(pMap map[string]any) string {
	if pMap == nil {
		return ""
	}

	for _, key := range contentFieldNames {
		if s := parsePayloadString(pMap[key]); s != "" {
			return s
		}
	}

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

func extractFilePath(pMap map[string]any) string {
	if pMap == nil {
		return "unknown"
	}
	for _, key := range pathFieldNames {
		if s, ok := pMap[key].(string); ok && s != "" {
			return s
		}
	}
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

func extractHeadingMetadata(pMap map[string]any) (docTitle string, headingContext string) {
	if pMap == nil {
		return "", ""
	}
	if meta, ok := pMap["metadata"].(map[string]any); ok {
		if dt, ok := meta["doc_title"].(string); ok {
			docTitle = dt
		}
		if hc, ok := meta["heading_context"].(string); ok {
			headingContext = hc
		}
	}
	if docTitle == "" {
		if dt, ok := pMap["doc_title"].(string); ok {
			docTitle = dt
		}
	}
	if headingContext == "" {
		if hc, ok := pMap["heading_context"].(string); ok {
			headingContext = hc
		}
	}
	return docTitle, headingContext
}

func extractChunkPos(pMap map[string]any) (chunkIdx int, totalChunks int, ok bool) {
	if pMap == nil {
		return 0, 0, false
	}

	var meta map[string]any
	if m, exists := pMap["metadata"].(map[string]any); exists {
		meta = m
	} else {
		meta = pMap
	}

	idxFound, totalFound := false, false

	if v, exists := meta["chunk_index"]; exists {
		switch num := v.(type) {
		case float64:
			chunkIdx = int(num)
			idxFound = true
		case int:
			chunkIdx = num
			idxFound = true
		}
	}

	if v, exists := meta["total_chunks"]; exists {
		switch num := v.(type) {
		case float64:
			totalChunks = int(num)
			totalFound = true
		case int:
			totalChunks = num
			totalFound = true
		}
	}

	return chunkIdx, totalChunks, (idxFound && totalFound)
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

	fmt.Printf("Searching endpoint '%s' -> Qdrant collection '%s' at %s (hybrid=%t, score-threshold=%.4f)...\n",
		cfg.Endpoint, cfg.Collection, cfg.QdrantURL, cfg.Hybrid, cfg.ScoreThreshold)

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
		docTitle, headingContext := extractHeadingMetadata(res.Payload)

		if content == "" {
			rawBytes, _ := json.MarshalIndent(res.Payload, "", "  ")
			content = fmt.Sprintf("[ERROR: no content found in payload. Raw payload:\n%s\n]", string(rawBytes))
		}

		fmt.Printf("Result #%d | Score: %.4f | Source: %s\n", i+1, res.Score, fp)

		var contextParts []string
		if docTitle != "" {
			contextParts = append(contextParts, fmt.Sprintf("Document: %s", docTitle))
		}
		if headingContext != "" && headingContext != docTitle {
			contextParts = append(contextParts, fmt.Sprintf("Section: %s", headingContext))
		}
		if chunkIdx, totalChunks, ok := extractChunkPos(res.Payload); ok {
			contextParts = append(contextParts, fmt.Sprintf("Chunk: %d/%d", chunkIdx+1, totalChunks))
		}

		if len(contextParts) > 0 {
			fmt.Printf("Context: %s\n", strings.Join(contextParts, " | "))
		}

		fmt.Println(strings.Repeat("-", 80))
		fmt.Println(content)
		fmt.Println(strings.Repeat("=", 80))
	}
}
