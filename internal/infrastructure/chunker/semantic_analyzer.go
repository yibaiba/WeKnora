package chunker

import (
	"fmt"
	"sort"
)

// AnalyzeSemanticDocument builds local structure first, then overlays only
// source-validated and boundary-aligned parser hints.
func AnalyzeSemanticDocument(content string, options SemanticAnalysisOptions) (SemanticDocument, error) {
	blocks := analyzeLocalSemanticBlocks(content)
	diagnostics := SemanticDiagnostics{HintsProvided: len(options.Hints)}
	if len(options.Hints) > 0 {
		source := options.HintSource
		if source == "" {
			source = content
		}
		relocated := relocateSemanticHints(source, content, options.Hints, &diagnostics)
		blocks = mergeSemanticHints(blocks, relocated, &diagnostics)
	}
	assignSemanticHierarchy(blocks)
	document := SemanticDocument{
		ContentLength: len([]rune(content)), Blocks: blocks, Diagnostics: diagnostics,
	}
	if err := ValidateSemanticDocument(document); err != nil {
		return SemanticDocument{}, fmt.Errorf("analyze semantic document: %w", err)
	}
	return document, nil
}

func mergeSemanticHints(
	blocks []SemanticBlock,
	hints []SemanticBlock,
	diagnostics *SemanticDiagnostics,
) []SemanticBlock {
	sort.SliceStable(hints, func(i, j int) bool {
		if hints[i].Start != hints[j].Start {
			return hints[i].Start < hints[j].Start
		}
		return hints[i].End < hints[j].End
	})
	lastEnd := -1
	for _, hint := range hints {
		if hint.Start < lastEnd {
			rejectSemanticHint(diagnostics, "hint_overlap")
			continue
		}
		blocks = splitSemanticBlocksAt(blocks, hint.Start)
		blocks = splitSemanticBlocksAt(blocks, hint.End)
		start, end := alignedSemanticBlockRange(blocks, hint.Start, hint.End)
		if start < 0 {
			rejectSemanticHint(diagnostics, "hint_unaligned")
			continue
		}
		blocks = append(blocks[:start], append([]SemanticBlock{hint}, blocks[end:]...)...)
		diagnostics.HintsAccepted++
		lastEnd = hint.End
	}
	return blocks
}

func splitSemanticBlocksAt(blocks []SemanticBlock, position int) []SemanticBlock {
	for index, block := range blocks {
		if position <= block.Start || position >= block.End {
			continue
		}
		left, right := block, block
		left.End = position
		right.Start = position
		result := make([]SemanticBlock, 0, len(blocks)+1)
		result = append(result, blocks[:index]...)
		result = append(result, left, right)
		return append(result, blocks[index+1:]...)
	}
	return blocks
}

func alignedSemanticBlockRange(blocks []SemanticBlock, start, end int) (int, int) {
	first := -1
	for index, block := range blocks {
		if block.Start == start {
			first = index
		}
		if first >= 0 && block.End == end {
			return first, index + 1
		}
		if block.Start > start || block.End > end {
			break
		}
	}
	return -1, -1
}

func assignSemanticHierarchy(blocks []SemanticBlock) {
	oldIDs := make([]string, len(blocks))
	idMap := make(map[string]string, len(blocks))
	for index := range blocks {
		oldIDs[index] = blocks[index].ID
		blocks[index].ID = fmt.Sprintf("block_%06d", index+1)
		if oldIDs[index] != "" {
			idMap[oldIDs[index]] = blocks[index].ID
		}
	}
	stack := make(map[int]string)
	for index := range blocks {
		block := &blocks[index]
		if explicit := idMap[block.ParentID]; explicit != "" {
			block.ParentID = explicit
			continue
		}
		if block.Kind == SemanticKindHeading {
			block.ParentID = nearestSemanticHeading(stack, block.SectionDepth-1)
			for depth := range stack {
				if depth >= block.SectionDepth {
					delete(stack, depth)
				}
			}
			stack[block.SectionDepth] = block.ID
			continue
		}
		block.ParentID = nearestSemanticHeading(stack, 6)
	}
}

func nearestSemanticHeading(stack map[int]string, maximum int) string {
	for depth := maximum; depth > 0; depth-- {
		if id := stack[depth]; id != "" {
			return id
		}
	}
	return ""
}
