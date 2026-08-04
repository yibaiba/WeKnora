package service

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
)

const ingestionDocumentAnalysisUnitRunes = 8000

type ingestionDocumentAnalysisUnit struct {
	Index   int
	Start   int
	End     int
	Content string
}

func splitIngestionDocumentAnalysisUnits(content string) ([]ingestionDocumentAnalysisUnit, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("待分析文档正文为空")
	}

	config := chunker.DefaultConfig()
	config.ChunkSize = ingestionDocumentAnalysisUnitRunes
	config.ChunkOverlap = 0
	config.AllowZeroOverlap = true
	chunks := chunker.SplitText(content, config)
	runes := []rune(content)
	units := make([]ingestionDocumentAnalysisUnit, 0, len(chunks))
	for index, chunk := range chunks {
		if chunk.Start < 0 || chunk.End < chunk.Start || chunk.End > len(runes) {
			return nil, fmt.Errorf("文档分析单元 %d 位置越界: [%d,%d)", index, chunk.Start, chunk.End)
		}
		units = append(units, ingestionDocumentAnalysisUnit{
			Index: index, Start: chunk.Start, End: chunk.End,
			Content: string(runes[chunk.Start:chunk.End]),
		})
	}
	if err := validateIngestionDocumentAnalysisCoverage(content, units); err != nil {
		return nil, err
	}
	return units, nil
}

func validateIngestionDocumentAnalysisCoverage(
	content string,
	units []ingestionDocumentAnalysisUnit,
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
		cursor = unit.End
	}
	if cursor != len(runes) {
		return fmt.Errorf("文档分析单元覆盖不完整: 已覆盖 %d，总长度 %d", cursor, len(runes))
	}
	return nil
}
