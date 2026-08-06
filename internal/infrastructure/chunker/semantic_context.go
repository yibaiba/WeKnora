package chunker

import "strings"

type semanticContextIndex struct {
	content      []rune
	byID         map[string]SemanticBlock
	tableHeaders map[string]SemanticBlock
	records      map[string]SemanticBlock
}

func newSemanticContextIndex(content string, document SemanticDocument) semanticContextIndex {
	index := semanticContextIndex{
		content: []rune(content), byID: make(map[string]SemanticBlock, len(document.Blocks)),
		tableHeaders: make(map[string]SemanticBlock), records: make(map[string]SemanticBlock),
	}
	for _, block := range document.Blocks {
		index.byID[block.ID] = block
		if block.Kind == SemanticKindTableHeader && block.TableID != "" {
			index.tableHeaders[block.TableID] = block
		}
		if block.Kind == SemanticKindRecord && block.RecordID != "" {
			index.records[block.RecordID] = block
		}
	}
	return index
}

func (index semanticContextIndex) headerFor(block SemanticBlock, continuation bool) string {
	parts := index.headingAncestors(block.ParentID)
	if block.Kind == SemanticKindTableRow {
		parts = appendSemanticContext(parts, index.blockText(index.tableHeaders[block.TableID]))
	}
	if continuation && block.Kind == SemanticKindRecord {
		parts = appendSemanticContext(parts, firstSemanticSourceLine(index.blockText(index.records[block.RecordID])))
	}
	if continuation && block.Kind == SemanticKindCodeBlock {
		parts = appendSemanticContext(parts, firstSemanticSourceLine(index.blockText(block)))
	}
	return strings.Join(parts, "\n")
}

func (index semanticContextIndex) headingAncestors(parentID string) []string {
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
		if parent.Kind == SemanticKindHeading && parent.Confidence == SemanticConfidenceHigh {
			reversed = append(reversed, index.blockText(parent))
		}
		parentID = parent.ParentID
	}
	result := make([]string, 0, len(reversed))
	for position := len(reversed) - 1; position >= 0; position-- {
		result = appendSemanticContext(result, reversed[position])
	}
	return result
}

func (index semanticContextIndex) blockText(block SemanticBlock) string {
	if block.End <= block.Start || block.Start < 0 || block.End > len(index.content) {
		return ""
	}
	return strings.TrimSpace(string(index.content[block.Start:block.End]))
}

func appendSemanticContext(parts []string, value string) []string {
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

func firstSemanticSourceLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return strings.TrimSpace(value[:index])
	}
	return strings.TrimSpace(value)
}
