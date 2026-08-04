package service

import (
	"fmt"
	"math"
	"sort"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

func buildIngestionCandidate(
	content string,
	config types.IngestionChunkingRecommendation,
	id string,
) (types.IngestionChunkingCandidate, error) {
	base := chunker.NormalizeConfig(chunker.SplitterConfig{
		ChunkSize:        config.ChunkSize,
		ChunkOverlap:     config.ChunkOverlap,
		AllowZeroOverlap: true,
		Separators:       append([]string(nil), config.Separators...),
		Strategy:         config.Strategy,
	})
	chunks, parents, parentIndexes, diagnostics, scoreConfig := splitIngestionPreview(content, config, base)
	if err := validateIngestionChunkPositions(content, chunks); err != nil {
		return types.IngestionChunkingCandidate{}, err
	}
	if len(parents) > 0 {
		if err := validateIngestionChunkPositions(content, parents); err != nil {
			return types.IngestionChunkingCandidate{}, fmt.Errorf("父块位置校验失败: %w", err)
		}
	}
	if config.EnableParentChild {
		if err := validateParentChildPreview(chunks, parents, parentIndexes); err != nil {
			return types.IngestionChunkingCandidate{}, err
		}
	}

	metrics := ingestionPreviewMetrics(content, chunks, parents, parentIndexes, config, scoreConfig)
	return types.IngestionChunkingCandidate{
		ID:               id,
		Config:           cloneChunkingRecommendation(config),
		ChunkCount:       len(chunks),
		ParentChunkCount: len(parents),
		Lengths:          metrics.lengths,
		Structure:        metrics.structure,
		Diagnostics:      convertIngestionDiagnostics(diagnostics),
		Score:            metrics.score,
		HardValid:        true,
		Violations:       []string{},
	}, nil
}

func splitIngestionPreview(
	content string,
	config types.IngestionChunkingRecommendation,
	base chunker.SplitterConfig,
) ([]chunker.Chunk, []chunker.Chunk, []int, *chunker.Diagnostics, chunker.SplitterConfig) {
	if !config.EnableParentChild {
		chunks, diagnostics := chunker.SplitWithDiagnostics(content, base)
		return chunks, nil, nil, diagnostics, base
	}
	chunkingConfig := types.ChunkingConfig{
		ChunkSize:         config.ChunkSize,
		ChunkOverlap:      config.ChunkOverlap,
		Separators:        append([]string(nil), config.Separators...),
		EnableParentChild: true,
		ParentChunkSize:   config.ParentChunkSize,
		ChildChunkSize:    config.ChildChunkSize,
		Strategy:          config.Strategy,
	}
	parentConfig, childConfig := buildParentChildConfigs(chunkingConfig, base)
	_, diagnostics := chunker.SplitWithDiagnostics(content, parentConfig)
	result := chunker.SplitParentChild(content, parentConfig, childConfig)
	children := make([]chunker.Chunk, len(result.Children))
	parentIndexes := make([]int, len(result.Children))
	for index, child := range result.Children {
		children[index] = child.Chunk
		parentIndexes[index] = child.ParentIndex
	}
	return children, result.Parents, parentIndexes, diagnostics, childConfig
}

func validateIngestionChunkPositions(content string, chunks []chunker.Chunk) error {
	if len(chunks) == 0 {
		return fmt.Errorf("预切分结果为空")
	}
	runes := []rune(content)
	lastEnd := 0
	for index, current := range chunks {
		if current.Start < 0 || current.End <= current.Start || current.End > len(runes) {
			return fmt.Errorf("块 %d 的位置 [%d,%d) 越界", index, current.Start, current.End)
		}
		if string(runes[current.Start:current.End]) != current.Content {
			return fmt.Errorf("块 %d 的位置与内容不一致", index)
		}
		if index > 0 && current.End <= lastEnd {
			return fmt.Errorf("块 %d 的结束位置未递增", index)
		}
		lastEnd = current.End
	}
	return nil
}

func validateParentChildPreview(children, parents []chunker.Chunk, parentIndexes []int) error {
	if len(children) != len(parentIndexes) {
		return fmt.Errorf("父子映射数量与子块数量不一致")
	}
	for index, parentIndex := range parentIndexes {
		if parentIndex == -1 {
			continue
		}
		if parentIndex < 0 || parentIndex >= len(parents) {
			return fmt.Errorf("子块 %d 的父块索引 %d 无效", index, parentIndex)
		}
		child := children[index]
		parent := parents[parentIndex]
		if child.Start < parent.Start || child.End > parent.End {
			return fmt.Errorf("子块 %d 不在父块 %d 范围内", index, parentIndex)
		}
	}
	return nil
}

func ingestionLengthDistribution(chunks []chunker.Chunk) types.IngestionLengthDistribution {
	lengths := make([]int, len(chunks))
	total := 0
	for index, current := range chunks {
		lengths[index] = len([]rune(current.Content))
		total += lengths[index]
	}
	sort.Ints(lengths)
	return types.IngestionLengthDistribution{
		Minimum: lengths[0],
		Maximum: lengths[len(lengths)-1],
		Average: roundScore(float64(total) / float64(len(lengths))),
		P50:     percentileLength(lengths, 0.50),
		P95:     percentileLength(lengths, 0.95),
	}
}

func percentileLength(sorted []int, percentile float64) int {
	index := int(math.Ceil(float64(len(sorted))*percentile)) - 1
	if index < 0 {
		index = 0
	}
	return sorted[index]
}

func convertIngestionDiagnostics(value *chunker.Diagnostics) types.IngestionChunkerDiagnostics {
	if value == nil {
		return types.IngestionChunkerDiagnostics{}
	}
	result := types.IngestionChunkerDiagnostics{SelectedTier: string(value.SelectedTier)}
	for _, tier := range value.TierChain {
		result.TierChain = append(result.TierChain, string(tier))
	}
	for _, rejected := range value.Rejected {
		result.Rejected = append(result.Rejected, types.IngestionTierRejection{
			Tier: string(rejected.Tier), Reason: rejected.Reason,
		})
	}
	return result
}
