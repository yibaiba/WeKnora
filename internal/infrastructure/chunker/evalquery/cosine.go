package evalquery

import (
	"fmt"
	"math"
)

func cosineSimilarity(a, b []float32) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("embedding dimension mismatch: %d vs %d", len(a), len(b))
	}
	if len(a) == 0 {
		return 0, fmt.Errorf("empty embedding")
	}
	var dot, normA, normB float64
	for i := range a {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0, nil
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB)), nil
}

type vectorCache struct {
	embed  Embedder
	byText map[string][]float32
}

func newVectorCache(embed Embedder) *vectorCache {
	return &vectorCache{embed: embed, byText: make(map[string][]float32)}
}

func (c *vectorCache) getMany(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	missingIdx := make([]int, 0)
	missing := make([]string, 0)
	for i, text := range texts {
		if vec, ok := c.byText[text]; ok {
			out[i] = vec
			continue
		}
		missingIdx = append(missingIdx, i)
		missing = append(missing, text)
	}
	if len(missing) == 0 {
		return out, nil
	}
	got, err := c.embed.Embed(missing)
	if err != nil {
		return nil, err
	}
	if len(got) != len(missing) {
		return nil, fmt.Errorf("embedder returned %d vectors, want %d", len(got), len(missing))
	}
	for i, vec := range got {
		out[missingIdx[i]] = vec
		c.byText[missing[i]] = vec
	}
	return out, nil
}
