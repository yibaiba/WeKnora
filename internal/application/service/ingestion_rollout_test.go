package service

import (
	"testing"

	appconfig "github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestResolveIngestionRolloutModeMatrix(t *testing.T) {
	zero, one := 0.0, 1.0
	tests := []struct {
		name    string
		config  *appconfig.Config
		runV2   bool
		applyV2 bool
	}{
		{name: "off", config: ingestionRolloutConfig(appconfig.SemanticChunkingV2ModeOff, &one)},
		{name: "shadow excluded", config: ingestionRolloutConfig(appconfig.SemanticChunkingV2ModeShadow, &zero)},
		{name: "shadow sampled", config: ingestionRolloutConfig(appconfig.SemanticChunkingV2ModeShadow, &one), runV2: true},
		{name: "on", config: ingestionRolloutConfig(appconfig.SemanticChunkingV2ModeOn, &zero), runV2: true, applyV2: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := resolveIngestionRollout(test.config, 42)
			require.NoError(t, err)
			require.Equal(t, test.runV2, decision.runV2)
			require.Equal(t, test.applyV2, decision.applyV2)
		})
	}
}

func TestIngestionTenantShadowSamplingIsStableAndHasBoundaries(t *testing.T) {
	require.False(t, ingestionTenantInShadowSample(42, 0))
	require.True(t, ingestionTenantInShadowSample(42, 1))

	sampled, excluded := uint64(0), uint64(0)
	for tenantID := uint64(1); tenantID <= 10_000; tenantID++ {
		if ingestionTenantInShadowSample(tenantID, 0.10) && sampled == 0 {
			sampled = tenantID
		}
		if !ingestionTenantInShadowSample(tenantID, 0.10) && excluded == 0 {
			excluded = tenantID
		}
		if sampled != 0 && excluded != 0 {
			break
		}
	}
	require.NotZero(t, sampled)
	require.NotZero(t, excluded)
	for range 20 {
		require.True(t, ingestionTenantInShadowSample(sampled, 0.10))
		require.False(t, ingestionTenantInShadowSample(excluded, 0.10))
	}
}

func TestBuildIngestionSemanticDiagnosticsAggregatesCandidateFacts(t *testing.T) {
	result := &types.IngestionAdvisorResult{
		Analysis: &types.IngestionAnalysis{AppliedMode: types.IngestionAppliedModeSmart},
		Candidates: []types.IngestionChunkingCandidate{
			{
				ID: "selected", HardValid: true, TokenCountMode: chunker.TokenCountModeExact,
				Score:   types.IngestionCandidateScore{Total: 80},
				Lengths: types.IngestionLengthDistribution{Average: 200}, ContextTokenRatio: 0.10,
				Violations: []string{"context_budget_exceeded"},
			},
			{
				ID: "highest", TokenCountMode: chunker.TokenCountModeConservative,
				Score:      types.IngestionCandidateScore{Total: 84},
				Violations: []string{"context_budget_exceeded", "atomic_block_split"},
			},
		},
		SelectedCandidateID: "selected",
	}
	run := ingestionAdvisorRun{
		Knowledge: &types.Knowledge{FileName: "guide.docx"},
		Document: chunker.SemanticDocument{Diagnostics: types.SemanticDiagnostics{
			HintsProvided: 3, HintsAccepted: 1, HintsRejected: 2,
			ReasonCodes:      []string{"hint_source_unmatched"},
			ReasonCodeCounts: map[string]int{"hint_source_unmatched": 2},
		}},
	}

	diagnostics := buildIngestionSemanticDiagnostics(result, run)

	require.Equal(t, "docx", diagnostics.SourceFormat)
	require.InDelta(t, 1.0/3.0, diagnostics.HintAcceptanceRate, 0.000001)
	require.Equal(t, map[string]int{"hint_source_unmatched": 2}, diagnostics.HintRejectionReasonCounts)
	require.Equal(t, map[string]int{
		"context_budget_exceeded": 2, "atomic_block_split": 1,
	}, diagnostics.StructureViolationCounts)
	require.Equal(t, map[string]int{
		chunker.TokenCountModeExact: 1, chunker.TokenCountModeConservative: 1,
	}, diagnostics.TokenCountModeCounts)
	require.Equal(t, 2, diagnostics.CandidateCount)
	require.Equal(t, 1, diagnostics.ValidCandidateCount)
	require.True(t, diagnostics.NonHighestScoreCandidateUsed)
	require.False(t, diagnostics.FallbackApplied)
	require.Equal(t, 200.0, diagnostics.SelectedAverageChunkTokens)
	require.Equal(t, 0.10, diagnostics.SelectedContextTokenRatio)
}

func ingestionRolloutConfig(mode string, rate *float64) *appconfig.Config {
	return &appconfig.Config{KnowledgeBase: &appconfig.KnowledgeBaseConfig{
		SemanticChunkingV2Mode: mode, ShadowSampleRate: rate,
	}}
}
