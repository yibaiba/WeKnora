package chunker

import (
	"fmt"
	"strings"
	"unicode"
)

func (packer semanticPacker) splitOversize(block SemanticBlock) ([]semanticPackingUnit, error) {
	switch block.Kind {
	case SemanticKindTableHeader, SemanticKindTableRow:
		return packer.splitOversizeLines(block, semanticOversizeOptions{forbidRuneBackoff: true})
	case SemanticKindFAQ:
		return packer.splitOversizeFAQ(block)
	case SemanticKindCodeBlock:
		return packer.splitOversizeLines(block, semanticOversizeOptions{
			keepFirstLine: true, forbidRuneBackoff: true,
		})
	case SemanticKindRecord:
		return packer.splitOversizeLines(block, semanticOversizeOptions{keepFirstLine: true})
	default:
		return packer.splitOversizeRunesRange(block, block.Start, block.End)
	}
}

type semanticOversizeOptions struct {
	keepFirstLine     bool
	forbidRuneBackoff bool
}

func (packer semanticPacker) splitOversizeFAQ(block SemanticBlock) ([]semanticPackingUnit, error) {
	lines := semanticBlockLines(packer.runes[block.Start:block.End])
	if len(lines) == 0 {
		return packer.splitOversizeLines(block, semanticOversizeOptions{forbidRuneBackoff: true})
	}
	splitAt := 1
	for index := 1; index < len(lines); index++ {
		if isExplicitAnswer(semanticUnquote(strings.TrimSpace(string(lines[index].text)))) {
			splitAt = index
			break
		}
	}
	questionEnd := block.Start + lines[splitAt-1].end
	units, err := packer.appendOversizeRange(nil, block, block.Start, questionEnd, false)
	if err != nil {
		return nil, err
	}
	if questionEnd >= block.End {
		return units, nil
	}
	tail, err := packer.splitOversizeLineRange(
		block, questionEnd, block.End, semanticOversizeOptions{forbidRuneBackoff: true},
	)
	if err != nil {
		return nil, err
	}
	return append(units, tail...), nil
}

func (packer semanticPacker) splitOversizeLines(
	block SemanticBlock,
	options semanticOversizeOptions,
) ([]semanticPackingUnit, error) {
	return packer.splitOversizeLineRange(block, block.Start, block.End, options)
}

func (packer semanticPacker) splitOversizeLineRange(
	block SemanticBlock,
	rangeStart, rangeEnd int,
	options semanticOversizeOptions,
) ([]semanticPackingUnit, error) {
	if rangeEnd <= rangeStart {
		return nil, fmt.Errorf("oversize_atomic: semantic block cannot fit chunk budget")
	}
	lines := semanticBlockLines(packer.runes[rangeStart:rangeEnd])
	if len(lines) == 0 {
		return packer.splitOversizeRunesRange(block, rangeStart, rangeEnd)
	}
	units := make([]semanticPackingUnit, 0, len(lines))
	start := rangeStart
	protectedEnd := rangeStart
	if options.keepFirstLine {
		protectedEnd = rangeStart + lines[0].end
	}
	for _, line := range lines {
		lineStart := rangeStart + line.start
		lineEnd := rangeStart + line.end
		if lineEnd <= start {
			continue
		}
		fits, err := packer.rangeFits(start, lineEnd, block, start > block.Start)
		if err != nil {
			return nil, err
		}
		if fits {
			continue
		}
		if start < lineStart {
			var appendErr error
			units, appendErr = packer.appendOversizeRange(
				units, block, start, lineStart, start > block.Start,
			)
			if appendErr != nil {
				return nil, appendErr
			}
			start = lineStart
			fits, err = packer.rangeFits(start, lineEnd, block, start > block.Start)
			if err != nil {
				return nil, err
			}
			if fits {
				continue
			}
		}
		if options.forbidRuneBackoff || lineEnd <= protectedEnd {
			return nil, fmt.Errorf("oversize_atomic: semantic block cannot fit chunk budget")
		}
		parts, splitErr := packer.splitOversizeRunesRange(block, start, lineEnd)
		if splitErr != nil {
			return nil, splitErr
		}
		units = append(units, parts...)
		start = lineEnd
	}
	if start < rangeEnd {
		var err error
		units, err = packer.appendOversizeRange(units, block, start, rangeEnd, start > block.Start)
		if err != nil {
			return nil, err
		}
	}
	if len(units) == 0 {
		return nil, fmt.Errorf("oversize_atomic: semantic block cannot fit chunk budget")
	}
	return units, nil
}

