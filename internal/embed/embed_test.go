package embed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbedBatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("expected path /v1/embeddings, got %s", r.URL.Path)
		}

		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}

		resp := embeddingResponse{
			Data: make([]embeddingItem, len(req.Input)),
		}
		for i := range req.Input {
			resp.Data[i] = embeddingItem{
				Embedding: []float32{0.1 * float32(i+1), 0.2 * float32(i+1)},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	texts := []string{"hello", "world", "foo", "bar"}
	vecs, err := EmbedBatch(texts, ts.URL+"/v1", "test-model", 2, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(vecs) != 4 {
		t.Fatalf("expected 4 vectors, got %d", len(vecs))
	}
	if len(vecs[0]) != 2 {
		t.Fatalf("expected 2 dimensions, got %d", len(vecs[0]))
	}
}

func TestEmbedBatchRejectsEmptyVector(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}

		resp := embeddingResponse{
			Data: make([]embeddingItem, len(req.Input)),
		}
		for i := range req.Input {
			resp.Data[i] = embeddingItem{Index: i, Embedding: []float32{0.1, 0.2}}
		}
		// Second input comes back with an empty embedding — must hard-fail.
		if len(req.Input) > 1 {
			resp.Data[1].Embedding = nil
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	_, err := EmbedBatch([]string{"hello", "world"}, ts.URL+"/v1", "test-model", 2, nil)
	if err == nil {
		t.Fatal("expected error for empty embedding vector, got nil")
	}
}

func TestEmbedBatchRejectsInconsistentDims(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}

		resp := embeddingResponse{
			Data: make([]embeddingItem, len(req.Input)),
		}
		for i := range req.Input {
			dim := 2
			if i == len(req.Input)-1 {
				dim = 3 // last vector has a different dimension
			}
			vec := make([]float32, dim)
			for j := range vec {
				vec[j] = 0.1
			}
			resp.Data[i] = embeddingItem{Index: i, Embedding: vec}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	_, err := EmbedBatch([]string{"hello", "world"}, ts.URL+"/v1", "test-model", 2, nil)
	if err == nil {
		t.Fatal("expected error for inconsistent embedding dimensions, got nil")
	}
}

func TestEmbedBatchOutOfOrder(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}

		resp := embeddingResponse{
			Data: make([]embeddingItem, len(req.Input)),
		}
		// Return items in reverse order
		for i := range req.Input {
			revIdx := len(req.Input) - 1 - i
			resp.Data[i] = embeddingItem{
				Index:     revIdx,
				Embedding: []float32{0.1 * float32(revIdx+1), 0.2 * float32(revIdx+1)},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	texts := []string{"hello", "world"}
	vecs, err := EmbedBatch(texts, ts.URL+"/v1", "test-model", 2, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	// Check that vectors were aligned back to correct positions
	if vecs[0][0] != 0.1 || vecs[0][1] != 0.2 {
		t.Errorf("expected vecs[0] to be [0.1, 0.2], got %v", vecs[0])
	}
	if vecs[1][0] != 0.2 || vecs[1][1] != 0.4 {
		t.Errorf("expected vecs[1] to be [0.2, 0.4], got %v", vecs[1])
	}
}
