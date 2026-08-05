package service

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
)

const ingestionDocumentAnalysisMinimumSafetyTokens = 512

type ingestionDocumentAnalysisTokenBudget struct {
	ContextWindowTokens int
	CompletionTokens    int
	PromptSchemaTokens  int
	SafetyTokens        int
	ContentTokens       int
}

type ingestionDocumentAnalysisUnit struct {
	Index           int
	Start           int
	End             int
	EstimatedTokens int
	Content         string
}

func calculateIngestionDocumentAnalysisTokenBudget(
	contextWindowTokens int,
	documentRuneCount int,
) (ingestionDocumentAnalysisTokenBudget, error) {
	safetyTokens := max(ingestionDocumentAnalysisMinimumSafetyTokens, (contextWindowTokens+9)/10)
	promptSchemaTokens := ingestionDocumentMapPromptSchemaTokens(documentRuneCount)
	budget := ingestionDocumentAnalysisTokenBudget{
		ContextWindowTokens: contextWindowTokens,
		CompletionTokens:    ingestionDocumentAnalysisCompletionTokens,
		PromptSchemaTokens:  promptSchemaTokens,
		SafetyTokens:        safetyTokens,
	}
	budget.ContentTokens = contextWindowTokens - budget.CompletionTokens - promptSchemaTokens - safetyTokens
	if budget.ContentTokens <= 0 {
		return budget, fmt.Errorf(
			"模型上下文窗口 token 预算不足: context=%d, output=%d, prompt_schema=%d, safety=%d",
			contextWindowTokens, budget.CompletionTokens, promptSchemaTokens, safetyTokens,
		)
	}
	return budget, nil
}

func ingestionDocumentMapPromptSchemaTokens(documentRuneCount int) int {
	maximumIndex := max(documentRuneCount-1, 0)
	maximumUnits := max(documentRuneCount, 1)
	wrapper := buildIngestionDocumentMapPrompt(ingestionDocumentAnalysisUnit{
		Index: maximumIndex, Start: documentRuneCount, End: documentRuneCount,
	}, maximumUnits)
	return estimateIngestionDocumentTokens(ingestionDocumentMapSystemPrompt) +
		estimateIngestionDocumentTokens(string(ingestionDocumentEvidenceSchema)) +
		estimateIngestionDocumentTokens(wrapper)
}

func splitIngestionDocumentAnalysisUnits(
	content string,
	tokenBudget int,
) ([]ingestionDocumentAnalysisUnit, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("待分析文档正文为空")
	}
	if tokenBudget <= 0 {
		return nil, fmt.Errorf("文档分析正文 token 预算必须大于 0")
	}

	config := chunker.DefaultConfig()
	config.ChunkSize = max(1, chunker.CharsForTokenLimit(tokenBudget, chunker.DetectLanguage(content)))
	config.ChunkOverlap = 0
	config.AllowZeroOverlap = true
	chunks := chunker.SplitText(content, config)
	runes := []rune(content)
	if err := validateIngestionDocumentChunkCoverage(chunks, len(runes)); err != nil {
		return nil, err
	}
	chunks = splitOversizeIngestionDocumentChunks(chunks, runes, tokenBudget)
	if err := validateIngestionDocumentChunkCoverage(chunks, len(runes)); err != nil {
		return nil, err
	}
	units := coalesceIngestionDocumentAnalysisUnits(chunks, runes, tokenBudget)
	if err := validateIngestionDocumentAnalysisCoverage(content, units, tokenBudget); err != nil {
		return nil, err
	}
	return units, nil
}

func validateIngestionDocumentChunkCoverage(chunks []chunker.Chunk, runeCount int) error {
	cursor := 0
	for index, chunk := range chunks {
		if chunk.Start != cursor || chunk.End <= chunk.Start || chunk.End > runeCount {
			return fmt.Errorf(
				"文档分析切分块 %d 范围无效或不连续: [%d,%d)，期望起点 %d",
				index, chunk.Start, chunk.End, cursor,
			)
		}
		cursor = chunk.End
	}
	if cursor != runeCount {
		return fmt.Errorf("文档分析切分块覆盖不完整: 已覆盖 %d，总长度 %d", cursor, runeCount)
	}
	return nil
}

