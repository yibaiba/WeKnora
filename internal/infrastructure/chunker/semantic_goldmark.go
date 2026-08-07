package chunker

import (
	"sort"
	"strings"
	"unicode/utf8"

	goldmark "github.com/yuin/goldmark"
	goldmarkast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extensionast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

const (
	semanticASTHeading = "heading"
	semanticASTList    = "list"
	semanticASTCode    = "code"
	semanticASTTable   = "table"
)

type semanticASTBlock struct {
	kind    string
	endLine int
	depth   int
}

func buildSemanticASTBlocks(content string, lines []semanticLine) map[int]semanticASTBlock {
	if len(lines) == 0 {
		return nil
	}
	source := []byte(content)
	document := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(source))
	byteToRune := buildByteToRuneIndex(source)
	blocks := make(map[int]semanticASTBlock)
	_ = goldmarkast.Walk(document, func(node goldmarkast.Node, entering bool) (goldmarkast.WalkStatus, error) {
		if !entering || hasSemanticListAncestor(node) {
			return goldmarkast.WalkContinue, nil
		}
		kind, depth := semanticASTNodeKind(node)
		if kind == "" {
			return goldmarkast.WalkContinue, nil
		}
		start, end := semanticASTNodeRange(node, byteToRune)
		startLine := semanticLineAtRune(lines, start)
		endLine := semanticLineAfterRune(lines, end)
		if kind == semanticASTHeading {
			endLine = extendSetextHeading(lines, startLine, endLine)
		}
		if kind == semanticASTCode {
			endLine = extendFencedCode(lines, startLine, endLine)
		}
		if startLine >= 0 && endLine > startLine {
			blocks[startLine] = semanticASTBlock{kind: kind, endLine: endLine, depth: depth}
		}
		return goldmarkast.WalkContinue, nil
	})
	return blocks
}

func buildByteToRuneIndex(source []byte) []int {
	index := make([]int, len(source)+1)
	runeOffset := 0
	for byteOffset := 0; byteOffset < len(source); {
		_, width := utf8.DecodeRune(source[byteOffset:])
		if width == 0 {
			width = 1
		}
		for current := byteOffset; current < byteOffset+width; current++ {
			index[current] = runeOffset
		}
		byteOffset += width
		runeOffset++
	}
	index[len(source)] = runeOffset
	return index
}

func semanticASTNodeKind(node goldmarkast.Node) (string, int) {
	switch typed := node.(type) {
	case *goldmarkast.Heading:
		return semanticASTHeading, typed.Level
	case *goldmarkast.FencedCodeBlock, *goldmarkast.CodeBlock:
		return semanticASTCode, 0
	case *goldmarkast.ListItem:
		return semanticASTList, 0
	case *extensionast.Table:
		return semanticASTTable, 0
	default:
		return "", 0
	}
}

func hasSemanticListAncestor(node goldmarkast.Node) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if _, ok := parent.(*goldmarkast.ListItem); ok {
			return true
		}
	}
	return false
}

func semanticASTNodeRange(node goldmarkast.Node, byteToRune []int) (int, int) {
	startByte := node.Pos()
	_, isTable := node.(*extensionast.Table)
	if isTable && node.FirstChild() != nil {
		startByte = node.FirstChild().Pos()
	}
	endByte := startByte
	_ = goldmarkast.Walk(node, func(current goldmarkast.Node, entering bool) (goldmarkast.WalkStatus, error) {
		if !entering {
			return goldmarkast.WalkContinue, nil
		}
		if current.Type() != goldmarkast.TypeBlock {
			return goldmarkast.WalkContinue, nil
		}
		if current.Pos() >= startByte {
			endByte = max(endByte, current.Pos()+1)
		}
		segments := current.Lines()
		for index := 0; index < segments.Len(); index++ {
			segment := segments.At(index)
			startByte = min(startByte, segment.Start)
			endByte = max(endByte, segment.Stop)
		}
		return goldmarkast.WalkContinue, nil
	})
	startByte = max(0, min(startByte, len(byteToRune)-1))
	endByte = max(startByte+1, min(endByte, len(byteToRune)-1))
	return byteToRune[startByte], byteToRune[endByte]
}

func semanticLineAtRune(lines []semanticLine, position int) int {
	index := sort.Search(len(lines), func(index int) bool { return lines[index].end > position })
	if index == len(lines) {
		return -1
	}
	return index
}

func semanticLineAfterRune(lines []semanticLine, position int) int {
	index := semanticLineAtRune(lines, max(0, position-1))
	if index < 0 {
		return len(lines)
	}
	return index + 1
}

func extendSetextHeading(lines []semanticLine, start, end int) int {
	if start < 0 || end >= len(lines) {
		return end
	}
	underline := strings.TrimSpace(lines[end].text)
	if underline == "" {
		return end
	}
	marker := underline[0]
	if (marker == '=' || marker == '-') && strings.Trim(underline, string(marker)+" \t\r\n") == "" {
		return end + 1
	}
	return end
}

func extendFencedCode(lines []semanticLine, start, end int) int {
	if start < 0 || start >= len(lines) {
		return end
	}
	marker, width, ok := semanticFenceMarker(semanticUnquote(lines[start].trimmed))
	if !ok {
		return end
	}
	for index := start + 1; index < len(lines); index++ {
		candidate := strings.TrimSpace(semanticUnquote(lines[index].trimmed))
		candidateMarker, candidateWidth, candidateOK := semanticFenceMarker(
			candidate,
		)
		if candidateOK && candidateMarker == marker && candidateWidth >= width &&
			strings.TrimSpace(candidate[candidateWidth:]) == "" {
			return index + 1
		}
	}
	return max(end, len(lines))
}

func semanticFenceMarker(value string) (byte, int, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 3 || (value[0] != '`' && value[0] != '~') {
		return 0, 0, false
	}
	width := 1
	for width < len(value) && value[width] == value[0] {
		width++
	}
	return value[0], width, width >= 3
}
