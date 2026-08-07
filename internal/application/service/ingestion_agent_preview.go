package service

import (
	"fmt"
	"math"
	"sort"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

type ingestionCandidateBuildRequest struct {
	content     string
	config      types.IngestionChunkingRecommendation
	constraints types.IngestionChunkingConstraints
	id          string
	document    chunker.SemanticDocument
	documentErr error
	archetype   string
	policy      types.SemanticPackingPolicy
}

func buildIngestionCandidate(request ingestionCandidateBuildRequest) (types.IngestionChunkingCandidate, error) {
	if request.documentErr != nil {
		return types.IngestionChunkingCandidate{}, wrapIngestionToolError(
			request.documentErr, ingestionFailureCandidatePreview, "", "semantic_document", "文档结构分析失败",
		)
	}
	if err := chunker.ValidateSemanticDocument(request.document); err != nil {
		return types.IngestionChunkingCandidate{}, wrapIngestionToolError(
			err, ingestionFailureCandidatePreview, "", "semantic_document", "文档结构校验失败",
		)
	}
	config := ingestionChunkingConfig(request.config, request.constraints)
	base := normalizeSplitterConfig(config, true)
	base.TokenCounter = request.constraints.TokenCounter
	base.EmbeddingPrefix = request.constraints.EmbeddingPrefix
	base.SemanticPackingPolicy = cloneSemanticPackingPolicy(request.policy)
	split, err := splitIngestionPreview(ingestionPreviewSplitRequest{
		content: request.content, config: config, base: base, document: request.document,
	})
	if err != nil {
		return types.IngestionChunkingCandidate{}, wrapIngestionToolError(
			err, ingestionFailureCandidatePreview, "", "semantic_chunking", "语义候选生成失败",
		)
	}
	validation, err := validateIngestionCandidate(ingestionCandidateValidationRequest{
		content: request.content, document: request.document, chunks: split.chunks,
		parents: split.parents, parentIndexes: split.parentIndexes,
		config: request.config, scoreConfig: split.scoreConfig, constraints: request.constraints,
	})
	if err != nil {
		return types.IngestionChunkingCandidate{}, wrapIngestionToolError(
			err, ingestionFailureCandidatePreview, "", "token_counter", "候选 token 校验失败",
		)
	}
	metrics := ingestionPreviewMetrics(ingestionCandidateMetricsRequest{
		content: request.content, document: request.document, chunks: split.chunks,
		parents: split.parents, parentIndexes: split.parentIndexes,
		config: request.config, scoreConfig: split.scoreConfig, validation: validation,
		tokenLimit: request.constraints.TokenLimit, policy: request.policy,
	})
	diagnostics := convertIngestionDiagnostics(split.diagnostics)
	diagnostics.ContextReasonCodes = ingestionContextReasonCodes(split.chunks)
	return types.IngestionChunkingCandidate{
		ID:                   request.id,
		Archetype:            request.archetype,
		TokenCountMode:       validation.tokenCountMode,
		TokenizerID:          validation.tokenizerID,
		PackingPolicyVersion: request.policy.Version,
		Config:               cloneChunkingRecommendation(request.config),
		ChunkCount:           len(split.chunks),
		ParentChunkCount:     len(split.parents),
		Lengths:              metrics.lengths,
		Structure:            metrics.structure,
		StructureQuality:     validation.quality,
		BlockDescriptions:    validation.descriptions,
		Diagnostics:          diagnostics,
		Score:                metrics.score,
		HardValid:            len(validation.violations) == 0,
		Violations:           validation.violations,
		ContextTokenRatio:    ingestionContextTokenRatio(validation),
	}, nil
}

func ingestionContextTokenRatio(validation ingestionCandidateValidationResult) float64 {
	sourceTokens := 0
	for _, count := range validation.sourceTokens {
		sourceTokens += count
	}
	total := sourceTokens + validation.contextTokens
	if total == 0 {
		return 0
	}
	return float64(validation.contextTokens) / float64(total)
}

func ingestionContextReasonCodes(chunks []chunker.Chunk) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, 2)
	for _, current := range chunks {
		for _, reason := range current.ContextReasonCodes {
			if _, ok := seen[reason]; ok {
				continue
			}
			seen[reason] = struct{}{}
			result = append(result, reason)
		}
	}
	sort.Strings(result)
	return result
}

type ingestionPreviewSplitRequest struct {
	content  string
	config   types.ChunkingConfig
	base     chunker.SplitterConfig
	document chunker.SemanticDocument
}

type ingestionPreviewSplitResult struct {
	chunks        []chunker.Chunk
	parents       []chunker.Chunk
	parentIndexes []int
	diagnostics   *chunker.Diagnostics
	scoreConfig   chunker.SplitterConfig
}

