package chunker

func (scanner *semanticScanner) consumeASTBlock(index int) (int, []SemanticBlock, bool) {
	block, ok := scanner.astBlocks[index]
	if !ok || block.endLine <= index || block.endLine > len(scanner.lines) {
		return 0, nil, false
	}
	start := scanner.lines[index].start
	end := scanner.lines[block.endLine-1].end
	switch block.kind {
	case semanticASTHeading:
		return block.endLine, []SemanticBlock{newSemanticBlock(semanticBlockSpec{
			kind: SemanticKindHeading, start: start, end: end,
			confidence: SemanticConfidenceHigh, atomic: true, depth: block.depth,
		})}, true
	case semanticASTList:
		return block.endLine, []SemanticBlock{newSemanticBlock(semanticBlockSpec{
			kind: SemanticKindListItem, start: start, end: end,
			confidence: SemanticConfidenceHigh, atomic: true,
		})}, true
	case semanticASTCode:
		return block.endLine, []SemanticBlock{newSemanticBlock(semanticBlockSpec{
			kind: SemanticKindCodeBlock, start: start, end: end,
			confidence: SemanticConfidenceHigh, atomic: true,
		})}, true
	case semanticASTTable:
		return scanner.consumeASTTable(index, block.endLine)
	default:
		return 0, nil, false
	}
}

func (scanner *semanticScanner) consumeASTTable(start, end int) (int, []SemanticBlock, bool) {
	if end-start < 2 {
		return 0, nil, false
	}
	scanner.tableSeq++
	tableID := semanticTableID(scanner.tableSeq)
	header := newSemanticBlock(semanticBlockSpec{
		kind: SemanticKindTableHeader, start: scanner.lines[start].start, end: scanner.lines[start+1].end,
		confidence: SemanticConfidenceHigh, atomic: true,
	})
	header.TableID = tableID
	blocks := []SemanticBlock{header}
	for index := start + 2; index < end; index++ {
		row := newSemanticBlock(semanticBlockSpec{
			kind: SemanticKindTableRow, start: scanner.lines[index].start, end: scanner.lines[index].end,
			confidence: SemanticConfidenceHigh, atomic: true,
		})
		row.TableID = tableID
		blocks = append(blocks, row)
	}
	return end, blocks, true
}
