package chunker

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	semanticListPattern = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+\S`)
	semanticKeyValue    = regexp.MustCompile(`^\s*[^:：|]{1,80}[:：]\s*\S.*$`)
	semanticHTMLHeading = regexp.MustCompile(`(?i)^\s*<h([1-6])\b[^>]*>.*</h[1-6]>\s*$`)
)

type semanticLine struct {
	start   int
	end     int
	text    string
	trimmed string
}

type semanticScanner struct {
	lines     []semanticLine
	blocks    []SemanticBlock
	tableSeq  int
	recordSeq int
}

func analyzeLocalSemanticBlocks(content string) []SemanticBlock {
	runes := []rune(content)
	if len(runes) == 0 {
		return nil
	}
	scanner := semanticScanner{lines: buildSemanticLines(runes)}
	scanner.scan()
	markSemanticPreamble(scanner.blocks)
	return scanner.blocks
}

func buildSemanticLines(runes []rune) []semanticLine {
	lines := make([]semanticLine, 0, strings.Count(string(runes), "\n")+1)
	start := 0
	for index, current := range runes {
		if current != '\n' {
			continue
		}
		text := string(runes[start : index+1])
		lines = append(lines, semanticLine{start: start, end: index + 1, text: text, trimmed: strings.TrimSpace(text)})
		start = index + 1
	}
	if start < len(runes) {
		text := string(runes[start:])
		lines = append(lines, semanticLine{start: start, end: len(runes), text: text, trimmed: strings.TrimSpace(text)})
	}
	return lines
}

func (scanner *semanticScanner) scan() {
	for index := 0; index < len(scanner.lines); {
		if scanner.lines[index].trimmed == "" {
			index = scanner.addBlank(index)
			continue
		}
		if next, blocks, ok := scanner.consumeHTMLTable(index); ok {
			scanner.blocks = append(scanner.blocks, blocks...)
			index = next
			continue
		}
		if next, ok := scanner.consumeMarkdownTable(index); ok {
			index = next
			continue
		}
		if next, block, ok := scanner.consumeSpecial(index); ok {
			scanner.blocks = append(scanner.blocks, block)
			index = next
			continue
		}
		index = scanner.addParagraph(index)
	}
}

func (scanner *semanticScanner) consumeSpecial(index int) (int, SemanticBlock, bool) {
	line := scanner.lines[index]
	if isFenceStart(line.trimmed) {
		return scanner.consumeFence(index)
	}
	if strings.Contains(line.text, "\f") || PageFooterPattern.MatchString(line.trimmed) {
		return index + 1, newSemanticBlock(SemanticKindPageRegion, line.start, line.end, SemanticConfidenceSoft, false, 0), true
	}
	if next, block, ok := scanner.consumeFAQ(index); ok {
		return next, block, true
	}
	if imageRefPattern.MatchString(line.trimmed) {
		return scanner.consumeImage(index)
	}
	if semanticListPattern.MatchString(line.trimmed) {
		return scanner.consumeListItem(index)
	}
	if next, block, ok := scanner.consumeRecord(index); ok {
		return next, block, true
	}
	if depth, confidence, ok := semanticHeading(line.trimmed, scanner.lines, index); ok {
		return index + 1, newSemanticBlock(SemanticKindHeading, line.start, line.end, confidence, true, depth), true
	}
	if tableRowPattern.MatchString(line.trimmed) {
		scanner.tableSeq++
		block := newSemanticBlock(SemanticKindTableRow, line.start, line.end, SemanticConfidenceSoft, false, 0)
		block.TableID = semanticTableID(scanner.tableSeq)
		return index + 1, block, true
	}
	return 0, SemanticBlock{}, false
}

func (scanner *semanticScanner) addBlank(index int) int {
	start := scanner.lines[index].start
	end := scanner.lines[index].end
	for index++; index < len(scanner.lines) && scanner.lines[index].trimmed == ""; index++ {
		end = scanner.lines[index].end
	}
	scanner.blocks = append(scanner.blocks,
		newSemanticBlock(SemanticKindParagraph, start, end, SemanticConfidenceSoft, false, 0))
	return index
}

func (scanner *semanticScanner) addParagraph(index int) int {
	start := scanner.lines[index].start
	end := scanner.lines[index].end
	index++
	for index < len(scanner.lines) && scanner.lines[index].trimmed != "" && !scanner.isStructuralStart(index) {
		end = scanner.lines[index].end
		index++
	}
	scanner.blocks = append(scanner.blocks,
		newSemanticBlock(SemanticKindParagraph, start, end, SemanticConfidenceHigh, true, 0))
	return index
}

func (scanner *semanticScanner) isStructuralStart(index int) bool {
	line := scanner.lines[index]
	if isFenceStart(line.trimmed) || strings.Contains(strings.ToLower(line.trimmed), "<table") {
		return true
	}
	if _, _, ok := semanticHeading(line.trimmed, scanner.lines, index); ok {
		return true
	}
	if semanticListPattern.MatchString(line.trimmed) || imageRefPattern.MatchString(line.trimmed) {
		return true
	}
	if tableRowPattern.MatchString(line.trimmed) || semanticKeyValue.MatchString(line.trimmed) {
		return true
	}
	return isExplicitQuestion(line.trimmed)
}

func (scanner *semanticScanner) consumeFence(index int) (int, SemanticBlock, bool) {
	start := scanner.lines[index].start
	marker := scanner.lines[index].trimmed[:3]
	end := scanner.lines[index].end
	for index++; index < len(scanner.lines); index++ {
		end = scanner.lines[index].end
		if strings.HasPrefix(scanner.lines[index].trimmed, marker) {
			return index + 1, newSemanticBlock(SemanticKindCodeBlock, start, end, SemanticConfidenceHigh, true, 0), true
		}
	}
	return index, newSemanticBlock(SemanticKindCodeBlock, start, end, SemanticConfidenceSoft, false, 0), true
}

func isFenceStart(value string) bool {
	return strings.HasPrefix(value, "```") || strings.HasPrefix(value, "~~~")
}

