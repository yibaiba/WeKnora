package service

import (
	"context"
	"fmt"
	"time"

	appconfig "github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type ingestionAdvisorRun struct {
	Knowledge *types.Knowledge
	KB        *types.KnowledgeBase
	Content   string
	Effective types.EffectiveProcessConfig
	Document  chunker.SemanticDocument
}

func (s *knowledgeService) applyIngestionAdvisor(
	ctx context.Context,
	run ingestionAdvisorRun,
) (types.EffectiveProcessConfig, error) {
	config, eligible, err := resolveIngestionAdvisorEligibility(run.KB, run.Knowledge)
	if err != nil {
		s.beginStage(ctx, run.Knowledge.ID, types.StageDocumentAnalysis, nil)
		return run.Effective, s.failIngestionAdvisor(ctx, run.Knowledge, err)
	}
	if !eligible {
		s.skipStage(ctx, run.Knowledge.ID, types.StageDocumentAnalysis, "disabled or unsupported source")
		return run.Effective, nil
	}

	s.beginStage(ctx, run.Knowledge.ID, types.StageDocumentAnalysis, types.JSONMap{
		"model_id":    run.KB.SummaryModelID,
		"text_length": len([]rune(run.Content)),
	})
	stage := s.tracker().LookupStage(
		ctx, run.Knowledge.ID, attemptFromCtx(ctx), types.StageDocumentAnalysis,
	)
	progress := newIngestionAgentSpanProgress(ctx, s.tracker(), stage)
	progress.RecordProfile(len([]rune(run.Content)))
	if err := s.clearPreviousIngestionAnalysis(ctx, run.Knowledge); err != nil {
		return run.Effective, s.failIngestionAdvisor(ctx, run.Knowledge, err)
	}
	advisorResult, err := s.analyzeIngestionContent(ctx, ingestionContentAnalysisRequest{
		Run: run, Config: config,
		AgentProgress: progress.Handle, AnalysisProgress: progress.HandleAnalysis,
	})
	progress.RecordEvaluation(advisorResult)
	if err != nil {
		return run.Effective, s.failIngestionAdvisor(ctx, run.Knowledge, err)
	}
	analysis := ingestionAnalysisFromAdvisorResult(advisorResult)

	run.Effective = applyIngestionAnalysis(run.Effective, analysis)
	analysis.AppliedChunking = appliedChunkingRecommendation(run.Effective)
	analysis.ModelID = run.KB.SummaryModelID
	if err := run.Knowledge.SetIngestionAnalysis(analysis); err != nil {
		return run.Effective, s.failIngestionAdvisor(ctx, run.Knowledge, fmt.Errorf("保存文档分析结果失败: %w", err))
	}
	run.Knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, run.Knowledge); err != nil {
		return run.Effective, s.failIngestionAdvisor(ctx, run.Knowledge, fmt.Errorf("持久化文档分析结果失败: %w", err))
	}

	s.endStage(ctx, run.Knowledge.ID, types.StageDocumentAnalysis, ingestionAnalysisOutput(analysis))
	return run.Effective, nil
}

type ingestionContentAnalysisRequest struct {
	Run              ingestionAdvisorRun
	Config           *types.IngestionAdvisorConfig
	AgentProgress    func(types.IngestionAgentStep)
	AnalysisProgress func(types.IngestionDocumentAnalysisProgress)
}

