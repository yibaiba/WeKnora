package chunker

import "strings"

func (scanner *semanticScanner) consumeMarkdownTable(index int) (int, bool) {
	if index+1 >= len(scanner.lines) || !tableRowPattern.MatchString(scanner.lines[index].trimmed) ||
		!isMarkdownTableSeparator(scanner.lines[index+1].trimmed) {
		return 0, false
	}
	scanner.tableSeq++
	tableID := semanticTableID(scanner.tableSeq)
	header := newSemanticBlock(semanticBlockSpec{
		kind: SemanticKindTableHeader, start: scanner.lines[index].start, end: scanner.lines[index+1].end,
		confidence: SemanticConfidenceHigh, atomic: true,
	})
	header.TableID = tableID
	scanner.blocks = append(scanner.blocks, header)
	index += 2
	for index < len(scanner.lines) && tableRowPattern.MatchString(scanner.lines[index].trimmed) {
		row := newSemanticBlock(semanticBlockSpec{
			kind: SemanticKindTableRow, start: scanner.lines[index].start, end: scanner.lines[index].end,
			confidence: SemanticConfidenceHigh, atomic: true,
		})
		row.TableID = tableID
		scanner.blocks = append(scanner.blocks, row)
		index++
	}
	return index, true
}

func isMarkdownTableSeparator(value string) bool {
	if !tableRowPattern.MatchString(value) {
		return false
	}
	cells := strings.Split(strings.Trim(value, " |\t"), "|")
	if len(cells) < 2 {
		return false
	}
	for _, cell := range cells {
		cell = strings.Trim(strings.TrimSpace(cell), ":")
		if len(cell) < 3 || strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return true
}

func (scanner *semanticScanner) consumeFAQ(index int) (int, SemanticBlock, bool) {
	if !isExplicitQuestion(scanner.lines[index].trimmed) || index+1 >= len(scanner.lines) ||
		!isExplicitAnswer(scanner.lines[index+1].trimmed) {
		return 0, SemanticBlock{}, false
	}
	start := scanner.lines[index].start
	end := scanner.lines[index+1].end
	index += 2
	for index < len(scanner.lines) && scanner.lines[index].trimmed != "" && !scanner.isStructuralStart(index) {
		end = scanner.lines[index].end
		index++
	}
	block := newSemanticBlock(semanticBlockSpec{
		kind: SemanticKindFAQ, start: start, end: end, confidence: SemanticConfidenceHigh, atomic: true,
	})
	block.ContextKinds = []string{"question", "answer"}
	return index, block, true
}

func isExplicitQuestion(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "q:") || strings.HasPrefix(lower, "q：") ||
		strings.HasPrefix(lower, "question:") || strings.HasPrefix(lower, "问：") ||
		strings.HasPrefix(lower, "问题：")
}

func isExplicitAnswer(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "a:") || strings.HasPrefix(lower, "a：") ||
		strings.HasPrefix(lower, "answer:") || strings.HasPrefix(lower, "答：") ||
		strings.HasPrefix(lower, "回答：")
}

func (scanner *semanticScanner) consumeImage(index int) (int, SemanticBlock, bool) {
	start := scanner.lines[index].start
	end := scanner.lines[index].end
	next := index + 1
	contexts := []string{"image"}
	if next < len(scanner.lines) && scanner.lines[next].trimmed != "" &&
		len([]rune(scanner.lines[next].trimmed)) <= 160 &&
		semanticImageCaption.MatchString(scanner.lines[next].trimmed) &&
		!scanner.isHardStructuralStart(next) {
		end = scanner.lines[next].end
		next++
		contexts = append(contexts, "caption")
	}
	block := newSemanticBlock(semanticBlockSpec{
		kind: SemanticKindImage, start: start, end: end, confidence: SemanticConfidenceHigh, atomic: true,
	})
	block.ContextKinds = contexts
	return next, block, true
}

func (scanner *semanticScanner) isHardStructuralStart(index int) bool {
	line := scanner.lines[index]
	if isFenceStart(line.trimmed) || semanticListPattern.MatchString(line.trimmed) ||
		tableRowPattern.MatchString(line.trimmed) || semanticKeyValue.MatchString(line.trimmed) {
		return true
	}
	if depth, confidence, ok := semanticHeading(line.trimmed, scanner.lines, index); ok {
		return depth > 0 && confidence == SemanticConfidenceHigh
	}
	return isExplicitQuestion(line.trimmed) || strings.Contains(strings.ToLower(line.trimmed), "<table")
}

func (scanner *semanticScanner) consumeListItem(index int) (int, SemanticBlock, bool) {
	start := scanner.lines[index].start
	end := scanner.lines[index].end
	index++
	for index < len(scanner.lines) && scanner.lines[index].trimmed != "" &&
		!semanticListPattern.MatchString(scanner.lines[index].trimmed) &&
		(strings.HasPrefix(scanner.lines[index].text, "  ") || strings.HasPrefix(scanner.lines[index].text, "\t")) {
		end = scanner.lines[index].end
		index++
	}
	return index, newSemanticBlock(semanticBlockSpec{
		kind: SemanticKindListItem, start: start, end: end, confidence: SemanticConfidenceHigh, atomic: true,
	}), true
}

func (scanner *semanticScanner) consumeRecord(index int) (int, SemanticBlock, bool) {
	if !semanticKeyValue.MatchString(scanner.lines[index].trimmed) {
		return 0, SemanticBlock{}, false
	}
	start := scanner.lines[index].start
	end := scanner.lines[index].end
	count := 1
	index++
	for index < len(scanner.lines) && semanticKeyValue.MatchString(scanner.lines[index].trimmed) {
		end = scanner.lines[index].end
		count++
		index++
	}
	if count < 2 {
		return 0, SemanticBlock{}, false
	}
	scanner.recordSeq++
	block := newSemanticBlock(semanticBlockSpec{
		kind: SemanticKindRecord, start: start, end: end, confidence: SemanticConfidenceSoft,
	})
	block.RecordID = semanticRecordID(scanner.recordSeq)
	block.ContextKinds = []string{"record"}
	return index, block, true
}
