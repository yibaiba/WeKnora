package chunker

import "fmt"

type SemanticParentChildRequest struct {
	Content      string
	ParentConfig SplitterConfig
	ChildConfig  SplitterConfig
	Document     SemanticDocument
}

func SplitParentChildSemanticDocument(request SemanticParentChildRequest) (ParentChildResult, error) {
	parents, err := SplitSemanticDocument(request.Content, request.ParentConfig, request.Document)
	if err != nil {
		return ParentChildResult{}, err
	}
	var storedParents []Chunk
	var children []ChildChunk
	childSequence := 0
	for _, parent := range parents {
		subDocument, sliceErr := sliceSemanticDocument(request.Document, parent.Start, parent.End)
		if sliceErr != nil {
			return ParentChildResult{}, sliceErr
		}
		subs, splitErr := SplitSemanticDocument(parent.Content, request.ChildConfig, subDocument)
		if splitErr != nil {
			return ParentChildResult{}, splitErr
		}
		parentIndex := semanticStoredParentIndex(parent, subs, &storedParents)
		for _, sub := range subs {
			sub.Seq = childSequence
			sub.Start += parent.Start
			sub.End += parent.Start
			sub, splitErr = mergeSemanticChildContext(parent, sub, request.ChildConfig)
			if splitErr != nil {
				return ParentChildResult{}, splitErr
			}
			children = append(children, ChildChunk{Chunk: sub, ParentIndex: parentIndex})
			childSequence++
		}
	}
	return ParentChildResult{Parents: storedParents, Children: children}, nil
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
	embeddingCount, err := counter.Count(child.EmbeddingContent())
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
		embeddingCount, err = counter.Count(child.EmbeddingContent())
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