func (s *knowledgeService) analyzeIngestionContent(
	ctx context.Context,
	request ingestionContentAnalysisRequest,
) (*types.IngestionAdvisorResult, error) {
	if s.ingestionAdvisor == nil {
		return nil, fmt.Errorf("文档智能分析服务未配置")
	}
	run := request.Run
	constraints := ingestionChunkingConstraintsFromConfig(run.Effective.ChunkingConfig)
	counter, err := s.resolveIngestionTokenCounter(ctx, run.KB)
	if err != nil {
		return nil, err
	}
	constraints.TokenCounter = counter
	document := chunker.CloneSemanticDocument(run.Document)
	result, err := s.ingestionAdvisor.Analyze(ctx, types.IngestionAdvisorRequest{
		Content:             run.Content,
		KnowledgeID:         run.Knowledge.ID,
		KnowledgeBaseID:     run.KB.ID,
		KnowledgeBaseName:   run.KB.Name,
		KnowledgeBaseType:   run.KB.Type,
		TenantID:            run.KB.TenantID,
		VectorEnabled:       run.KB.IsVectorEnabled(),
		KeywordEnabled:      run.KB.IsKeywordEnabled(),
		GraphEnabled:        run.KB.IsGraphEnabled(),
		WikiEnabled:         run.KB.IsWikiEnabled(),
		ModelID:             run.KB.SummaryModelID,
		AllowWebAccess:      request.Config.AllowWebAccess,
		AllowReadOnlyMCP:    request.Config.AllowReadOnlyMCP,
		ChunkingConstraints: constraints,
		FallbackChunking:    ordinaryChunkingRecommendation(run.Effective.ChunkingConfig),
		SemanticDocument:    &document,
		ProgressFn:          request.AgentProgress,
		AnalysisProgressFn:  request.AnalysisProgress,
		Timeout:             appconfig.IngestionAdvisorTimeout(s.config),
	}, interfaces.IngestionAdvisorRuntime{WebSearchKnowledge: s})
	if err != nil {
		return result, err
	}
	if err := validateIngestionAdvisorResultWithConstraints(result, constraints); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *knowledgeService) resolveIngestionTokenCounter(
	ctx context.Context,
	kb *types.KnowledgeBase,
) (types.TokenCounter, error) {
	config := chunker.TokenCounterConfig{Encoding: chunker.TokenizerEncodingByteUpperBound}
	if kb == nil || kb.EmbeddingModelID == "" {
		return chunker.NewTokenCounter(config)
	}
	if s.modelService == nil {
		return nil, fmt.Errorf("embedding model service is unavailable")
	}
	model, err := s.modelService.GetModelByID(ctx, kb.EmbeddingModelID)
	if err != nil {
		return nil, fmt.Errorf("resolve embedding tokenizer model: %w", err)
	}
	if model == nil {
		return nil, fmt.Errorf("resolve embedding tokenizer model: empty model")
	}
	config.Encoding = model.Parameters.EmbeddingParameters.TokenizerEncoding
	config.Model = model.Name
	counter, err := chunker.NewTokenCounter(config)
	if err != nil {
		return nil, fmt.Errorf("create embedding token counter: %w", err)
	}
	return counter, nil
}

func applyIngestionAnalysis(
	effective types.EffectiveProcessConfig,
	analysis *types.IngestionAnalysis,
) types.EffectiveProcessConfig {
	mode := ingestionAppliedMode(analysis)
	effective.IngestionAppliedMode = mode
	effective.IngestionAdvisorApplied = mode == types.IngestionAppliedModeSmart
	analysis.AppliedMode = mode
	if mode == types.IngestionAppliedModeFallback {
		return effective
	}
	effective.ChunkingConfig = applyAdvisorChunking(
		effective.ChunkingConfig, analysis.RecommendedChunking,
	)
	normalized := buildSplitterConfigFromEffective(effective)
	effective.ChunkingConfig.ChunkSize = normalized.ChunkSize
	effective.ChunkingConfig.ChunkOverlap = normalized.ChunkOverlap
	return effective
}

func appliedChunkingRecommendation(
	effective types.EffectiveProcessConfig,
) types.IngestionChunkingRecommendation {
	if effective.IngestionAppliedMode == types.IngestionAppliedModeFallback {
		return ordinaryChunkingRecommendation(effective.ChunkingConfig)
	}
	return chunkingRecommendationFromConfig(effective.ChunkingConfig)
}

func ordinaryChunkingRecommendation(
	config types.ChunkingConfig,
) types.IngestionChunkingRecommendation {
	normalized := normalizeSplitterConfig(config, false)
	strategy := config.Strategy
	if strategy == "" {
		strategy = chunker.StrategyLegacy
	}
	parentSize, childSize := config.ParentChunkSize, config.ChildChunkSize
	if parentSize <= 0 {
		parentSize = 4096
	}
	if childSize <= 0 {
		childSize = 384
	}
	return types.IngestionChunkingRecommendation{
		Strategy: strategy, ChunkSize: normalized.ChunkSize,
		ChunkOverlap:      normalized.ChunkOverlap,
		EnableParentChild: config.EnableParentChild,
		ParentChunkSize:   parentSize, ChildChunkSize: childSize,
		Separators: append([]string(nil), normalized.Separators...),
	}
}

func ingestionChunkingConstraintsFromConfig(
	config types.ChunkingConfig,
) types.IngestionChunkingConstraints {
	return types.IngestionChunkingConstraints{
		TokenLimit: config.TokenLimit,
		Languages:  append([]string(nil), config.Languages...),
	}
}

func ingestionAnalysisFromAdvisorResult(result *types.IngestionAdvisorResult) *types.IngestionAnalysis {
	analysis := cloneIngestionAnalysis(result.Analysis)
	analysis.Candidates = cloneIngestionCandidates(result.Candidates)
	analysis.SelectedCandidateID = result.SelectedCandidateID
	analysis.SelectionReasonCodes = append([]string(nil), result.SelectionReasonCodes...)
	analysis.AgentRun = cloneIngestionAgentRun(result.AgentRun)
	return analysis
}

