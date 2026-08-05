package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestReduceIngestionDocumentSkipsModelForSingleEvidence(t *testing.T) {
	evidence := validIngestionDocumentEvidence("single")

	result, err := reduceIngestionDocument(context.Background(), ingestionDocumentReduceRequest{
		Evidence: []ingestionDocumentEvidence{evidence}, CoveredCharacters: 42,
	})

	require.NoError(t, err)
	require.Equal(t, evidence, result)
}

func TestReduceIngestionDocumentRecursesWithinBudgetAndPreservesOrder(t *testing.T) {
	input := make([]ingestionDocumentEvidence, 7)
	for index := range input {
		input[index] = largeIngestionDocumentEvidence(index)
		require.NoError(t, validateIngestionDocumentEvidence(input[index]))
	}
	var responseIndex atomic.Int32
	model := &ingestionMapModelStub{response: func(_ context.Context, _ []chat.Message) (*types.ChatResponse, error) {
		index := responseIndex.Add(1)
		return mapEvidenceResponse(fmt.Sprintf("reduced-%02d", index)), nil
	}}
	var progress []types.IngestionDocumentAnalysisProgress

	result, err := reduceIngestionDocument(context.Background(), ingestionDocumentReduceRequest{
		Model: model, Evidence: input, CoveredCharacters: 54321,
		Progress: func(event types.IngestionDocumentAnalysisProgress) {
			progress = append(progress, event)
		},
	})

	require.NoError(t, err)
	require.NotEmpty(t, result.Summary)
	require.Greater(t, len(progress), 1)
	terminalProgress := terminalAnalysisProgress(progress)
	for index, event := range progressWithoutDuration(terminalProgress) {
		require.Equal(t, index+1, event.Level)
		require.Equal(t, ingestionAnalysisProgressSucceeded, event.Status)
		require.Equal(t, event.UnitCount, event.Completed)
		require.Equal(t, 54321, event.CoveredCharacters)
		require.False(t, event.Failed)
	}
	calls := model.callSnapshot()
	require.Len(t, calls, int(responseIndex.Load()))
	firstLevelCount := terminalProgress[0].UnitCount
	var firstLevelSummaries []string
	for index, call := range calls {
		require.True(t, call.redacted)
		require.LessOrEqual(t, utf8.RuneCountInString(call.messages[1].Content), ingestionDocumentReduceInputMaxRunes)
		if index < firstLevelCount {
			batch := decodeEvidenceBatchForTest(t, call.messages[1].Content)
			for _, evidence := range batch.Evidence {
				firstLevelSummaries = append(firstLevelSummaries, evidence.Summary)
			}
		}
	}
	require.Equal(t, evidenceSummaries(input), firstLevelSummaries)
}

func TestFullDocumentAnalysisCovers15032RunesBeforeReduction(t *testing.T) {
	content := strings.Repeat("文", 15032)
	units, err := splitIngestionDocumentAnalysisUnits(content)
	require.NoError(t, err)
	var mapCalls atomic.Int32
	var reduceCalls atomic.Int32
	model := &ingestionMapModelStub{response: func(_ context.Context, messages []chat.Message) (*types.ChatResponse, error) {
		if strings.Contains(messages[0].Content, "Map 阶段") {
			mapCalls.Add(1)
			return mapEvidenceResponse("mapped"), nil
		}
		reduceCalls.Add(1)
		return mapEvidenceResponse("reduced"), nil
	}}

	mapped, err := mapIngestionDocument(context.Background(), ingestionDocumentMapRequest{
		Model: model, Units: units,
	})
	require.NoError(t, err)
	result, err := reduceIngestionDocument(context.Background(), ingestionDocumentReduceRequest{
		Model: model, Evidence: mapped, CoveredCharacters: utf8.RuneCountInString(content),
	})

	require.NoError(t, err)
	require.Equal(t, "reduced", result.Summary)
	require.Equal(t, int32(len(units)), mapCalls.Load())
	require.Equal(t, int32(1), reduceCalls.Load())
	require.Equal(t, content, joinIngestionDocumentUnits(units))
}

