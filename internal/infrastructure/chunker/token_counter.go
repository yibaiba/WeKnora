package chunker

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/tiktoken-go/tokenizer"
)

const (
	TokenCountModeExact        = "exact"
	TokenCountModeConservative = "conservative"

	TokenizerEncodingAuto           = "auto"
	TokenizerEncodingCL100KBase     = "cl100k_base"
	TokenizerEncodingO200KBase      = "o200k_base"
	TokenizerEncodingByteUpperBound = "byte_upper_bound"

	ConservativeEmbeddingTokenReserve = 8
)

type TokenCount = types.TokenCount
type TokenCounter = types.TokenCounter

type TokenCounterConfig struct {
	Encoding string
	Model    string
}

type exactTokenCounter struct {
	codec tokenizer.Codec
}

type conservativeTokenCounter struct{}

func NewTokenCounter(config TokenCounterConfig) (TokenCounter, error) {
	encoding := strings.TrimSpace(strings.ToLower(config.Encoding))
	if encoding == "" || encoding == TokenizerEncodingAuto {
		encoding = knownEmbeddingEncoding(config.Model)
		if encoding == "" {
			return conservativeTokenCounter{}, nil
		}
	}
	if encoding == TokenizerEncodingByteUpperBound {
		return conservativeTokenCounter{}, nil
	}
	if encoding != TokenizerEncodingCL100KBase && encoding != TokenizerEncodingO200KBase {
		return nil, fmt.Errorf("unsupported tokenizer encoding %q", encoding)
	}
	codec, err := tokenizer.Get(tokenizer.Encoding(encoding))
	if err != nil {
		return nil, fmt.Errorf("unsupported tokenizer encoding %q: %w", encoding, err)
	}
	return exactTokenCounter{codec: codec}, nil
}

func (c exactTokenCounter) Count(text string) (TokenCount, error) {
	count, err := c.codec.Count(text)
	if err != nil {
		return TokenCount{}, fmt.Errorf("count tokens with %s: %w", c.codec.GetName(), err)
	}
	return TokenCount{Count: count, Mode: TokenCountModeExact, TokenizerID: c.codec.GetName()}, nil
}

func (conservativeTokenCounter) Count(text string) (TokenCount, error) {
	count := 0
	if text != "" {
		count = len([]byte(text)) + ConservativeEmbeddingTokenReserve
	}
	return TokenCount{
		Count: count, Mode: TokenCountModeConservative,
		TokenizerID: TokenizerEncodingByteUpperBound,
	}, nil
}

func knownEmbeddingEncoding(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(model, "text-embedding-3-") || model == "text-embedding-ada-002" {
		return TokenizerEncodingCL100KBase
	}
	return ""
}

func tokenCounterOrConservative(counter TokenCounter) TokenCounter {
	if counter != nil {
		return counter
	}
	return conservativeTokenCounter{}
}
