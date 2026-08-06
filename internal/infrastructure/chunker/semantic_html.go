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
	return index, semanticHTMLTableBlocks(semanticHTMLTableRequest{
		lines: scanner.lines, start: start, end: end, tableID: semanticTableID(scanner.tableSeq),
	}), true
}

type semanticHTMLTableRequest struct {
	lines   []semanticLine
	start   int
	end     int
	tableID string
}

func semanticHTMLTableBlocks(request semanticHTMLTableRequest) []SemanticBlock {
	content := semanticLineRangeText(request.lines, request.start, request.end)
	rows := semanticHTMLRow.FindAllStringIndex(content, -1)
	if len(rows) == 0 {
		block := newSemanticBlock(semanticBlockSpec{
			kind: SemanticKindTableRow, start: request.start, end: request.end,
			confidence: SemanticConfidenceHigh, atomic: true,
		})
		block.TableID = request.tableID
		return []SemanticBlock{block}
	}
	starts := make([]int, len(rows))
	for index, row := range rows {
		starts[index] = request.start + utf8.RuneCountInString(content[:row[0]])
	}
	result := make([]SemanticBlock, 0, len(rows))
	for index, row := range rows {
		blockStart := starts[index]
		if index == 0 {
			blockStart = request.start
		}
		blockEnd := request.end
		if index+1 < len(rows) {
			blockEnd = starts[index+1]
		}
		kind := SemanticKindTableRow
		if strings.Contains(strings.ToLower(content[row[0]:row[1]]), "<th") {
			kind = SemanticKindTableHeader
		}
		block := newSemanticBlock(semanticBlockSpec{
			kind: kind, start: blockStart, end: blockEnd,
			confidence: SemanticConfidenceHigh, atomic: true,
		})
		block.TableID = request.tableID
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
