package service

import (
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

type ingestionSemanticIndex struct {
	content      []rune
	byID         map[string]chunker.SemanticBlock
	tableHeaders map[string]chunker.SemanticBlock
}

func newIngestionSemanticIndex(
	content string,
	document chunker.SemanticDocument,
) ingestionSemanticIndex {
	index := ingestionSemanticIndex{
		content: []rune(content), byID: make(map[string]chunker.SemanticBlock, len(document.Blocks)),
		tableHeaders: make(map[string]chunker.SemanticBlock),
	}
	for _, block := range document.Blocks {
		index.byID[block.ID] = block
		if block.Kind == chunker.SemanticKindTableHeader && block.TableID != "" {
			index.tableHeaders[block.TableID] = block
		}
	}
	return index
}

func (index ingestionSemanticIndex) blockText(block chunker.SemanticBlock) string {
	if block.Start < 0 || block.End <= block.Start || block.End > len(index.content) {
		return ""
	}
	return strings.TrimSpace(string(index.content[block.Start:block.End]))
}

func (index ingestionSemanticIndex) sectionDepth(block chunker.SemanticBlock) int {
	depth := block.SectionDepth
	parentID := block.ParentID
	seen := make(map[string]struct{})
	for parentID != "" {
		if _, duplicate := seen[parentID]; duplicate {
			break
		}
		seen[parentID] = struct{}{}
		parent, ok := index.byID[parentID]
		if !ok {
			break
		}
		depth = max(depth, parent.SectionDepth)
		parentID = parent.ParentID
	}
	return depth
}

func (index ingestionSemanticIndex) headingAncestors(parentID string) []string {
	var reversed []string
	seen := make(map[string]struct{})
	for parentID != "" {
		if _, duplicate := seen[parentID]; duplicate {
			break
		}
		seen[parentID] = struct{}{}
		parent, ok := index.byID[parentID]
		if !ok {
			break
		}
		if parent.Kind == chunker.SemanticKindHeading &&
			parent.Confidence == chunker.SemanticConfidenceHigh {
			reversed = append(reversed, index.blockText(parent))
		}
		parentID = parent.ParentID
	}
	result := make([]string, 0, len(reversed))
	for position := len(reversed) - 1; position >= 0; position-- {
		result = appendUniqueContext(result, reversed[position])
	}
	return result
}

func appendUniqueContext(parts []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return parts
	}
	for _, existing := range parts {
		if existing == value {
			return parts
		}
	}
	return append(parts, value)
}

func firstIngestionContextLine(value string) string {
	if position := strings.IndexByte(value, '\n'); position >= 0 {
		return strings.TrimSpace(value[:position])
	}
	return strings.TrimSpace(value)
}

func (validator *ingestionStructureValidator) validateTableContinuations() {
	seen := make(map[string]struct{})
	for _, block := range validator.request.document.Blocks {
		if block.Kind != chunker.SemanticKindTableRow {
			continue
		}
		header, ok := validator.index.tableHeaders[block.TableID]
		if !ok {
			validator.result.quality.OrphanTableRows++
			if block.Confidence == chunker.SemanticConfidenceHigh {
				validator.violations.add(ingestionViolationTableHeaderInvalid)
			}
			continue
		}
		validateTableRowChunks(tableRowValidationRequest{
			block: block, header: header, chunks: validator.request.chunks,
			index: validator.index, result: &validator.result,
			violations: validator.violations, seen: seen,
		})
	}
}

type tableRowValidationRequest struct {
	block      chunker.SemanticBlock
	header     chunker.SemanticBlock
	chunks     []chunker.Chunk
	index      ingestionSemanticIndex
	result     *ingestionCandidateValidationResult
	violations ingestionViolationSet
	seen       map[string]struct{}
}

func validateTableRowChunks(request tableRowValidationRequest) {
	headerText := request.index.blockText(request.header)
	for chunkIndex, current := range request.chunks {
		if !rangesOverlap(chunkRange(current), blockRange(request.block)) ||
			(current.Start <= request.header.Start && current.End >= request.header.End) {
			continue
		}
		key := request.block.TableID + ":" + strconv.Itoa(chunkIndex)
		if _, duplicate := request.seen[key]; duplicate {
			continue
		}
		request.seen[key] = struct{}{}
		if headerText != "" && strings.Contains(strings.TrimSpace(current.ContextHeader), headerText) {
			continue
		}
		request.result.quality.HeaderlessContinuations++
		if headerText == "" {
			request.violations.add(ingestionViolationTableHeaderInvalid)
			continue
		}
		if strings.TrimSpace(current.ContextHeader) == "" {
			request.violations.add(ingestionViolationTableHeaderMissing)
			continue
		}
		request.violations.add(ingestionViolationTableHeaderInvalid)
	}
}

