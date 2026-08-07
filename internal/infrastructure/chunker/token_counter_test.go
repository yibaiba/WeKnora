package chunker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExactTokenCounterCoversRepresentativeEmbeddingContent(t *testing.T) {
	counter, err := NewTokenCounter(TokenCounterConfig{Encoding: TokenizerEncodingO200KBase})
	require.NoError(t, err)

	samples := []string{
		"English and 中文、日本語、한국어",
		"func main() { println(\"hello\") }",
		"https://example.com/a?q=one%20two",
		`{"emoji":"🧪","ok":true}`,
		"<|endoftext|>",
	}
	for _, sample := range samples {
		result, countErr := counter.Count(sample)
		require.NoError(t, countErr)
		require.Positive(t, result.Count)
		require.Equal(t, TokenCountModeExact, result.Mode)
		require.Equal(t, TokenizerEncodingO200KBase, result.TokenizerID)
	}
}

func TestAutoTokenCounterRecognizesOpenAIEmbeddingModels(t *testing.T) {
	for _, model := range []string{"text-embedding-ada-002", "text-embedding-3-small", "TEXT-EMBEDDING-3-LARGE"} {
		counter, err := NewTokenCounter(TokenCounterConfig{Encoding: TokenizerEncodingAuto, Model: model})
		require.NoError(t, err)
		result, countErr := counter.Count("hello 世界")
		require.NoError(t, countErr)
		require.Equal(t, TokenCountModeExact, result.Mode)
		require.Equal(t, TokenizerEncodingCL100KBase, result.TokenizerID)
	}
}

func TestUnknownModelUsesConservativeByteUpperBound(t *testing.T) {
	counter, err := NewTokenCounter(TokenCounterConfig{Model: "custom-embedding-v1"})
	require.NoError(t, err)

	text := "hello🧪"
	result, countErr := counter.Count(text)
	require.NoError(t, countErr)
	require.Equal(t, len([]byte(text))+ConservativeEmbeddingTokenReserve, result.Count)
	require.Equal(t, TokenCountModeConservative, result.Mode)
	require.Equal(t, TokenizerEncodingByteUpperBound, result.TokenizerID)

	empty, countErr := counter.Count("")
	require.NoError(t, countErr)
	require.Zero(t, empty.Count)
}

func TestTokenCounterRejectsUnsupportedExplicitEncoding(t *testing.T) {
	_, err := NewTokenCounter(TokenCounterConfig{Encoding: "p50k_base"})
	require.ErrorContains(t, err, "unsupported tokenizer encoding")
}
