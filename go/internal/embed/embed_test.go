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
	vecs, err := EmbedBatch(texts, ts.URL+"/v1", "test-model", 2)
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
