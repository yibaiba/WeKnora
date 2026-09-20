package chunker

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

type SemanticParentChildRequest struct {
	Content      string
	ParentConfig SplitterConfig
	ChildConfig  SplitterConfig
	Document     SemanticDocument
}

func SplitParentChildSemanticDocument(request SemanticParentChildRequest) (ParentChildResult, error) {
	childConfig := request.ChildConfig
	if childConfig.SemanticPackingPolicy.Version == "" &&
		request.ParentConfig.SemanticPackingPolicy.Version != "" {
		childConfig.SemanticPackingPolicy = request.ParentConfig.SemanticPackingPolicy
	}
	children, err := SplitSemanticDocument(request.Content, childConfig, request.Document)
	if err != nil {
		return ParentChildResult{}, err
	}
	parentUnit := semanticParentUnit(request)
	if parentUnit == types.SemanticParentUnitNone {
		result := ParentChildResult{Children: make([]ChildChunk, len(children))}
		for index, child := range children {
			result.Children[index] = ChildChunk{Chunk: child, ParentIndex: -1}
		}
		return result, nil
	}
	return packSemanticParentTree(semanticParentTreeRequest{
		content: request.Content, children: children, document: request.Document,
		parentConfig: request.ParentConfig, childConfig: childConfig, parentUnit: parentUnit,
	})
}

func semanticParentUnit(request SemanticParentChildRequest) string {
	if unit := request.ChildConfig.SemanticPackingPolicy.ParentUnit; unit != "" {
		return unit
	}
	if unit := request.ParentConfig.SemanticPackingPolicy.ParentUnit; unit != "" {
		return unit
	}
	return types.SemanticParentUnitSection
}

type semanticParentTreeRequest struct {
	content      string
	children     []Chunk
	document     SemanticDocument
	parentConfig SplitterConfig
	childConfig  SplitterConfig
	parentUnit   string
}

func packSemanticParentTree(request semanticParentTreeRequest) (ParentChildResult, error) {
	index := newSemanticContextIndex(request.content, request.document, request.childConfig.SemanticPackingPolicy)
	packer := semanticPacker{runes: []rune(request.content), config: request.parentConfig, context: index}
	type parentGroup struct {
		key      string
		start    int
		end      int
		children []int
	}
	groups := make([]parentGroup, 0)
	current := parentGroup{start: -1}
	for childIndex, child := range request.children {
		key := semanticParentKey(index, request.document, child, request.parentUnit)
		if current.start >= 0 {
			sameKey := key == current.key
			fits, err := packer.fits(current.start, child.End, "")
			if err != nil {
				return ParentChildResult{}, err
			}
			if sameKey && fits {
				current.end = child.End
				current.children = append(current.children, childIndex)
				continue
			}
			groups = append(groups, current)
		}
		current = parentGroup{
			key: key, start: child.Start, end: child.End, children: []int{childIndex},
		}
	}
	if current.start >= 0 {
		groups = append(groups, current)
	}

	var parents []Chunk
	mapped := make([]ChildChunk, len(request.children))
	for _, group := range groups {
		subs := make([]Chunk, len(group.children))
		for index, childIndex := range group.children {
			subs[index] = request.children[childIndex]
		}
		parent := semanticParentChunk(packer.runes, group.start, group.end, subs)
		parentIndex := semanticStoredParentIndex(parent, subs, &parents)
		for _, childIndex := range group.children {
			child := request.children[childIndex]
			merged, err := mergeSemanticChildContext(parent, child, request.childConfig)
			if err != nil {
				return ParentChildResult{}, err
			}
			mapped[childIndex] = ChildChunk{Chunk: merged, ParentIndex: parentIndex}
		}
	}
	for index := range parents {
		parents[index].Seq = index
	}
	for index := range mapped {
		mapped[index].Seq = index
	}
	return ParentChildResult{Parents: parents, Children: mapped}, nil
}

func semanticParentChunk(runes []rune, start, end int, children []Chunk) Chunk {
	parent := Chunk{
		Content: string(runes[start:end]),
		Start:   start, End: end,
	}
	if len(children) == 0 {
		return parent
	}
	header := strings.TrimSpace(children[0].ContextHeader)
	if header == "" || strings.Contains(parent.Content, header) {
		return parent
	}
	parent.ContextHeader = children[0].ContextHeader
	parent.ContextReasonCodes = append([]string(nil), children[0].ContextReasonCodes...)
	return parent
}

func semanticParentKey(
	index semanticContextIndex,
	document SemanticDocument,
	child Chunk,
	parentUnit string,
) string {
	if parentUnit == types.SemanticParentUnitPage {
		if pageID := nearestSemanticPageID(document, child.Start); pageID != "" {
			return "page:" + pageID
		}
	}
	block := overlappingSemanticBlock(document, child.Start, child.End)
	root := index.headingRootID(block)
	if root == "" {
		return "root"
	}
	return "section:" + root
}

