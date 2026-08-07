package service

import "github.com/Tencent/WeKnora/internal/types"

func cloneIngestionCandidates(
	candidates []types.IngestionChunkingCandidate,
) []types.IngestionChunkingCandidate {
	result := make([]types.IngestionChunkingCandidate, len(candidates))
	for index, candidate := range candidates {
		result[index] = cloneIngestionCandidate(candidate)
	}
	return result
}

func cloneIngestionAgentRun(run types.IngestionAgentRun) types.IngestionAgentRun {
	run.AvailableTools = append([]string(nil), run.AvailableTools...)
	run.Warnings = append([]types.IngestionAgentWarning(nil), run.Warnings...)
	run.Steps = append([]types.IngestionAgentStep(nil), run.Steps...)
	return run
}

func cloneIngestionAnalysis(analysis *types.IngestionAnalysis) *types.IngestionAnalysis {
	if analysis == nil {
		return nil
	}
	cloned := *analysis
	cloned.ReasonCodes = append([]string(nil), analysis.ReasonCodes...)
	cloned.FallbackReasonCodes = append([]string(nil), analysis.FallbackReasonCodes...)
	cloned.RecommendedChunking = cloneChunkingRecommendation(analysis.RecommendedChunking)
	cloned.AppliedChunking = cloneChunkingRecommendation(analysis.AppliedChunking)
	cloned.Candidates = cloneIngestionCandidates(analysis.Candidates)
	cloned.SelectionReasonCodes = append([]string(nil), analysis.SelectionReasonCodes...)
	cloned.AgentRun = cloneIngestionAgentRun(analysis.AgentRun)
	cloned.PackingPolicy = cloneSemanticPackingPolicy(analysis.PackingPolicy)
	cloned.SemanticDiagnostics = cloneIngestionSemanticDiagnostics(analysis.SemanticDiagnostics)
	cloned.ShadowComparison = cloneIngestionShadowComparison(analysis.ShadowComparison)
	return &cloned
}

func cloneIngestionSemanticDiagnostics(
	diagnostics types.IngestionSemanticDiagnostics,
) types.IngestionSemanticDiagnostics {
	diagnostics.HintRejectionReasonCounts = cloneStringIntMap(diagnostics.HintRejectionReasonCounts)
	diagnostics.StructureViolationCounts = cloneStringIntMap(diagnostics.StructureViolationCounts)
	diagnostics.TokenCountModeCounts = cloneStringIntMap(diagnostics.TokenCountModeCounts)
	return diagnostics
}

func cloneIngestionShadowComparison(
	comparison *types.IngestionShadowComparison,
) *types.IngestionShadowComparison {
	if comparison == nil {
		return nil
	}
	cloned := *comparison
	cloned.BaselineChunking = cloneChunkingRecommendation(comparison.BaselineChunking)
	cloned.V2RecommendedChunking = cloneChunkingRecommendation(comparison.V2RecommendedChunking)
	cloned.ReasonCodes = append([]string(nil), comparison.ReasonCodes...)
	return &cloned
}

func cloneStringIntMap(source map[string]int) map[string]int {
	if source == nil {
		return nil
	}
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
