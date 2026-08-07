package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestIngestionFallbackRequiresThreeStructurallyInvalidCandidates(t *testing.T) {
	session := newFallbackTestSession(ingestionInvalidCandidate("cand_1", "atomic_block_split"))
	_, err := session.submitFallback(validIngestionFallbackInput())
	require.ErrorContains(t, err, "三个已保存候选")

	session = newFallbackTestSession(
		ingestionInvalidCandidate("cand_1", "atomic_block_split"),
		ingestionInvalidCandidate("cand_2", "source_coverage_gap"),
		types.IngestionChunkingCandidate{
			ID: "cand_3", Config: ingestionTestConfig(300), HardValid: true,
		},
	)
	_, err = session.submitFallback(validIngestionFallbackInput())
	require.ErrorContains(t, err, "三个已保存候选")
}

func TestIngestionFallbackRecordsEveryCandidateViolation(t *testing.T) {
	fallback := ingestionTestConfig(320)
	session := newFallbackTestSessionWithConfig(fallback,
		ingestionInvalidCandidate("cand_1", "source_coverage_gap"),
		ingestionInvalidCandidate("cand_2", "atomic_block_split"),
		ingestionInvalidCandidate("cand_3", "source_coverage_gap", "token_limit_exceeded"),
	)

	analysis, err := session.submitFallback(validIngestionFallbackInput())

	require.NoError(t, err)
	require.Equal(t, types.IngestionAppliedModeFallback, analysis.AppliedMode)
	require.Equal(t, fallback, analysis.RecommendedChunking)
	require.Equal(t, []string{
		"all_candidates_structurally_invalid",
		"atomic_block_split",
		"source_coverage_gap",
		"token_limit_exceeded",
	}, analysis.FallbackReasonCodes)
	require.Empty(t, session.selectedCandidateID())
	require.False(t, session.fallbackReady(), "a completed decision cannot be submitted again")
}

func TestIngestionFallbackRejectsUnavailableOriginalConfiguration(t *testing.T) {
	session := newFallbackTestSessionWithConfig(types.IngestionChunkingRecommendation{},
		ingestionInvalidCandidate("cand_1", "atomic_block_split"),
		ingestionInvalidCandidate("cand_2", "source_coverage_gap"),
		ingestionInvalidCandidate("cand_3", "token_limit_exceeded"),
	)

	_, err := session.submitFallback(validIngestionFallbackInput())

	require.ErrorContains(t, err, "知识库原始分块配置不可用")
	require.Nil(t, session.decisionSnapshot())
}

func TestIngestionFallbackAcceptsLegacyCustomSeparators(t *testing.T) {
	fallback := ingestionTestConfig(5000)
	fallback.Separators = []string{"<record-boundary>"}
	session := newFallbackTestSessionWithConfig(fallback,
		ingestionInvalidCandidate("cand_1", "atomic_block_split"),
		ingestionInvalidCandidate("cand_2", "source_coverage_gap"),
		ingestionInvalidCandidate("cand_3", "token_limit_exceeded"),
	)

	analysis, err := session.submitFallback(validIngestionFallbackInput())

	require.NoError(t, err)
	require.Equal(t, fallback, analysis.RecommendedChunking)
}

func TestIngestionFallbackAndSmartSubmissionAreAtomic(t *testing.T) {
	valid := types.IngestionChunkingCandidate{
		ID: "cand_valid", Config: ingestionTestConfig(300), HardValid: true,
		ComparisonFacts: types.IngestionCandidateComparisonFacts{
			ReferenceCandidateID: "cand_valid", SelectionEligible: true,
			ReasonCodes: []string{"highest_total_score"},
		},
	}
	session := newFallbackTestSession(
		valid,
		ingestionInvalidCandidate("cand_2", "atomic_block_split"),
		ingestionInvalidCandidate("cand_3", "source_coverage_gap"),
	)
	var wait sync.WaitGroup
	wait.Add(2)
	errorsByMode := make(chan error, 2)
	go func() {
		defer wait.Done()
		_, err := session.submit(validIngestionDecisionInput("cand_valid"))
		errorsByMode <- err
	}()
	go func() {
		defer wait.Done()
		_, err := session.submitFallback(validIngestionFallbackInput())
		errorsByMode <- err
	}()
	wait.Wait()
	close(errorsByMode)

	successes := 0
	for err := range errorsByMode {
		if err == nil {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, types.IngestionAppliedModeSmart, session.decisionSnapshot().AppliedMode)
}

func TestSubmitIngestionFallbackToolUsesStateDerivedReasons(t *testing.T) {
	session := newFallbackTestSession(
		ingestionInvalidCandidate("cand_1", "atomic_block_split"),
		ingestionInvalidCandidate("cand_2", "source_coverage_gap"),
		ingestionInvalidCandidate("cand_3", "token_limit_exceeded"),
	)
	require.True(t, session.fallbackReady())
	arguments, err := json.Marshal(validIngestionFallbackInput())
	require.NoError(t, err)

	result, err := newSubmitIngestionFallback(session).Execute(context.Background(), arguments)

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, types.IngestionAppliedModeFallback)
	require.NotContains(t, result.Output, "candidate_id")
}

