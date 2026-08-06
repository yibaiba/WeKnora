package chunker

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var semanticHTMLRow = regexp.MustCompile(`(?is)<tr\b[^>]*>.*?</tr>`)

func (scanner *semanticScanner) consumeHTMLTable(index int) (int, []SemanticBlock, bool) {
	if !strings.Contains(strings.ToLower(scanner.lines[index].text), "<table") {
		return 0, nil, false
	}
	start := scanner.lines[index].start
	end := scanner.lines[index].end
	foundEnd := strings.Contains(strings.ToLower(scanner.lines[index].text), "</table>")
	for index++; index < len(scanner.lines) && !foundEnd; index++ {
		end = scanner.lines[index].end
		foundEnd = strings.Contains(strings.ToLower(scanner.lines[index].text), "</table>")
	}
	if !foundEnd {
		return 0, nil, false
	}
	scanner.tableSeq++
	return index, semanticHTMLTableBlocks(scanner.lines, start, end, semanticTableID(scanner.tableSeq)), true
}

func semanticHTMLTableBlocks(lines []semanticLine, start, end int, tableID string) []SemanticBlock {
	content := semanticLineRangeText(lines, start, end)
	rows := semanticHTMLRow.FindAllStringIndex(content, -1)
	if len(rows) == 0 {
		block := newSemanticBlock(SemanticKindTableRow, start, end, SemanticConfidenceHigh, true, 0)
		block.TableID = tableID
		return []SemanticBlock{block}
	}
	starts := make([]int, len(rows))
	for index, row := range rows {
		starts[index] = start + utf8.RuneCountInString(content[:row[0]])
	}
	result := make([]SemanticBlock, 0, len(rows))
	for index, row := range rows {
		blockStart := starts[index]
		if index == 0 {
			blockStart = start
		}
		blockEnd := end
		if index+1 < len(rows) {
			blockEnd = starts[index+1]
		}
		kind := SemanticKindTableRow
		if strings.Contains(strings.ToLower(content[row[0]:row[1]]), "<th") {
			kind = SemanticKindTableHeader
		}
		block := newSemanticBlock(kind, blockStart, blockEnd, SemanticConfidenceHigh, true, 0)
		block.TableID = tableID
		result = append(result, block)
	}
	return result
}

func semanticLineRangeText(lines []semanticLine, start, end int) string {
	var builder strings.Builder
	for _, line := range lines {
		if line.end <= start || line.start >= end {
			continue
		}
		builder.WriteString(line.text)
	}
	return builder.String()
}
