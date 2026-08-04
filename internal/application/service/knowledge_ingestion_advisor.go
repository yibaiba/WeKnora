package service

import (
	"context"
	"fmt"
	"time"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

type ingestionAdvisorRun struct {
	Knowledge *types.Knowledge
	KB        *types.KnowledgeBase
	Content   string
	Effective types.EffectiveProcessConfig
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

	promptVersion := ingestionPromptVersion(config)
	s.beginStage(ctx, run.Knowledge.ID, types.StageDocumentAnalysis, types.JSONMap{
		"model_id":       run.KB.SummaryModelID,
		"prompt_version": promptVersion,
		"text_length":    len([]rune(run.Content)),
	})
	if err := s.clearPreviousIngestionAnalysis(ctx, run.Knowledge); err != nil {
		return run.Effective, s.failIngestionAdvisor(ctx, run.Knowledge, err)
	}
	analysis, err := s.analyzeIngestionContent(ctx, run, promptVersion, config)
	if err != nil {
		return run.Effective, s.failIngestionAdvisor(ctx, run.Knowledge, err)
	}

	run.Effective.ChunkingConfig = applyAdvisorChunking(run.Effective.ChunkingConfig, analysis.RecommendedChunking)
	run.Effective.IngestionAdvisorApplied = true
	normalized := buildSplitterConfigFromEffective(run.Effective)
	run.Effective.ChunkingConfig.ChunkSize = normalized.ChunkSize
	run.Effective.ChunkingConfig.ChunkOverlap = normalized.ChunkOverlap
	analysis = cloneIngestionAnalysis(analysis)
	analysis.AppliedChunking = chunkingRecommendationFromConfig(run.Effective.ChunkingConfig)
	analysis.ModelID = run.KB.SummaryModelID
	analysis.PromptVersion = promptVersion
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

func (s *knowledgeService) analyzeIngestionContent(
	ctx context.Context,
	run ingestionAdvisorRun,
	promptVersion string,
	config *types.IngestionAdvisorConfig,
) (*types.IngestionAnalysis, error) {
	if s.ingestionAdvisor == nil {
		return nil, fmt.Errorf("文档智能分析服务未配置")
	}
	result, err := s.ingestionAdvisor.Analyze(ctx, types.IngestionAdvisorRequest{
		Content:           run.Content,
		KnowledgeID:       run.Knowledge.ID,
		KnowledgeBaseID:   run.KB.ID,
		KnowledgeBaseName: run.KB.Name,
		KnowledgeBaseType: run.KB.Type,
		TenantID:          run.KB.TenantID,
		VectorEnabled:     run.KB.IsVectorEnabled(),
		KeywordEnabled:    run.KB.IsKeywordEnabled(),
		GraphEnabled:      run.KB.IsGraphEnabled(),
		WikiEnabled:       run.KB.IsWikiEnabled(),
		ModelID:           run.KB.SummaryModelID,
		PromptVersion:     promptVersion,
		AllowWebAccess:    config.AllowWebAccess,
		AllowReadOnlyMCP:  config.AllowReadOnlyMCP,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("文档智能分析未返回结果")
	}
	analysis := result.Analysis
	if err := ValidateIngestionAnalysis(analysis); err != nil {
		return nil, err
	}
	return analysis, nil
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
		werrors.ErrCodeDocumentAnalysisFailed, err.Error(), err)
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

func cloneIngestionAnalysis(analysis *types.IngestionAnalysis) *types.IngestionAnalysis {
	cloned := *analysis
	cloned.ReasonCodes = append([]string(nil), analysis.ReasonCodes...)
	cloned.RecommendedChunking = cloneChunkingRecommendation(analysis.RecommendedChunking)
	cloned.AppliedChunking = cloneChunkingRecommendation(analysis.AppliedChunking)
	return &cloned
}

func ingestionAnalysisOutput(analysis *types.IngestionAnalysis) types.JSONMap {
	return types.JSONMap{
		"document_kind":            analysis.DocumentKind,
		"confidence":               analysis.Confidence,
		"recommended_content_mode": analysis.RecommendedContentMode,
		"reason_codes":             analysis.ReasonCodes,
		"summary":                  analysis.Summary,
		"recommended_chunking":     analysis.RecommendedChunking,
		"applied_chunking":         analysis.AppliedChunking,
		"model_id":                 analysis.ModelID,
		"prompt_version":           analysis.PromptVersion,
	}
}
