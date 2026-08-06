package chunker

import (
	"fmt"
	"strings"
	"unicode"
)

type semanticPackingUnit struct {
	SemanticBlock
	ContextHeader string
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
		context: newSemanticContextIndex(content, document),
	}
	units, err := packer.expand(document.Blocks)
	if err != nil {
		return nil, err
	}
	return packer.pack(units), nil
}

func (packer semanticPacker) expand(blocks []SemanticBlock) ([]semanticPackingUnit, error) {
	units := make([]semanticPackingUnit, 0, len(blocks))
	for _, block := range blocks {
		header := packer.context.headerFor(block, false)
		if packer.fits(block.Start, block.End, header) {
			units = append(units, semanticPackingUnit{SemanticBlock: block, ContextHeader: header})
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
		header := packer.context.headerFor(block, continuation)
		limit := packer.sourceBudget(header)
		if limit <= 0 {
			return nil, fmt.Errorf("semantic context exceeds chunk budget")
		}
		end := min(block.End, start+limit)
		if end < block.End {
			end = semanticSafeCut(packer.runes, semanticCutRequest{
				start: start, proposed: end, kind: block.Kind,
			})
		}
		for end > start && !packer.fits(start, end, header) {
			end--
		}
		if end <= start {
			return nil, fmt.Errorf("semantic block cannot fit chunk budget")
		}
		part := block
		part.Start, part.End = start, end
		result = append(result, semanticPackingUnit{SemanticBlock: part, ContextHeader: header})
		start = end
	}
	return result, nil
}

func (packer semanticPacker) sourceBudget(header string) int {
	limit := packer.config.ChunkSize
	if packer.config.TokenLimit <= 0 {
		return limit
	}
	lang := semanticConfigLanguage(packer.config)
	remaining := packer.config.TokenLimit - ApproxTokenCount(header, lang)
	if remaining <= 0 {
		return 0
	}
	tokenChars := CharsForTokenLimit(remaining, lang)
	if tokenChars > 0 && tokenChars < limit {
		return tokenChars
	}
	return limit
}

func (packer semanticPacker) fits(start, end int, header string) bool {
	if end <= start || end-start > packer.config.ChunkSize {
		return false
	}
	if packer.config.TokenLimit <= 0 {
		return true
	}
	body := string(packer.runes[start:end])
	if header != "" {
		body = header + "\n\n" + body
	}
	return ApproxTokenCount(body, semanticConfigLanguage(packer.config)) <= packer.config.TokenLimit
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