func TestReduceIngestionDocumentFailsOnInvalidResponseWithoutPartialResult(t *testing.T) {
	input := []ingestionDocumentEvidence{
		validIngestionDocumentEvidence("first"), validIngestionDocumentEvidence("second"),
	}
	model := &ingestionMapModelStub{response: func(context.Context, []chat.Message) (*types.ChatResponse, error) {
		return &types.ChatResponse{Content: `{"summary":"incomplete"}`}, nil
	}}
	var progress []types.IngestionDocumentAnalysisProgress

	result, err := reduceIngestionDocument(context.Background(), ingestionDocumentReduceRequest{
		Model: model, Evidence: input, CoveredCharacters: 100,
		Progress: func(event types.IngestionDocumentAnalysisProgress) {
			progress = append(progress, event)
		},
	})

	require.Equal(t, ingestionDocumentEvidence{}, result)
	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorDocumentAnalysis, ingestionAdvisorRunErrorCode(err))
	require.Len(t, progress, 2)
	require.Equal(t, ingestionAnalysisProgressRunning, progress[0].Status)
	require.Equal(t, ingestionAnalysisProgressFailed, progress[1].Status)
	require.True(t, progress[1].Failed)
	require.Zero(t, progress[1].Completed)
}

func TestGroupIngestionDocumentEvidenceRejectsOversizedSingleItem(t *testing.T) {
	oversized := validIngestionDocumentEvidence(strings.Repeat("x", ingestionDocumentReduceInputMaxRunes))

	groups, err := groupIngestionDocumentEvidence([]ingestionDocumentEvidence{oversized}, 1)

	require.Nil(t, groups)
	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorDocumentAnalysis, ingestionAdvisorRunErrorCode(err))
}

func validIngestionDocumentEvidence(summary string) ingestionDocumentEvidence {
	return ingestionDocumentEvidence{
		Summary:                summary,
		DocumentKindCandidates: []string{types.IngestionDocumentKindReport},
		ContentModeCandidates:  []string{types.IngestionContentModeDocument},
		StructureSignals:       []string{"sectioned"},
		ChunkingSignals:        []string{"prefer headings"},
	}
}

func largeIngestionDocumentEvidence(index int) ingestionDocumentEvidence {
	evidence := validIngestionDocumentEvidence(fmt.Sprintf("source-%02d-%s", index, strings.Repeat("概", 950)))
	evidence.StructureSignals = make([]string, ingestionDocumentSignalLimit)
	evidence.ChunkingSignals = make([]string, ingestionDocumentSignalLimit)
	for signal := 0; signal < ingestionDocumentSignalLimit; signal++ {
		evidence.StructureSignals[signal] = fmt.Sprintf("structure-%d-%s", signal, strings.Repeat("构", 245))
		evidence.ChunkingSignals[signal] = fmt.Sprintf("chunking-%d-%s", signal, strings.Repeat("切", 245))
	}
	return evidence
}

func decodeEvidenceBatchForTest(t *testing.T, payload string) ingestionDocumentEvidenceBatch {
	t.Helper()
	var batch ingestionDocumentEvidenceBatch
	require.NoError(t, json.Unmarshal([]byte(payload), &batch))
	return batch
}

func evidenceSummaries(evidence []ingestionDocumentEvidence) []string {
	result := make([]string, len(evidence))
	for index := range evidence {
		result[index] = evidence[index].Summary
	}
	return result
}

func terminalAnalysisProgress(
	events []types.IngestionDocumentAnalysisProgress,
) []types.IngestionDocumentAnalysisProgress {
	result := make([]types.IngestionDocumentAnalysisProgress, 0, len(events))
	for _, event := range events {
		if event.Status != ingestionAnalysisProgressRunning {
			result = append(result, event)
		}
	}
	return result
}