func TestModelIngestionAdvisorTerminatesWithFallbackAfterThreeInvalidCandidates(t *testing.T) {
	request := validIngestionAdvisorRequest()
	request.Content = "| " + strings.Repeat("very-long-header ", 2) + "| value |\n" +
		"| --- | --- |\n" + strings.Repeat("| row-with-long-value | answer text |\n", 6)
	request.ChunkingConstraints.TokenLimit = 100
	request.FallbackChunking = ingestionTestConfig(100)
	fallbackArgs, err := json.Marshal(validIngestionFallbackInput())
	require.NoError(t, err)
	model := &ingestionAdvisorScriptedModel{responses: [][]types.StreamResponse{
		toolResponse("fallback-1", submitIngestionFallbackTool, string(fallbackArgs)),
	}}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)

	result, err := advisor.Analyze(context.Background(), request, interfaces.IngestionAdvisorRuntime{})

	require.NoError(t, err)
	require.Equal(t, types.IngestionAppliedModeFallback, result.Analysis.AppliedMode)
	require.Empty(t, result.SelectedCandidateID)
	require.Len(t, result.Candidates, maxIngestionCandidates)
	for _, candidate := range result.Candidates {
		require.False(t, candidate.HardValid)
	}
	require.Equal(t, "termination_tool", result.AgentRun.StopReason)
	require.Equal(t, 1, result.AgentRun.ActualRounds)
	require.Contains(t, result.AgentRun.AvailableTools, submitIngestionFallbackTool)
	require.NotContains(t, result.AgentRun.AvailableTools, previewIngestionChunkingTool)
	require.NoError(t, ValidateIngestionAdvisorResult(result))
}

func TestValidateIngestionAgentOutcomeRejectsFallbackAfterCoreToolFailure(t *testing.T) {
	session := newFallbackTestSession(
		ingestionInvalidCandidate("cand_1", ingestionViolationAtomicSplit),
		ingestionInvalidCandidate("cand_2", ingestionViolationTableHeaderMissing),
		ingestionInvalidCandidate("cand_3", ingestionViolationTokenLimit),
	)
	_, err := session.submitFallback(validIngestionFallbackInput())
	require.NoError(t, err)
	state := &types.AgentState{
		TerminatedByTool: submitIngestionFallbackTool,
		RoundSteps: []types.AgentStep{{ToolCalls: []types.ToolCall{
			{
				Name: submitIngestionDecisionTool,
				Result: &types.ToolResult{Success: false, Failure: &types.ToolFailure{
					Code: ingestionFailureArgumentsInvalid, Constraint: "json_schema",
				}},
			},
			{Name: submitIngestionFallbackTool, Result: &types.ToolResult{Success: true}},
		}}},
	}

	err = validateIngestionAgentOutcome(state, session)

	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorCandidate, ingestionAdvisorRunErrorCode(err))
	require.Contains(t, err.Error(), ingestionFailureArgumentsInvalid)
}

func newFallbackTestSession(
	candidates ...types.IngestionChunkingCandidate,
) *ingestionAgentSession {
	return newFallbackTestSessionWithConfig(ingestionTestConfig(300), candidates...)
}

func newFallbackTestSessionWithConfig(
	fallback types.IngestionChunkingRecommendation,
	candidates ...types.IngestionChunkingCandidate,
) *ingestionAgentSession {
	session := newIngestionAgentSessionWithFallback(
		"fallback fixture", types.IngestionChunkingConstraints{}, fallback,
	)
	for _, candidate := range candidates {
		session.candidates[candidate.ID] = candidate
	}
	return session
}

func ingestionInvalidCandidate(
	id string,
	violations ...string,
) types.IngestionChunkingCandidate {
	return types.IngestionChunkingCandidate{
		ID: id, Config: ingestionTestConfig(300), HardValid: false,
		Violations: append([]string(nil), violations...),
	}
}

func validIngestionFallbackInput() submitIngestionFallbackInput {
	return submitIngestionFallbackInput{
		DocumentKind: types.IngestionDocumentKindMixedDocument,
		Confidence:   0.7, RecommendedContentMode: types.IngestionContentModeDocument,
		ReasonCodes: []string{"mixed_layout"}, Summary: "使用普通分块配置",
	}
}

func validIngestionDecisionInput(candidateID string) submitIngestionDecisionInput {
	return submitIngestionDecisionInput{
		CandidateID: candidateID, DocumentKind: types.IngestionDocumentKindPolicyManual,
		Confidence: 0.9, RecommendedContentMode: types.IngestionContentModeDocument,
		ReasonCodes: []string{"valid_candidate"}, Summary: "使用有效候选",
	}
}