func splitOversizeIngestionDocumentChunks(
	chunks []chunker.Chunk,
	runes []rune,
	tokenBudget int,
) []chunker.Chunk {
	result := make([]chunker.Chunk, 0, len(chunks))
	for _, current := range chunks {
		cursor := current.Start
		for cursor < current.End {
			end := largestIngestionDocumentPrefix(runes, cursor, current.End, tokenBudget)
			if end < current.End {
				end = ingestionDocumentSemanticBoundary(runes, cursor, end)
			}
			result = append(result, chunker.Chunk{
				Seq: len(result), Start: cursor, End: end, Content: string(runes[cursor:end]),
			})
			cursor = end
		}
	}
	return result
}

func largestIngestionDocumentPrefix(runes []rune, start, end, tokenBudget int) int {
	if estimateIngestionDocumentTokens(string(runes[start:end])) <= tokenBudget {
		return end
	}
	low, high := start+1, end
	for low < high {
		middle := low + (high-low+1)/2
		if estimateIngestionDocumentTokens(string(runes[start:middle])) <= tokenBudget {
			low = middle
			continue
		}
		high = middle - 1
	}
	return low
}

func ingestionDocumentSemanticBoundary(runes []rune, start, end int) int {
	minimum := start + (end-start)/2
	for index := end; index > minimum; index-- {
		if isIngestionDocumentBoundaryRune(runes[index-1]) {
			return index
		}
	}
	return end
}

func isIngestionDocumentBoundaryRune(value rune) bool {
	switch value {
	case '\n', ' ', '\t', '。', '！', '？', '；', '.', '!', '?', ';':
		return true
	default:
		return false
	}
}

func coalesceIngestionDocumentAnalysisUnits(
	chunks []chunker.Chunk,
	runes []rune,
	tokenBudget int,
) []ingestionDocumentAnalysisUnit {
	units := make([]ingestionDocumentAnalysisUnit, 0, len(chunks))
	for _, current := range chunks {
		last := len(units) - 1
		if last >= 0 {
			candidate := string(runes[units[last].Start:current.End])
			candidateTokens := estimateIngestionDocumentTokens(candidate)
			if candidateTokens <= tokenBudget {
				units[last].End = current.End
				units[last].Content = candidate
				units[last].EstimatedTokens = candidateTokens
				continue
			}
		}
		content := string(runes[current.Start:current.End])
		units = append(units, ingestionDocumentAnalysisUnit{
			Index: len(units), Start: current.Start, End: current.End,
			EstimatedTokens: estimateIngestionDocumentTokens(content), Content: content,
		})
	}
	return units
}

func validateIngestionDocumentAnalysisCoverage(
	content string,
	units []ingestionDocumentAnalysisUnit,
	tokenBudget int,
) error {
	runes := []rune(content)
	if len(runes) > 0 && len(units) == 0 {
		return fmt.Errorf("文档分析单元未覆盖非空正文")
	}
	cursor := 0
	for index, unit := range units {
		if unit.Index != index {
			return fmt.Errorf("文档分析单元顺序错误: 位置 %d 的索引为 %d", index, unit.Index)
		}
		if unit.Start != cursor {
			return fmt.Errorf("文档分析单元覆盖不连续: 期望起点 %d，实际 %d", cursor, unit.Start)
		}
		if unit.End <= unit.Start || unit.End > len(runes) {
			return fmt.Errorf("文档分析单元 %d 范围无效: [%d,%d)", index, unit.Start, unit.End)
		}
		if unit.Content != string(runes[unit.Start:unit.End]) {
			return fmt.Errorf("文档分析单元 %d 内容与原文位置不一致", index)
		}
		estimatedTokens := estimateIngestionDocumentTokens(unit.Content)
		if estimatedTokens != unit.EstimatedTokens {
			return fmt.Errorf("文档分析单元 %d token 估算不一致", index)
		}
		if estimatedTokens > tokenBudget {
			return fmt.Errorf(
				"文档分析单元 %d 估算 token %d 超过正文预算 %d",
				index, estimatedTokens, tokenBudget,
			)
		}
		cursor = unit.End
	}
	if cursor != len(runes) {
		return fmt.Errorf("文档分析单元覆盖不完整: 已覆盖 %d，总长度 %d", cursor, len(runes))
	}
	return nil
}

func estimateIngestionDocumentTokens(content string) int {
	return chunker.ApproxTokenCount(content, chunker.DetectLanguage(content))
}

func ingestionDocumentRuneCount(content string) int {
	return utf8.RuneCountInString(content)
}
