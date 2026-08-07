package chunker

import (
	"io"
	"strings"

	"golang.org/x/net/html"
)

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

type semanticHTMLRowSpan struct {
	start     int
	hasHeader bool
}

func semanticHTMLTableBlocks(request semanticHTMLTableRequest) []SemanticBlock {
	content := semanticLineRangeText(request.lines, request.start, request.end)
	rows := parseSemanticHTMLRows(content)
	if len(rows) == 0 || !rows[0].hasHeader {
		return []SemanticBlock{newSemanticBlock(semanticBlockSpec{
			kind: SemanticKindParagraph, start: request.start, end: request.end,
			confidence: SemanticConfidenceHigh, atomic: true,
		})}
	}
	byteToRune := buildByteToRuneIndex([]byte(content))
	result := make([]SemanticBlock, 0, len(rows))
	for index, row := range rows {
		start := request.start + byteToRune[row.start]
		if index == 0 {
			start = request.start
		}
		end := request.end
		if index+1 < len(rows) {
			end = request.start + byteToRune[rows[index+1].start]
		}
		kind := SemanticKindTableRow
		if index == 0 && row.hasHeader {
			kind = SemanticKindTableHeader
		}
		result = append(result, semanticHTMLTableBlock(kind, start, end, request.tableID))
	}
	return result
}

func parseSemanticHTMLRows(content string) []semanticHTMLRowSpan {
	tokenizer := html.NewTokenizer(strings.NewReader(content))
	rows := make([]semanticHTMLRowSpan, 0)
	offset := 0
	current := semanticHTMLRowSpan{start: -1}
	for {
		tokenType := tokenizer.Next()
		raw := tokenizer.Raw()
		tokenStart := offset
		offset += len(raw)
		if tokenType == html.ErrorToken {
			if tokenizer.Err() != nil && tokenizer.Err() != io.EOF {
				return nil
			}
			return rows
		}
		tokenName, _ := tokenizer.TagName()
		name := string(tokenName)
		if tokenType == html.StartTagToken && name == "tr" {
			current = semanticHTMLRowSpan{start: tokenStart}
			continue
		}
		if tokenType == html.StartTagToken && name == "th" && current.start >= 0 {
			current.hasHeader = true
			continue
		}
		if tokenType == html.EndTagToken && name == "tr" && current.start >= 0 {
			rows = append(rows, current)
			current = semanticHTMLRowSpan{start: -1}
		}
	}
}

func semanticHTMLTableBlock(kind string, start, end int, tableID string) SemanticBlock {
	block := newSemanticBlock(semanticBlockSpec{
		kind: kind, start: start, end: end,
		confidence: SemanticConfidenceHigh, atomic: true,
	})
	block.TableID = tableID
	return block
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