func nearestSemanticPageID(document SemanticDocument, start int) string {
	pageID := ""
	for _, block := range document.Blocks {
		if block.Kind != SemanticKindPageRegion || block.Start > start {
			continue
		}
		pageID = block.ID
	}
	return pageID
}

func overlappingSemanticBlock(document SemanticDocument, start, end int) SemanticBlock {
	for _, block := range document.Blocks {
		if block.End <= start || block.Start >= end {
			continue
		}
		if block.Kind != SemanticKindHeading && stringsTrimSpaceNonEmpty(block) {
			return block
		}
	}
	for _, block := range document.Blocks {
		if block.End > start && block.Start < end {
			return block
		}
	}
	return SemanticBlock{}
}

func stringsTrimSpaceNonEmpty(block SemanticBlock) bool {
	return block.End > block.Start
}

func mergeSemanticChildContext(
	parent Chunk,
	child Chunk,
	config SplitterConfig,
) (Chunk, error) {
	child.ContextReasonCodes = mergeSemanticReasons(
		parent.ContextReasonCodes, child.ContextReasonCodes,
	)
	if config.TokenLimit <= 0 {
		child.ContextHeader = mergeBreadcrumbs(parent.ContextHeader, child.ContextHeader)
		return child, nil
	}
	counter := tokenCounterOrConservative(config.TokenCounter)
	originalChildHeader := child.ContextHeader
	merged := mergeBreadcrumbs(parent.ContextHeader, originalChildHeader)
	contextCount, err := counter.Count(merged)
	if err != nil {
		return Chunk{}, err
	}
	contextBudget := min(semanticContextTokenCap, config.TokenLimit/5)
	if contextCount.Count > contextBudget && parent.ContextHeader != "" {
		merged = originalChildHeader
		child.ContextReasonCodes = appendUniqueReason(
			child.ContextReasonCodes, SemanticReasonAncestorOmitted,
		)
	}
	child.ContextHeader = merged
	embeddingCount, err := counter.Count(PrependEmbeddingPrefix(
		config.EmbeddingPrefix, child.EmbeddingContent(),
	))
	if err != nil {
		return Chunk{}, err
	}
	if embeddingCount.Count <= config.TokenLimit {
		return child, nil
	}
	if parent.ContextHeader != "" && child.ContextHeader != originalChildHeader {
		child.ContextHeader = originalChildHeader
		child.ContextReasonCodes = appendUniqueReason(
			child.ContextReasonCodes, SemanticReasonAncestorOmitted,
		)
		embeddingCount, err = counter.Count(PrependEmbeddingPrefix(
			config.EmbeddingPrefix, child.EmbeddingContent(),
		))
		if err != nil {
			return Chunk{}, err
		}
	}
	if embeddingCount.Count > config.TokenLimit {
		return Chunk{}, fmt.Errorf("semantic child embedding exceeds token budget")
	}
	return child, nil
}

func mergeSemanticReasons(left, right []string) []string {
	result := append([]string(nil), left...)
	for _, reason := range right {
		result = appendUniqueReason(result, reason)
	}
	return result
}

func semanticStoredParentIndex(parent Chunk, children []Chunk, parents *[]Chunk) int {
	if len(children) == 1 && children[0].Content == parent.Content {
		return -1
	}
	index := len(*parents)
	*parents = append(*parents, parent)
	return index
}

func sliceSemanticDocument(document SemanticDocument, start, end int) (SemanticDocument, error) {
	if start < 0 || end <= start || end > document.ContentLength {
		return SemanticDocument{}, fmt.Errorf("invalid semantic parent range")
	}
	blocks := make([]SemanticBlock, 0)
	for _, source := range document.Blocks {
		if source.End <= start || source.Start >= end {
			continue
		}
		block := source
		block.Start = max(source.Start, start) - start
		block.End = min(source.End, end) - start
		blocks = append(blocks, block)
	}
	clearExternalSemanticParents(blocks)
	downgradeSlicedTablesWithoutHeaders(blocks)
	result := SemanticDocument{ContentLength: end - start, Blocks: blocks}
	if err := ValidateSemanticDocument(result); err != nil {
		return SemanticDocument{}, err
	}
	return result, nil
}

func downgradeSlicedTablesWithoutHeaders(blocks []SemanticBlock) {
	headers := make(map[string]struct{})
	for _, block := range blocks {
		if block.Kind == SemanticKindTableHeader {
			headers[block.TableID] = struct{}{}
		}
	}
	for index := range blocks {
		if blocks[index].Kind != SemanticKindTableRow {
			continue
		}
		if _, ok := headers[blocks[index].TableID]; ok {
			continue
		}
		blocks[index].Kind = SemanticKindParagraph
		blocks[index].TableID = ""
	}
}

func clearExternalSemanticParents(blocks []SemanticBlock) {
	ids := make(map[string]struct{}, len(blocks))
	for _, block := range blocks {
		ids[block.ID] = struct{}{}
	}
	for index := range blocks {
		if _, ok := ids[blocks[index].ParentID]; !ok {
			blocks[index].ParentID = ""
		}
	}
}
