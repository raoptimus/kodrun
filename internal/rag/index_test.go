package rag

import (
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float64
		want float64
	}{
		{"identical", []float64{1, 0, 0}, []float64{1, 0, 0}, 1.0},
		{"orthogonal", []float64{1, 0, 0}, []float64{0, 1, 0}, 0.0},
		{"opposite", []float64{1, 0}, []float64{-1, 0}, -1.0},
		{"empty", nil, nil, 0.0},
		{"mismatch", []float64{1}, []float64{1, 2}, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSimilarity(tt.a, tt.b)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("cosineSimilarity() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestChunkHash(t *testing.T) {
	c1 := Chunk{FilePath: "a.go", Content: "hello", StartLine: 1, EndLine: 5}
	c2 := Chunk{FilePath: "a.go", Content: "hello", StartLine: 1, EndLine: 5}
	c3 := Chunk{FilePath: "b.go", Content: "hello", StartLine: 1, EndLine: 5}

	if chunkHash(c1) != chunkHash(c2) {
		t.Error("identical chunks should have same hash")
	}
	if chunkHash(c1) == chunkHash(c3) {
		t.Error("different chunks should have different hash")
	}
}
