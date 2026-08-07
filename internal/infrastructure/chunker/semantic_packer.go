package chunker

import (
	"fmt"
	"strings"
	"unicode"
)

type semanticPackingUnit struct {
	SemanticBlock
	ContextHeader      string
	ContextReasonCodes []string
}

type semanticPacker struct {
	runes   []rune
	config  SplitterConfig
	context semanticContextIndex
}

// SplitSemanticDocument packs a validated structure tree without changing
// source content or positions.
func SplitSemanticDocument(
	content string,
	config SplitterConfig,
	document SemanticDocument,
) ([]Chunk, error) {
	config = NormalizeConfig(config)
	if document.ContentLength != len([]rune(content)) {
		return nil, fmt.Errorf("semantic document length does not match content")
	}
	if err := ValidateSemanticDocument(document); err != nil {
		return nil, err
	}
	packer := semanticPacker{
		runes: []rune(content), config: config,
		context: newSemanticContextIndex(content, document, config.SemanticPackingPolicy),
	}
	units, err := packer.expand(document.Blocks)
	if err != nil {
		return nil, err
	}
	return packer.pack(units)
}

func (packer semanticPacker) expand(blocks []SemanticBlock) ([]semanticPackingUnit, error) {
	units := make([]semanticPackingUnit, 0, len(blocks))
	for _, block := range blocks {
		context, contextErr := packer.context.contextFor(
			block, semanticContextOptions{
				tokenLimit: packer.config.TokenLimit, counter: packer.config.TokenCounter,
				embeddingPrefix: packer.config.EmbeddingPrefix,
			},
		)
		if contextErr != nil {
			return nil, contextErr
		}
		fits, err := packer.fits(block.Start, block.End, context.header)
		if err != nil {
			return nil, err
		}
		if fits {
			units = append(units, semanticPackingUnit{
				SemanticBlock: block, ContextHeader: context.header,
				ContextReasonCodes: context.reasonCodes,
			})
			continue
		}
		parts, err := packer.splitOversize(block)
		if err != nil {
			return nil, err
		}
		units = append(units, parts...)
	}
	return units, nil
}

func (packer semanticPacker) splitOversize(block SemanticBlock) ([]semanticPackingUnit, error) {
	result := make([]semanticPackingUnit, 0, (block.End-block.Start)/packer.config.ChunkSize+1)
	start := block.Start
	for start < block.End {
		continuation := start > block.Start
		context, contextErr := packer.context.contextFor(
			block, semanticContextOptions{
				continuation: continuation, tokenLimit: packer.config.TokenLimit,
				counter: packer.config.TokenCounter, embeddingPrefix: packer.config.EmbeddingPrefix,
			},
		)
		if contextErr != nil {
			return nil, contextErr
		}
		limit, err := packer.sourceBudget(context.header)
		if err != nil {
			return nil, err
		}
		if limit <= 0 {
			return nil, fmt.Errorf("semantic context exceeds chunk budget")
		}
		end := min(block.End, start+limit)
		if end < block.End {
			end = semanticSafeCut(packer.runes, semanticCutRequest{
				start: start, proposed: end, kind: block.Kind,
			})
		}
		for end > start {
			fits, fitErr := packer.fits(start, end, context.header)
			if fitErr != nil {
				return nil, fitErr
			}
			if fits {
				break
			}
			end--
		}
		if end <= start {
			return nil, fmt.Errorf("semantic block cannot fit chunk budget")
		}
		part := block
		part.Start, part.End = start, end
		result = append(result, semanticPackingUnit{
			SemanticBlock: part, ContextHeader: context.header,
			ContextReasonCodes: context.reasonCodes,
		})
		start = end
	}
	return result, nil
}

func (packer semanticPacker) sourceBudget(header string) (int, error) {
	limit := packer.config.ChunkSize
	if packer.config.TokenLimit <= 0 {
		return limit, nil
	}
	lang := semanticConfigLanguage(packer.config)
	overhead := embeddingContentOverhead(packer.config.EmbeddingPrefix, header)
	headerCount, err := tokenCounterOrConservative(packer.config.TokenCounter).Count(overhead)
	if err != nil {
		return 0, err
	}
	remaining := packer.config.TokenLimit - headerCount.Count
	if remaining <= 0 {
		if strings.TrimSpace(packer.config.EmbeddingPrefix) != "" && strings.TrimSpace(header) == "" {
			return 0, fmt.Errorf("embedding prefix exceeds chunk budget")
		}
		return 0, fmt.Errorf("embedding prefix and context exceed chunk budget")
	}
	tokenChars := CharsForTokenLimit(remaining, lang)
	if tokenChars > 0 && tokenChars < limit {
		return tokenChars, nil
	}
	return limit, nil
}

func (packer semanticPacker) fits(start, end int, header string) (bool, error) {
	if end <= start || end-start > packer.config.ChunkSize {
		return false, nil
	}
	if packer.config.TokenLimit <= 0 {
		return true, nil
	}
	body := string(packer.runes[start:end])
	if header != "" {
		body = header + "\n\n" + body
	}
	body = PrependEmbeddingPrefix(packer.config.EmbeddingPrefix, body)
	count, err := tokenCounterOrConservative(packer.config.TokenCounter).Count(body)
	if err != nil {
		return false, err
	}
	return count.Count <= packer.config.TokenLimit, nil
}

func semanticConfigLanguage(config SplitterConfig) string {
	if len(config.Languages) > 0 {
		return config.Languages[0]
	}
	return LangMixed
}

type semanticCutRequest struct {
	start    int
	proposed int
	kind     string
}

func semanticSafeCut(content []rune, request semanticCutRequest) int {
	for position := request.proposed; position > request.start; position-- {
		current := content[position-1]
		if current == '\n' {
			return position
		}
		if request.kind == SemanticKindTableRow && current == '|' {
			return position
		}
		if semanticNarrativeKind(request.kind) && (strings.ContainsRune("。！？.!?;；", current) || unicode.IsSpace(current)) {
			return position
		}
	}
	return request.proposed
}

func semanticNarrativeKind(kind string) bool {
	return kind == SemanticKindParagraph || kind == SemanticKindPreamble || kind == SemanticKindFAQ
}