func (packer semanticPacker) splitOversizeRunesRange(
	block SemanticBlock,
	rangeStart, rangeEnd int,
) ([]semanticPackingUnit, error) {
	result := make([]semanticPackingUnit, 0, (rangeEnd-rangeStart)/packer.config.ChunkSize+1)
	start := rangeStart
	for start < rangeEnd {
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
		end := min(rangeEnd, start+limit)
		if end < rangeEnd {
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
			if semanticForbidsRuneBackoff(block.Kind) {
				return nil, fmt.Errorf("oversize_atomic: semantic block cannot fit chunk budget")
			}
			return nil, fmt.Errorf("semantic block cannot fit chunk budget")
		}
		result = append(result, packer.oversizeUnit(block, start, end, context))
		start = end
	}
	return result, nil
}

func (packer semanticPacker) rangeFits(start, end int, block SemanticBlock, continuation bool) (bool, error) {
	context, err := packer.context.contextFor(block, semanticContextOptions{
		continuation: continuation, tokenLimit: packer.config.TokenLimit,
		counter: packer.config.TokenCounter, embeddingPrefix: packer.config.EmbeddingPrefix,
	})
	if err != nil {
		return false, err
	}
	return packer.fits(start, end, context.header)
}

func (packer semanticPacker) appendOversizeRange(
	units []semanticPackingUnit,
	block SemanticBlock,
	start, end int,
	continuation bool,
) ([]semanticPackingUnit, error) {
	if end <= start {
		return units, nil
	}
	fits, err := packer.rangeFits(start, end, block, continuation)
	if err != nil {
		return nil, err
	}
	if !fits {
		return nil, fmt.Errorf("oversize_atomic: semantic block cannot fit chunk budget")
	}
	context, err := packer.context.contextFor(block, semanticContextOptions{
		continuation: continuation, tokenLimit: packer.config.TokenLimit,
		counter: packer.config.TokenCounter, embeddingPrefix: packer.config.EmbeddingPrefix,
	})
	if err != nil {
		return nil, err
	}
	return append(units, packer.oversizeUnit(block, start, end, context)), nil
}

func (packer semanticPacker) oversizeUnit(
	block SemanticBlock,
	start, end int,
	context semanticContext,
) semanticPackingUnit {
	part := block
	part.Start, part.End = start, end
	return semanticPackingUnit{
		SemanticBlock: part, ContextHeader: context.header,
		ContextReasonCodes: context.reasonCodes,
	}
}

func semanticForbidsRuneBackoff(kind string) bool {
	switch kind {
	case SemanticKindTableHeader, SemanticKindTableRow, SemanticKindFAQ, SemanticKindCodeBlock:
		return true
	default:
		return false
	}
}

type semanticLineSpan struct {
	start int
	end   int
	text  []rune
}

func semanticBlockLines(content []rune) []semanticLineSpan {
	lines := make([]semanticLineSpan, 0)
	start := 0
	for index := 0; index <= len(content); index++ {
		if index < len(content) && content[index] != '\n' {
			continue
		}
		end := index
		if index < len(content) {
			end = index + 1
		}
		if end > start {
			lines = append(lines, semanticLineSpan{start: start, end: end, text: content[start:end]})
		}
		start = end
	}
	return lines
}

func semanticSafeCut(content []rune, request semanticCutRequest) int {
	switch request.kind {
	case SemanticKindTableHeader, SemanticKindTableRow, SemanticKindFAQ, SemanticKindCodeBlock:
		for position := request.proposed; position > request.start; position-- {
			if content[position-1] == '\n' {
				return position
			}
		}
		return request.start
	case SemanticKindRecord:
		for position := request.proposed; position > request.start; position-- {
			if content[position-1] == '\n' {
				return position
			}
		}
		// Notes/body may be a single oversize line; keep the identifier via
		// context and allow rune backoff on the remainder.
		return request.proposed
	}
	newline, punct, space := request.start, request.start, request.start
	for position := request.proposed; position > request.start; position-- {
		current := content[position-1]
		if current == '\n' {
			newline = position
			break
		}
		if semanticNarrativeKind(request.kind) && strings.ContainsRune("。！？.!?;；", current) && punct == request.start {
			punct = position
		}
		if semanticNarrativeKind(request.kind) && unicode.IsSpace(current) && space == request.start {
			space = position
		}
	}
	if newline > request.start {
		return newline
	}
	if punct > request.start {
		return punct
	}
	if space > request.start {
		return space
	}
	return request.proposed
}

func semanticNarrativeKind(kind string) bool {
	return kind == SemanticKindParagraph || kind == SemanticKindPreamble
}
