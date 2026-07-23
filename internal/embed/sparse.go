package embed

import (
	"hash/fnv"
	"math"
	"regexp"
	"sort"
	"strings"
)

type SparseVector struct {
	Indices []uint32  `json:"indices"`
	Values  []float32 `json:"values"`
}

var wordRegexp = regexp.MustCompile(`[a-zA-Z0-9_]+`)

// GenerateSparseVector converts a text string into a sparse vector representation using token hashing and term frequency.
func GenerateSparseVector(text string) SparseVector {
	text = strings.ToLower(text)
	words := wordRegexp.FindAllString(text, -1)

	if len(words) == 0 {
		return SparseVector{
			Indices: []uint32{},
			Values:  []float32{},
		}
	}

	// Count raw frequencies
	counts := make(map[string]int)
	for _, w := range words {
		// Skip short words
		if len(w) > 1 {
			counts[w]++
		}
	}

	if len(counts) == 0 {
		return SparseVector{
			Indices: []uint32{},
			Values:  []float32{},
		}
	}

	// Map to unique hashed indices
	indexMap := make(map[uint32]float32)
	for word, count := range counts {
		h := fnv.New32a()
		h.Write([]byte(word))
		idx := h.Sum32()

		// Simple term frequency (TF) scoring. Let's use log-scaling:
		// TF = 1 + log(count)
		val := float32(1.0 + math.Log(float64(count)))
		indexMap[idx] = val
	}

	// Extract and sort indices
	indices := make([]uint32, 0, len(indexMap))
	for idx := range indexMap {
		indices = append(indices, idx)
	}
	sort.Slice(indices, func(i, j int) bool {
		return indices[i] < indices[j]
	})

	// Align values with sorted indices
	values := make([]float32, len(indices))
	for i, idx := range indices {
		values[i] = indexMap[idx]
	}

	return SparseVector{
		Indices: indices,
		Values:  values,
	}
}
