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
	cloned.RecommendedChunking = cloneChunkingRecommendation(analysis.RecommendedChunking)
	cloned.AppliedChunking = cloneChunkingRecommendation(analysis.AppliedChunking)
	cloned.Candidates = cloneIngestionCandidates(analysis.Candidates)
	cloned.SelectionReasonCodes = append([]string(nil), analysis.SelectionReasonCodes...)
	cloned.AgentRun = cloneIngestionAgentRun(analysis.AgentRun)
	return &cloned
}