func splitIngestionPreview(request ingestionPreviewSplitRequest) (ingestionPreviewSplitResult, error) {
	if request.config.Strategy == chunker.StrategyAuto {
		return splitSemanticIngestionPreview(request)
	}
	if !request.config.EnableParentChild {
		chunks, diagnostics := chunker.SplitWithDiagnostics(request.content, request.base)
		return ingestionPreviewSplitResult{
			chunks: chunks, diagnostics: diagnostics, scoreConfig: request.base,
		}, nil
	}
	parentConfig, childConfig := buildParentChildConfigs(request.config, request.base)
	_, diagnostics := chunker.SplitWithDiagnostics(request.content, parentConfig)
	result := chunker.SplitParentChild(request.content, parentConfig, childConfig)
	return ingestionParentChildSplitResult(result, diagnostics, chunker.NormalizeConfig(childConfig)), nil
}

func splitSemanticIngestionPreview(
	request ingestionPreviewSplitRequest,
) (ingestionPreviewSplitResult, error) {
	diagnostics := &chunker.Diagnostics{
		SelectedTier: chunker.TierSemantic, TierChain: []chunker.StrategyTier{chunker.TierSemantic},
	}
	if !request.config.EnableParentChild {
		chunks, err := chunker.SplitSemanticDocument(request.content, request.base, request.document)
		return ingestionPreviewSplitResult{
			chunks: chunks, diagnostics: diagnostics, scoreConfig: request.base,
		}, err
	}
	parentConfig, childConfig := buildParentChildConfigs(request.config, request.base)
	childConfig = chunker.NormalizeConfig(childConfig)
	result, err := chunker.SplitParentChildSemanticDocument(chunker.SemanticParentChildRequest{
		Content: request.content, ParentConfig: parentConfig, ChildConfig: childConfig,
		Document: request.document,
	})
	return ingestionParentChildSplitResult(result, diagnostics, childConfig), err
}

func ingestionParentChildSplitResult(
	result chunker.ParentChildResult,
	diagnostics *chunker.Diagnostics,
	scoreConfig chunker.SplitterConfig,
) ingestionPreviewSplitResult {
	children := make([]chunker.Chunk, len(result.Children))
	parentIndexes := make([]int, len(result.Children))
	for index, child := range result.Children {
		children[index] = child.Chunk
		parentIndexes[index] = child.ParentIndex
	}
	return ingestionPreviewSplitResult{
		chunks: children, parents: result.Parents, parentIndexes: parentIndexes,
		diagnostics: diagnostics, scoreConfig: scoreConfig,
	}
}

func validateIngestionChunkPositions(content string, chunks []chunker.Chunk) error {
	if len(chunks) == 0 {
		return fmt.Errorf("预切分结果为空")
	}
	runes := []rune(content)
	for index, current := range chunks {
		if current.Start < 0 || current.End <= current.Start || current.End > len(runes) {
			return fmt.Errorf("块 %d 的位置 [%d,%d) 越界", index, current.Start, current.End)
		}
		if string(runes[current.Start:current.End]) != current.Content {
			return fmt.Errorf("块 %d 的位置与内容不一致", index)
		}
	}
	return nil
}

func validateIngestionChunkOrder(chunks []chunker.Chunk) error {
	lastStart, lastEnd := -1, 0
	for index, current := range chunks {
		if index > 0 && (current.Start <= lastStart || current.End <= lastEnd) {
			return fmt.Errorf("块 %d 的位置未递增", index)
		}
		lastStart = current.Start
		lastEnd = current.End
	}
	return nil
}

func validateParentChildPreview(children, parents []chunker.Chunk, parentIndexes []int) error {
	if len(children) != len(parentIndexes) {
		return fmt.Errorf("父子映射数量与子块数量不一致")
	}
	lastEndByParent := make(map[int]int, len(parents))
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
		if lastEnd, ok := lastEndByParent[parentIndex]; ok && child.End <= lastEnd {
			return fmt.Errorf("父块 %d 内子块 %d 的结束位置未递增", parentIndex, index)
		}
		lastEndByParent[parentIndex] = child.End
	}
	return nil
}

func ingestionTokenLengthDistribution(tokenLengths []int) types.IngestionLengthDistribution {
	if len(tokenLengths) == 0 {
		return types.IngestionLengthDistribution{}
	}
	lengths := append([]int(nil), tokenLengths...)
	total := 0
	for _, length := range lengths {
		total += length
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

func ingestionLengthDistribution(chunks []chunker.Chunk) types.IngestionLengthDistribution {
	lengths := make([]int, len(chunks))
	for index, current := range chunks {
		lengths[index] = len([]rune(current.Content))
	}
	return ingestionTokenLengthDistribution(lengths)
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
