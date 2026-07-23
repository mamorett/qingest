package qdrant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mamorett/qingest/internal/chunk"
)

func TestQdrantClient(t *testing.T) {
	mux := http.NewServeMux()

	// GET /collections/test
	mux.HandleFunc("/collections/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == "PUT" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
	})

	// PUT /collections/test/index
	mux.HandleFunc("/collections/test/index", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// POST /collections/test/points/scroll
	mux.HandleFunc("/collections/test/points/scroll", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"result": map[string]any{
				"points": []map[string]any{
					{
						"id": "1",
						"payload": map[string]any{
							"file_path": "doc1.md",
							"file_hash": "hash1",
						},
					},
				},
				"next_page_offset": nil,
			},
			"status": "ok",
		}
		json.NewEncoder(w).Encode(resp)
	})

	// POST /collections/test/points/delete
	mux.HandleFunc("/collections/test/points/delete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// PUT /collections/test/points
	mux.HandleFunc("/collections/test/points", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// POST /collections/test/points/query
	mux.HandleFunc("/collections/test/points/query", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"result": map[string]any{
				"points": []map[string]any{
					{
						"id":    "pt1",
						"score": 0.95,
						"payload": map[string]any{
							"file_path": "doc1.md",
							"content":   "hello",
						},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := NewClient(ts.URL, "")

	// 1. Create collection
	if err := client.CreateCollectionIfNotExists("test", 128, false); err != nil {
		t.Fatalf("CreateCollectionIfNotExists failed: %v", err)
	}

	// 2. Scroll hashes
	hashes, err := client.GetAllIndexedHashes("test", []string{"doc1.md"}, 100)
	if err != nil {
		t.Fatalf("GetAllIndexedHashes failed: %v", err)
	}
	if hashes["doc1.md"] != "hash1" {
		t.Errorf("expected hash1, got %s", hashes["doc1.md"])
	}

	// 3. Delete by paths
	deleted, err := client.DeleteByPaths("test", []string{"doc1.md"})
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteByPaths failed: deleted=%d, err=%v", deleted, err)
	}

	// 4. Store embeddings
	chunks := []chunk.Chunk{
		{FilePath: "doc1.md", ChunkIndex: 0, Content: "hello", FileHash: "hash1"},
	}
	embeddings := [][]float32{{0.1, 0.2}}
	stored, err := client.StoreEmbeddings("test", chunks, embeddings, false, false)
	if err != nil || stored != 1 {
		t.Fatalf("StoreEmbeddings failed: stored=%d, err=%v", stored, err)
	}

	// 5. Query
	results, err := client.QueryPoints("test", []float32{0.1, 0.2}, "hello", 5, false)
	if err != nil || len(results) != 1 {
		t.Fatalf("QueryPoints failed: len=%d, err=%v", len(results), err)
	}
	if results[0].Score != 0.95 {
		t.Errorf("expected score 0.95, got %f", results[0].Score)
	}
}

func TestStoreEmbeddingsRejectsGarbage(t *testing.T) {
	// Server that should NEVER receive an upsert — if it does, the guard failed.
	mux := http.NewServeMux()
	mux.HandleFunc("/collections/test/points", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upsert endpoint was hit despite invalid input")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/collections/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{"config":{"params":{"vectors":{"size":2}}}}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := NewClient(ts.URL, "")

	// Empty content must be refused.
	_, err := client.StoreEmbeddings("test",
		[]chunk.Chunk{{FilePath: "doc.md", ChunkIndex: 0, Content: "   ", FileHash: "h"}},
		[][]float32{{0.1, 0.2}}, false, false)
	if err == nil {
		t.Error("expected error for empty content, got nil")
	}

	// Empty vector must be refused.
	_, err = client.StoreEmbeddings("test",
		[]chunk.Chunk{{FilePath: "doc.md", ChunkIndex: 0, Content: "valid content here", FileHash: "h"}},
		[][]float32{{}}, false, false)
	if err == nil {
		t.Error("expected error for empty vector, got nil")
	}

	// Count mismatch must be refused before any network call.
	_, err = client.StoreEmbeddings("test",
		[]chunk.Chunk{
			{FilePath: "doc.md", ChunkIndex: 0, Content: "valid content here", FileHash: "h"},
			{FilePath: "doc.md", ChunkIndex: 1, Content: "more valid content", FileHash: "h"},
		},
		[][]float32{{0.1, 0.2}}, false, false)
	if err == nil {
		t.Error("expected error for chunk/embedding count mismatch, got nil")
	}
}