func (validator *ingestionStructureValidator) validateChunkContexts() {
	for _, current := range validator.request.chunks {
		if strings.TrimSpace(current.ContextHeader) == "" {
			continue
		}
		allowed := allowedContextLines(current, validator.request.document, validator.index)
		if contextLinesAllowed(current.ContextHeader, allowed) {
			continue
		}
		validator.result.contextValid = false
		validator.violations.add(ingestionViolationContextSource)
	}
}

func allowedContextLines(
	current chunker.Chunk,
	document chunker.SemanticDocument,
	index ingestionSemanticIndex,
) map[string]struct{} {
	allowed := make(map[string]struct{})
	for _, block := range document.Blocks {
		if !rangesOverlap(chunkRange(current), blockRange(block)) {
			continue
		}
		addContextLines(allowed, strings.Join(index.headingAncestors(block.ParentID), "\n"))
		if block.Kind == chunker.SemanticKindTableRow {
			addContextLines(allowed, index.blockText(index.tableHeaders[block.TableID]))
		}
		if block.Kind == chunker.SemanticKindRecord || block.Kind == chunker.SemanticKindCodeBlock {
			addContextLines(allowed, firstIngestionContextLine(index.blockText(block)))
		}
	}
	return allowed
}

func addContextLines(target map[string]struct{}, value string) {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			target[line] = struct{}{}
		}
	}
}

func contextLinesAllowed(value string, allowed map[string]struct{}) bool {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, ok := allowed[line]; !ok {
			return false
		}
	}
	return true
}

type ingestionSourceRange struct {
	start int
	end   int
}

func chunkRange(value chunker.Chunk) ingestionSourceRange {
	return ingestionSourceRange{start: value.Start, end: value.End}
}

func blockRange(value chunker.SemanticBlock) ingestionSourceRange {
	return ingestionSourceRange{start: value.Start, end: value.End}
}

func rangesOverlap(left, right ingestionSourceRange) bool {
	return left.start < right.end && right.start < left.end
}

func describeIngestionChunks(
	request ingestionCandidateValidationRequest,
) []types.IngestionChunkStructureDescription {
	index := newIngestionSemanticIndex(request.content, request.document)
	result := make([]types.IngestionChunkStructureDescription, len(request.chunks))
	for chunkIndex, current := range request.chunks {
		result[chunkIndex] = describeIngestionChunk(ingestionChunkDescriptionRequest{
			current: current, validation: request, index: index, chunkIndex: chunkIndex,
		})
	}
	return result
}

type ingestionChunkDescriptionRequest struct {
	current    chunker.Chunk
	validation ingestionCandidateValidationRequest
	index      ingestionSemanticIndex
	chunkIndex int
}

func describeIngestionChunk(
	request ingestionChunkDescriptionRequest,
) types.IngestionChunkStructureDescription {
	kinds := make(map[string]struct{})
	description := types.IngestionChunkStructureDescription{
		Index: request.chunkIndex, HasContext: strings.TrimSpace(request.current.ContextHeader) != "",
	}
	for _, block := range request.validation.document.Blocks {
		if !rangesOverlap(chunkRange(request.current), blockRange(block)) {
			continue
		}
		kinds[block.Kind] = struct{}{}
		description.SectionDepth = max(description.SectionDepth, request.index.sectionDepth(block))
		if block.Kind == chunker.SemanticKindTableRow {
			header := request.index.tableHeaders[block.TableID]
			description.TableContinuation = request.current.Start > header.Start
		}
	}
	for kind := range kinds {
		description.Kinds = append(description.Kinds, kind)
	}
	sort.Strings(description.Kinds)
	if request.validation.config.EnableParentChild &&
		request.chunkIndex < len(request.validation.parentIndexes) {
		parentIndex := request.validation.parentIndexes[request.chunkIndex]
		description.ParentMapped = parentIndex == -1 ||
			(parentIndex >= 0 && parentIndex < len(request.validation.parents))
	}
	return description
}
