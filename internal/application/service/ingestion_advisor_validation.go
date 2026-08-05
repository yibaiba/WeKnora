package service

import (
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

var (
	allowedChunkingStrategyValues   = [...]string{"auto", "heading", "heuristic", "legacy", "recursive"}
	allowedIngestionSeparatorValues = [...]string{
		"\n\n", "\n", "。", "！", "？", "；", ";", " ",
	}
	allowedDocumentKinds = map[string]struct{}{
		types.IngestionDocumentKindPolicyManual: {}, types.IngestionDocumentKindFAQ: {},
		types.IngestionDocumentKindTabularData: {}, types.IngestionDocumentKindReport: {},
		types.IngestionDocumentKindMeetingNotes: {}, types.IngestionDocumentKindPresentation: {},
		types.IngestionDocumentKindShortArticle: {}, types.IngestionDocumentKindMixedDocument: {},
	}
	allowedContentModes = map[string]struct{}{
		types.IngestionContentModeDocument: {}, types.IngestionContentModeFAQCandidate: {},
		types.IngestionContentModeWikiCandidate: {},
	}
	allowedChunkingStrategies  = ingestionAllowedValues(allowedChunkingStrategyValues[:])
	allowedIngestionSeparators = ingestionAllowedValues(allowedIngestionSeparatorValues[:])
)

const (
	minimumAdvisorChunkSize  = 100
	maximumAdvisorChunkSize  = 4000
	maximumAdvisorOverlap    = 500
	minimumAdvisorParentSize = 512
	maximumAdvisorParentSize = 8192
	minimumAdvisorChildSize  = 64
	maximumAdvisorChildSize  = 2048
)

func ingestionAllowedValues(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

// ValidateIngestionAdvisorConfig rejects unsupported modes and prompt
// versions without normalizing or mutating the upload payload.
func ValidateIngestionAdvisorConfig(config *types.IngestionAdvisorConfig) error {
	if config == nil {
		return nil
	}
	if config.Mode != types.IngestionAdvisorModeSmart && config.Mode != types.IngestionAdvisorModeOff {
		return fmt.Errorf("ingestion_advisor.mode %q 不受支持", config.Mode)
	}
	return nil
}

// ValidateIngestionAnalysis protects the pipeline from both remote model
// responses and injected advisor implementations.
func ValidateIngestionAnalysis(analysis *types.IngestionAnalysis) error {
	return validateIngestionAnalysisWithConstraints(analysis, types.IngestionChunkingConstraints{})
}

func validateIngestionAnalysisWithConstraints(
	analysis *types.IngestionAnalysis,
	constraints types.IngestionChunkingConstraints,
) error {
	if analysis == nil {
		return fmt.Errorf("文档分析结果为空")
	}
	if _, ok := allowedDocumentKinds[analysis.DocumentKind]; !ok {
		return fmt.Errorf("document_kind %q 不受支持", analysis.DocumentKind)
	}
	if math.IsNaN(analysis.Confidence) || math.IsInf(analysis.Confidence, 0) ||
		analysis.Confidence < 0 || analysis.Confidence > 1 {
		return fmt.Errorf("confidence 必须在 0 到 1 之间")
	}
	if _, ok := allowedContentModes[analysis.RecommendedContentMode]; !ok {
		return fmt.Errorf("recommended_content_mode %q 不受支持", analysis.RecommendedContentMode)
	}
	if len(analysis.ReasonCodes) == 0 || strings.TrimSpace(analysis.Summary) == "" {
		return fmt.Errorf("reason_codes 和 summary 不能为空")
	}
	for _, code := range analysis.ReasonCodes {
		if strings.TrimSpace(code) == "" {
			return fmt.Errorf("reason_codes 不能包含空值")
		}
	}
	return validateIngestionChunkingRecommendation(analysis.RecommendedChunking, constraints)
}

func ValidateIngestionAdvisorResult(result *types.IngestionAdvisorResult) error {
	return validateIngestionAdvisorResultWithConstraints(result, types.IngestionChunkingConstraints{})
}

func validateIngestionAdvisorResultWithConstraints(
	result *types.IngestionAdvisorResult,
	constraints types.IngestionChunkingConstraints,
) error {
	if result == nil {
		return fmt.Errorf("文档智能分析未返回结果")
	}
	if err := validateIngestionAnalysisWithConstraints(result.Analysis, constraints); err != nil {
		return err
	}
	if result.SelectedCandidateID == "" || len(result.SelectionReasonCodes) == 0 {
		return fmt.Errorf("selected_candidate_id 和 selection_reason_codes 不能为空")
	}
	for _, candidate := range result.Candidates {
		if candidate.ID != result.SelectedCandidateID {
			continue
		}
		if !candidate.HardValid {
			return fmt.Errorf("选中候选 %q 未通过硬校验", candidate.ID)
		}
		if !reflect.DeepEqual(candidate.Config, result.Analysis.RecommendedChunking) {
			return fmt.Errorf("选中候选与 recommended_chunking 不一致")
		}
		return nil
	}
	return fmt.Errorf("选中候选 %q 不存在", result.SelectedCandidateID)
}

func ValidateIngestionChunkingRecommendation(value types.IngestionChunkingRecommendation) error {
	return validateIngestionChunkingRecommendation(value, types.IngestionChunkingConstraints{})
}

func validateIngestionChunkingRecommendation(
	value types.IngestionChunkingRecommendation,
	constraints types.IngestionChunkingConstraints,
) error {
	if _, ok := allowedChunkingStrategies[value.Strategy]; !ok {
		return newIngestionToolError(
			ingestionFailureStrategyInvalid, "strategy", "supported_strategy",
			fmt.Sprintf("strategy %q 不受支持", value.Strategy),
		)
	}
	minimumChunkSize, maximumChunkSize := ingestionChunkSizeBounds(constraints)
	if value.ChunkSize < minimumChunkSize || value.ChunkSize > maximumChunkSize {
		return newIngestionToolError(
			ingestionFailureChunkSizeInvalid, "chunk_size", "effective_chunk_size_range",
			fmt.Sprintf("chunk_size 必须在 %d 到 %d 之间", minimumChunkSize, maximumChunkSize),
		)
	}
	maxOverlap := min(maximumAdvisorOverlap, value.ChunkSize/2)
	if value.ChunkOverlap < 0 || value.ChunkOverlap > maxOverlap {
		return newIngestionToolError(
			ingestionFailureOverlapInvalid, "chunk_overlap", "at_most_half_chunk_size",
			fmt.Sprintf("chunk_overlap 必须在 0 到 %d 之间", maxOverlap),
		)
	}
	if value.ParentChunkSize < minimumAdvisorParentSize || value.ParentChunkSize > maximumAdvisorParentSize {
		return newIngestionToolError(
			ingestionFailureParentSizeInvalid, "parent_chunk_size", "parent_chunk_size_range",
			fmt.Sprintf("parent_chunk_size 必须在 %d 到 %d 之间", minimumAdvisorParentSize, maximumAdvisorParentSize),
		)
	}
	if value.ChildChunkSize < minimumAdvisorChildSize || value.ChildChunkSize > maximumAdvisorChildSize {
		return newIngestionToolError(
			ingestionFailureChildSizeInvalid, "child_chunk_size", "child_chunk_size_range",
			fmt.Sprintf("child_chunk_size 必须在 %d 到 %d 之间", minimumAdvisorChildSize, maximumAdvisorChildSize),
		)
	}
	if value.ChildChunkSize > value.ParentChunkSize {
		return newIngestionToolError(
			ingestionFailureParentChildInvalid, "child_chunk_size", "not_greater_than_parent_chunk_size",
			"child_chunk_size 不能大于 parent_chunk_size",
		)
	}
	if len(value.Separators) == 0 {
		return newIngestionToolError(
			ingestionFailureSeparatorsInvalid, "separators", "non_empty_supported_separators",
			"separators 不能为空",
		)
	}
	for _, separator := range value.Separators {
		if _, ok := allowedIngestionSeparators[separator]; !ok {
			return newIngestionToolError(
				ingestionFailureSeparatorsInvalid, "separators", "non_empty_supported_separators",
				fmt.Sprintf("separator %q 不受支持", separator),
			)
		}
	}
	return nil
}

func ingestionChunkSizeBounds(constraints types.IngestionChunkingConstraints) (int, int) {
	if constraints.TokenLimit <= 0 {
		return minimumAdvisorChunkSize, maximumAdvisorChunkSize
	}
	config := normalizeSplitterConfig(types.ChunkingConfig{
		ChunkSize:  maximumAdvisorChunkSize,
		TokenLimit: constraints.TokenLimit,
		Languages:  append([]string(nil), constraints.Languages...),
	}, true)
	maximum := min(maximumAdvisorChunkSize, config.ChunkSize)
	return min(minimumAdvisorChunkSize, maximum), maximum
}

func cloneChunkingRecommendation(value types.IngestionChunkingRecommendation) types.IngestionChunkingRecommendation {
	value.Separators = append([]string(nil), value.Separators...)
	return value
}