func semanticHeading(value string, lines []semanticLine, index int) (int, string, bool) {
	if match := MarkdownHeadingPattern.FindStringSubmatch(value); match != nil {
		return len(match[1]), SemanticConfidenceHigh, true
	}
	if match := semanticHTMLHeading.FindStringSubmatch(value); match != nil {
		return int(match[1][0] - '0'), SemanticConfidenceHigh, true
	}
	if NumberedSectionPattern.MatchString(value) || ChineseChapterPattern.MatchString(value) ||
		EnglishChapterPattern.MatchString(value) || GermanChapterPattern.MatchString(value) ||
		AllCapsHeadingPattern.MatchString(value) {
		return 1, SemanticConfidenceSoft, true
	}
	if utf8.RuneCountInString(value) > 80 || strings.ContainsAny(value, "。！？!?；;") {
		return 0, "", false
	}
	if index+2 < len(lines) && lines[index+1].trimmed == "" && lines[index+2].trimmed != "" {
		return 1, SemanticConfidenceSoft, true
	}
	return 0, "", false
}

func newSemanticBlock(kind string, start, end int, confidence string, atomic bool, depth int) SemanticBlock {
	return SemanticBlock{
		Kind: kind, Start: start, End: end, Confidence: confidence,
		Atomic: atomic, SectionDepth: depth,
	}
}

func markSemanticPreamble(blocks []SemanticBlock) {
	firstHeading := -1
	for _, block := range blocks {
		if block.Kind == SemanticKindHeading {
			firstHeading = block.Start
			break
		}
	}
	if firstHeading < 0 {
		return
	}
	for index := range blocks {
		if blocks[index].End > firstHeading {
			return
		}
		if blocks[index].Kind == SemanticKindParagraph {
			blocks[index].Kind = SemanticKindPreamble
		}
	}
}

func semanticTableID(sequence int) string  { return fmt.Sprintf("table_%04d", sequence) }
func semanticRecordID(sequence int) string { return fmt.Sprintf("record_%04d", sequence) }
