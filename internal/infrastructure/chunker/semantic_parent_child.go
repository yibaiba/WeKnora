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
			sub.ContextHeader = mergeBreadcrumbs(parent.ContextHeader, sub.ContextHeader)
			children = append(children, ChildChunk{Chunk: sub, ParentIndex: parentIndex})
			childSequence++
		}
	}
	return ParentChildResult{Parents: storedParents, Children: children}, nil
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
	result := SemanticDocument{ContentLength: end - start, Blocks: blocks}
	if err := ValidateSemanticDocument(result); err != nil {
		return SemanticDocument{}, err
	}
	return result, nil
}