func resolveIngestionAdvisorEligibility(
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
) (*types.IngestionAdvisorConfig, bool, error) {
	if kb == nil || knowledge == nil {
		return nil, false, nil
	}
	overrides, err := knowledge.ProcessOverrides()
	if err != nil {
		return nil, false, fmt.Errorf("读取文档智能分析配置失败: %w", err)
	}
	if overrides == nil || overrides.IngestionAdvisor == nil ||
		overrides.IngestionAdvisor.Mode != types.IngestionAdvisorModeSmart {
		return nil, false, nil
	}
	if knowledge.Type != "file" || knowledgeFromDataSource(knowledge) {
		return overrides.IngestionAdvisor, false, nil
	}
	eligibleKB := kb.Type == types.KnowledgeBaseTypeDocument || kb.Type == types.KnowledgeBaseTypeWiki
	return overrides.IngestionAdvisor, eligibleKB, nil
}

func knowledgeFromDataSource(knowledge *types.Knowledge) bool {
	metadata, err := knowledge.Metadata.Map()
	if err != nil {
		return false
	}
	_, ok := metadata["datasource_id"]
	return ok
}

func (s *knowledgeService) clearPreviousIngestionAnalysis(
	ctx context.Context,
	knowledge *types.Knowledge,
) error {
	if err := knowledge.SetIngestionAnalysis(nil); err != nil {
		return fmt.Errorf("清除旧文档分析结果失败: %w", err)
	}
	if err := s.repo.UpdateKnowledgeColumn(ctx, knowledge.ID, "metadata", knowledge.Metadata); err != nil {
		return fmt.Errorf("持久化文档分析重试状态失败: %w", err)
	}
	return nil
}

func (s *knowledgeService) failIngestionAdvisor(
	ctx context.Context,
	knowledge *types.Knowledge,
	err error,
) error {
	knowledge.ParseStatus = types.ParseStatusFailed
	knowledge.ErrorMessage = err.Error()
	knowledge.UpdatedAt = time.Now()
	if updateErr := s.repo.UpdateKnowledge(ctx, knowledge); updateErr != nil {
		err = fmt.Errorf("%w; 更新知识失败状态失败: %v", err, updateErr)
	}
	s.failStage(ctx, knowledge.ID, types.StageDocumentAnalysis,
		ingestionAdvisorRunErrorCode(err), err.Error(), err)
	return err
}

func applyAdvisorChunking(
	base types.ChunkingConfig,
	recommendation types.IngestionChunkingRecommendation,
) types.ChunkingConfig {
	base.Strategy = recommendation.Strategy
	base.ChunkSize = recommendation.ChunkSize
	base.ChunkOverlap = recommendation.ChunkOverlap
	base.EnableParentChild = recommendation.EnableParentChild
	base.ParentChunkSize = recommendation.ParentChunkSize
	base.ChildChunkSize = recommendation.ChildChunkSize
	base.Separators = append([]string(nil), recommendation.Separators...)
	return base
}

func chunkingRecommendationFromConfig(config types.ChunkingConfig) types.IngestionChunkingRecommendation {
	return types.IngestionChunkingRecommendation{
		Strategy:          config.Strategy,
		ChunkSize:         config.ChunkSize,
		ChunkOverlap:      config.ChunkOverlap,
		EnableParentChild: config.EnableParentChild,
		ParentChunkSize:   config.ParentChunkSize,
		ChildChunkSize:    config.ChildChunkSize,
		Separators:        append([]string(nil), config.Separators...),
	}
}

func ingestionAnalysisOutput(analysis *types.IngestionAnalysis) types.JSONMap {
	return types.JSONMap{
		"applied_mode":             ingestionAppliedMode(analysis),
		"fallback_reason_codes":    analysis.FallbackReasonCodes,
		"document_kind":            analysis.DocumentKind,
		"confidence":               analysis.Confidence,
		"recommended_content_mode": analysis.RecommendedContentMode,
		"reason_codes":             analysis.ReasonCodes,
		"summary":                  analysis.Summary,
		"recommended_chunking":     analysis.RecommendedChunking,
		"applied_chunking":         analysis.AppliedChunking,
		"model_id":                 analysis.ModelID,
		"candidates":               analysis.Candidates,
		"selected_candidate_id":    analysis.SelectedCandidateID,
		"selection_reason_codes":   analysis.SelectionReasonCodes,
		"agent_run":                analysis.AgentRun,
	}
}
