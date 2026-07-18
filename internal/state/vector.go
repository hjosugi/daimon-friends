package state

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

const DefaultEmbeddingDimensions = 256

// FeatureHashEmbedding is a compact local fallback. Production may replace it
// with a semantic embedding provider while keeping the same storage format.
func FeatureHashEmbedding(text string, dimensions int) []float32 {
	if dimensions <= 0 {
		dimensions = DefaultEmbeddingDimensions
	}
	vector := make([]float32, dimensions)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	for index, word := range words {
		addHashedFeature(vector, word, 1)
		if index > 0 {
			addHashedFeature(vector, words[index-1]+"\x00"+word, 0.6)
		}
	}
	normalize(vector)
	return vector
}

func addHashedFeature(vector []float32, feature string, weight float32) {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(feature))
	sum := hasher.Sum64()
	index := int(sum % uint64(len(vector)))
	if sum&(1<<63) != 0 {
		weight = -weight
	}
	vector[index] += weight
}

func normalize(vector []float32) {
	var sum float64
	for _, value := range vector {
		sum += float64(value * value)
	}
	if sum == 0 {
		return
	}
	norm := float32(math.Sqrt(sum))
	for index := range vector {
		vector[index] /= norm
	}
}

func quantizeEmbedding(vector []float32) ([]byte, float64) {
	if len(vector) == 0 {
		return nil, 0
	}
	var maximum float64
	for _, value := range vector {
		absolute := math.Abs(float64(value))
		if absolute > maximum {
			maximum = absolute
		}
	}
	if maximum == 0 {
		return make([]byte, len(vector)), 1
	}
	scale := maximum / 127
	quantized := make([]byte, len(vector))
	for index, value := range vector {
		rounded := math.Round(float64(value) / scale)
		rounded = math.Max(-127, math.Min(127, rounded))
		quantized[index] = byte(int8(rounded))
	}
	return quantized, scale
}

func dequantizeEmbedding(quantized []byte, scale float64) []float32 {
	vector := make([]float32, len(quantized))
	for index, value := range quantized {
		vector[index] = float32(float64(int8(value)) * scale)
	}
	return vector
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullableScale(value []byte, scale float64) any {
	if len(value) == 0 {
		return nil
	}
	return scale
}

func cosineSimilarity(left, right []float32) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return 0
	}
	var dot, leftNorm, rightNorm float64
	for index := range left {
		l := float64(left[index])
		r := float64(right[index])
		dot += l * r
		leftNorm += l * l
		rightNorm += r * r
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	return dot / math.Sqrt(leftNorm*rightNorm)
}
