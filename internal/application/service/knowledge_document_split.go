package service

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

type knowledgeDocumentSplitRequest struct {
	content      string
	effective    types.EffectiveProcessConfig
	document     chunker.SemanticDocument
	tokenCounter types.TokenCounter
}

type knowledgeDocumentSplitResult struct {
	chunks  []types.ParsedChunk
	parents []types.ParsedParentChunk
}

type knowledgeChunkSet struct {
	chunks        []chunker.Chunk
	parents       []chunker.Chunk
	parentIndexes []int
	overlap       int
}

func splitKnowledgeDocument(
	request knowledgeDocumentSplitRequest,
) (knowledgeDocumentSplitResult, error) {
	base := buildSplitterConfigFromEffective(request.effective)
	base.TokenCounter = request.tokenCounter
	set, err := buildKnowledgeChunkSet(request, base)
	if err != nil {
		return knowledgeDocumentSplitResult{}, err
	}
	if request.effective.IngestionAppliedMode == types.IngestionAppliedModeFallback {
		if err := validateOrdinaryKnowledgeSplit(request.content, set); err != nil {
			return knowledgeDocumentSplitResult{}, fmt.Errorf("普通分块回退校验失败: %w", err)
		}
	}
	return mapKnowledgeChunkSet(set), nil
}

func buildKnowledgeChunkSet(
	request knowledgeDocumentSplitRequest,
	base chunker.SplitterConfig,
) (knowledgeChunkSet, error) {
	if !request.effective.ChunkingConfig.EnableParentChild {
		chunks, err := splitKnowledgeFlat(request, base)
		return knowledgeChunkSet{chunks: chunks, overlap: base.ChunkOverlap}, err
	}
	parent, child := buildParentChildConfigs(request.effective.ChunkingConfig, base)
	parent = chunker.NormalizeConfig(parent)
	child = chunker.NormalizeConfig(child)
	result, err := splitKnowledgeParentChild(request, parent, child)
	if err != nil {
		return knowledgeChunkSet{}, err
	}
	set := knowledgeChunkSet{parents: result.Parents, overlap: max(parent.ChunkOverlap, child.ChunkOverlap)}
	for _, current := range result.Children {
		set.chunks = append(set.chunks, current.Chunk)
		set.parentIndexes = append(set.parentIndexes, current.ParentIndex)
	}
	return set, nil
}

func splitKnowledgeFlat(
	request knowledgeDocumentSplitRequest,
	config chunker.SplitterConfig,
) ([]chunker.Chunk, error) {
	if usesSemanticProductionSplit(request.effective) {
		chunks, err := chunker.SplitSemanticDocument(request.content, config, request.document)
		if err != nil {
			return nil, fmt.Errorf("生产语义分块失败: %w", err)
		}
		return chunks, nil
	}
	return chunker.Split(request.content, config), nil
}

func splitKnowledgeParentChild(
	request knowledgeDocumentSplitRequest,
	parent chunker.SplitterConfig,
	child chunker.SplitterConfig,
) (chunker.ParentChildResult, error) {
	if usesSemanticProductionSplit(request.effective) {
		result, err := chunker.SplitParentChildSemanticDocument(chunker.SemanticParentChildRequest{
			Content: request.content, ParentConfig: parent,
			ChildConfig: child, Document: request.document,
		})
		if err != nil {
			return chunker.ParentChildResult{}, fmt.Errorf("生产父子语义分块失败: %w", err)
		}
		return result, nil
	}
	return chunker.SplitParentChild(request.content, parent, child), nil
}

func usesSemanticProductionSplit(effective types.EffectiveProcessConfig) bool {
	return effective.IngestionAdvisorApplied &&
		effective.ChunkingConfig.Strategy == chunker.StrategyAuto
}

func validateOrdinaryKnowledgeSplit(content string, set knowledgeChunkSet) error {
	violations := make(ingestionViolationSet)
	if validateIngestionChunkPositions(content, set.chunks) != nil {
		violations.add(ingestionViolationSourcePosition)
	}
	if validateIngestionChunkOrder(set.chunks) != nil {
		violations.add(ingestionViolationSourceOrder)
	}
	validateChunkCoverage(set.chunks, ingestionCoverageOptions{
		totalLength: len([]rune(content)), allowedOverlap: set.overlap,
	}, violations)
	if len(set.parents) > 0 && validateOrdinaryParentMapping(content, set) != nil {
		violations.add(ingestionViolationParentMapping)
	}
	if len(violations) > 0 {
		return fmt.Errorf("%s", strings.Join(sortedIngestionViolations(violations), ","))
	}
	return nil
}

func validateOrdinaryParentMapping(content string, set knowledgeChunkSet) error {
	if validateIngestionChunkPositions(content, set.parents) != nil {
		return fmt.Errorf("父块位置无效")
	}
	if validateIngestionChunkOrder(set.parents) != nil {
		return fmt.Errorf("父块顺序无效")
	}
	return validateParentChildPreview(set.chunks, set.parents, set.parentIndexes)
}

func mapKnowledgeChunkSet(set knowledgeChunkSet) knowledgeDocumentSplitResult {
	result := knowledgeDocumentSplitResult{
		chunks:  make([]types.ParsedChunk, len(set.chunks)),
		parents: make([]types.ParsedParentChunk, len(set.parents)),
	}
	for index, current := range set.chunks {
		result.chunks[index] = types.ParsedChunk{
			Content: current.Content, ContextHeader: current.ContextHeader,
			Seq: current.Seq, Start: current.Start, End: current.End,
			ParentIndex: parentIndexAt(set.parentIndexes, index),
		}
	}
	for index, current := range set.parents {
		result.parents[index] = types.ParsedParentChunk{
			Content: current.Content, ContextHeader: current.ContextHeader,
			Seq: current.Seq, Start: current.Start, End: current.End,
		}
	}
	return result
}

func parentIndexAt(indexes []int, index int) int {
	if len(indexes) == 0 {
		return 0
	}
	return indexes[index]
}
