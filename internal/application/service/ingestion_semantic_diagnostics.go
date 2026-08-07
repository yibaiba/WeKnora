package service

import (
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

func enrichIngestionAnalysis(
	analysis *types.IngestionAnalysis,
	result *types.IngestionAdvisorResult,
	run ingestionAdvisorRun,
) {
	analysis.CandidateGeneratorVersion = ingestionCandidateGeneratorVersion
	analysis.SemanticDiagnostics = buildIngestionSemanticDiagnostics(result, run)
}

func buildIngestionSemanticDiagnostics(
	result *types.IngestionAdvisorResult,
	run ingestionAdvisorRun,
) types.IngestionSemanticDiagnostics {
	documentDiagnostics := run.Document.Diagnostics
	diagnostics := types.IngestionSemanticDiagnostics{
		SourceFormat:              ingestionSourceFormat(run.Knowledge),
		HintsProvided:             documentDiagnostics.HintsProvided,
		HintsAccepted:             documentDiagnostics.HintsAccepted,
		HintsRejected:             documentDiagnostics.HintsRejected,
		HintRejectionReasonCounts: ingestionHintRejectionCounts(documentDiagnostics),
		StructureViolationCounts:  make(map[string]int),
		TokenCountModeCounts:      make(map[string]int),
		CandidateCount:            len(result.Candidates),
		FallbackApplied:           ingestionAppliedMode(result.Analysis) == types.IngestionAppliedModeFallback,
	}
	if documentDiagnostics.HintsProvided > 0 {
		diagnostics.HintAcceptanceRate = float64(documentDiagnostics.HintsAccepted) /
			float64(documentDiagnostics.HintsProvided)
	}
	populateCandidateDiagnostics(&diagnostics, result)
	return diagnostics
}

func populateCandidateDiagnostics(
	diagnostics *types.IngestionSemanticDiagnostics,
	result *types.IngestionAdvisorResult,
) {
	var highestScore float64
	var selectedScore float64
	selectedFound := false
	for _, candidate := range result.Candidates {
		if candidate.HardValid {
			diagnostics.ValidCandidateCount++
		}
		for _, violation := range candidate.Violations {
			diagnostics.StructureViolationCounts[violation]++
		}
		if candidate.TokenCountMode != "" {
			diagnostics.TokenCountModeCounts[candidate.TokenCountMode]++
		}
		if candidate.Score.Total > highestScore {
			highestScore = candidate.Score.Total
		}
		if candidate.ID == result.SelectedCandidateID {
			selectedFound = true
			selectedScore = candidate.Score.Total
			diagnostics.SelectedAverageChunkTokens = candidate.Lengths.Average
			diagnostics.SelectedContextTokenRatio = candidate.ContextTokenRatio
		}
	}
	diagnostics.NonHighestScoreCandidateUsed = selectedFound && selectedScore < highestScore
}

func ingestionHintRejectionCounts(diagnostics types.SemanticDiagnostics) map[string]int {
	result := make(map[string]int)
	for code, count := range diagnostics.ReasonCodeCounts {
		if strings.HasPrefix(code, "hint_") && count > 0 {
			result[code] = count
		}
	}
	for _, code := range diagnostics.ReasonCodes {
		if strings.HasPrefix(code, "hint_") && result[code] == 0 {
			result[code] = 1
		}
	}
	return result
}

func ingestionSourceFormat(knowledge *types.Knowledge) string {
	if knowledge == nil {
		return "unknown"
	}
	format := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(knowledge.FileType)), ".")
	if format == "" || strings.Contains(format, "/") {
		format = strings.TrimPrefix(strings.ToLower(filepath.Ext(knowledge.FileName)), ".")
	}
	switch format {
	case "pdf", "docx", "pptx", "md", "markdown", "html", "htm", "txt":
		return format
	default:
		return "other"
	}
}

func applyIngestionRolloutResult(
	effective types.EffectiveProcessConfig,
	analysis *types.IngestionAnalysis,
	decision ingestionRolloutDecision,
) types.EffectiveProcessConfig {
	if decision.applyV2 {
		return applyIngestionAnalysis(effective, analysis)
	}
	baseline := ordinaryChunkingRecommendation(effective.ChunkingConfig)
	v2Mode := ingestionAppliedMode(analysis)
	analysis.AppliedMode = types.IngestionAppliedModeShadow
	analysis.AppliedChunking = cloneChunkingRecommendation(baseline)
	analysis.ShadowComparison = &types.IngestionShadowComparison{
		BaselineChunking:      cloneChunkingRecommendation(baseline),
		V2RecommendedChunking: cloneChunkingRecommendation(analysis.RecommendedChunking),
		V2AppliedMode:         v2Mode,
		V2SelectedCandidateID: analysis.SelectedCandidateID,
		ChunkingChanged:       !reflect.DeepEqual(baseline, analysis.RecommendedChunking),
		ReasonCodes:           []string{"shadow_v2_not_applied"},
	}
	return effective
}
