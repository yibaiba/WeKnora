package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelParametersTokenizerEncodingValidationAndRoundTrip(t *testing.T) {
	for _, encoding := range []string{
		"", TokenizerEncodingAuto, TokenizerEncodingCL100KBase,
		TokenizerEncodingO200KBase, TokenizerEncodingByteUpperBound,
	} {
		params := ModelParameters{EmbeddingParameters: EmbeddingParameters{TokenizerEncoding: encoding}}
		require.NoError(t, params.Validate())
		payload, err := json.Marshal(params)
		require.NoError(t, err)
		var restored ModelParameters
		require.NoError(t, json.Unmarshal(payload, &restored))
		require.Equal(t, encoding, restored.EmbeddingParameters.TokenizerEncoding)
	}

	invalid := ModelParameters{EmbeddingParameters: EmbeddingParameters{TokenizerEncoding: "p50k_base"}}
	require.ErrorContains(t, invalid.Validate(), "tokenizer_encoding")
}
