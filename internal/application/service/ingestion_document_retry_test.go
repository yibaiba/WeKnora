package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type ingestionRetryStep struct {
	response *types.ChatResponse
	err      error
}

type ingestionRetryModel struct {
	mu    sync.Mutex
	steps []ingestionRetryStep
	calls []ingestionMapCall
}

func (m *ingestionRetryModel) Chat(
	ctx context.Context,
	messages []chat.Message,
	options *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, ingestionMapCall{
		messages: append([]chat.Message(nil), messages...),
		options:  *options,
		redacted: types.LLMTracePayloadsRedacted(ctx),
	})
	if len(m.steps) == 0 {
		return nil, errors.New("retry test exhausted scripted responses")
	}
	step := m.steps[0]
	m.steps = m.steps[1:]
	return step.response, step.err
}

func (m *ingestionRetryModel) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("unexpected streaming call")
}

func (m *ingestionRetryModel) GetModelName() string { return "retry-model" }
func (m *ingestionRetryModel) GetModelID() string   { return "retry-model-id" }

func (m *ingestionRetryModel) callSnapshot() []ingestionMapCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ingestionMapCall(nil), m.calls...)
}

func TestCallIngestionDocumentAnalysisRetriesOnlyTransientFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "429", err: chat.NewProviderError(chat.ProviderFailureRateLimited, http.StatusTooManyRequests, "")},
		{name: "502", err: chat.NewProviderError(chat.ProviderFailureUnavailable, http.StatusBadGateway, "")},
		{name: "503", err: chat.NewProviderError(chat.ProviderFailureUnavailable, http.StatusServiceUnavailable, "")},
		{name: "504", err: chat.NewProviderError(chat.ProviderFailureUnavailable, http.StatusGatewayTimeout, "")},
		{name: "transport", err: chat.NewProviderError(chat.ProviderFailureTransport, 0, "")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := &ingestionRetryModel{steps: []ingestionRetryStep{
				{err: test.err}, {response: mapEvidenceResponse("ok")},
			}}
			var waits []time.Duration
			result, err := callIngestionDocumentAnalysis(
				context.Background(), retryTestCall(model), recordingRetryPolicy(&waits),
			)

			require.NoError(t, err)
			require.Equal(t, 2, result.Attempts)
			require.Equal(t, []time.Duration{time.Second}, waits)
			calls := model.callSnapshot()
			require.Len(t, calls, 2)
			require.Equal(t, calls[0].messages, calls[1].messages)
			require.Equal(t, calls[0].options, calls[1].options)
		})
	}
}

func TestCallIngestionDocumentAnalysisUsesRetryAfterAndCapsIt(t *testing.T) {
	retryAfter := 45 * time.Second
	rateLimit := chat.NewProviderErrorWithDetails(chat.ProviderFailureDetails{
		Kind: chat.ProviderFailureRateLimited, StatusCode: http.StatusTooManyRequests,
		RetryAfter: &retryAfter,
	})
	model := &ingestionRetryModel{steps: []ingestionRetryStep{
		{err: rateLimit}, {response: mapEvidenceResponse("ok")},
	}}
	var waits []time.Duration

	result, err := callIngestionDocumentAnalysis(
		context.Background(), retryTestCall(model), recordingRetryPolicy(&waits),
	)

	require.NoError(t, err)
	require.Equal(t, 2, result.Attempts)
	require.Equal(t, []time.Duration{30 * time.Second}, waits)
}

func TestCallIngestionDocumentAnalysisStopsAfterThirdFailure(t *testing.T) {
	transient := chat.NewProviderError(chat.ProviderFailureUnavailable, http.StatusServiceUnavailable, "")
	model := &ingestionRetryModel{steps: []ingestionRetryStep{
		{err: transient}, {err: transient}, {err: transient},
	}}
	var waits []time.Duration

	result, err := callIngestionDocumentAnalysis(
		context.Background(), retryTestCall(model), recordingRetryPolicy(&waits),
	)

	require.Error(t, err)
	require.Equal(t, ingestionDocumentAnalysisMaximumAttempts, result.Attempts)
	require.Equal(t, []time.Duration{time.Second, 2 * time.Second}, waits)
	require.Len(t, model.callSnapshot(), ingestionDocumentAnalysisMaximumAttempts)
}

func TestCallIngestionDocumentAnalysisDoesNotRetryPermanentFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "400", err: chat.NewProviderError(chat.ProviderFailureRequestInvalid, http.StatusBadRequest, "")},
		{name: "401", err: chat.NewProviderError(chat.ProviderFailureAuthentication, http.StatusUnauthorized, "")},
		{name: "403", err: chat.NewProviderError(chat.ProviderFailureAuthentication, http.StatusForbidden, "")},
		{name: "schema", err: chat.NewProviderError(chat.ProviderFailureRequestInvalid, http.StatusBadRequest, "response_format")},
		{name: "provider timeout", err: chat.NewProviderError(chat.ProviderFailureTimeout, 0, "")},
		{name: "provider unknown", err: chat.NewProviderError(chat.ProviderFailureUnknown, 0, "")},
		{name: "untyped", err: errors.New("permanent private failure")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := &ingestionRetryModel{steps: []ingestionRetryStep{{err: test.err}}}
			var waits []time.Duration
			result, err := callIngestionDocumentAnalysis(
				context.Background(), retryTestCall(model), recordingRetryPolicy(&waits),
			)

			require.Error(t, err)
			require.Equal(t, 1, result.Attempts)
			require.Empty(t, waits)
			require.Len(t, model.callSnapshot(), 1)
		})
	}
}

func TestCallIngestionDocumentAnalysisCancelsRetryWait(t *testing.T) {
	transient := chat.NewProviderError(chat.ProviderFailureUnavailable, http.StatusServiceUnavailable, "")
	model := &ingestionRetryModel{steps: []ingestionRetryStep{{err: transient}}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()

	result, err := callIngestionDocumentAnalysis(ctx, retryTestCall(model), ingestionDocumentAnalysisRetryPolicy{})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, result.Attempts)
	require.Less(t, time.Since(started), 250*time.Millisecond)
	require.Len(t, model.callSnapshot(), 1)
}

func TestCallIngestionDocumentAnalysisDoesNotCallAfterCallerCancellation(t *testing.T) {
	model := &ingestionRetryModel{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := callIngestionDocumentAnalysis(ctx, retryTestCall(model), immediateRetryPolicy())

	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result.Attempts)
	require.Empty(t, model.callSnapshot())
}

func TestAnalyzeIngestionDocumentUnitDoesNotRetryInvalidResponse(t *testing.T) {
	model := &ingestionRetryModel{steps: []ingestionRetryStep{{
		response: &types.ChatResponse{Content: `{"summary":"incomplete"}`},
	}}}
	var waits []time.Duration
	unit := newTestAnalysisUnit(0, 0, 2, "正文")

	_, attempts, err := analyzeIngestionDocumentUnit(context.Background(), ingestionDocumentMapUnitRequest{
		Model: model, Unit: unit, TotalUnits: 1, RetryPolicy: recordingRetryPolicy(&waits),
	})

	require.Error(t, err)
	require.Equal(t, 1, attempts)
	require.Empty(t, waits)
	require.Len(t, model.callSnapshot(), 1)
}

func TestMapRetriesStayWithinFourWorkers(t *testing.T) {
	units := ingestionMapTestUnits(10)
	var initiallyFailed sync.Map
	model := &ingestionMapModelStub{
		release: make(chan struct{}),
		response: func(_ context.Context, messages []chat.Message) (*types.ChatResponse, error) {
			content := textBetween(messages[1].Content, "<document_unit>\n", "\n</document_unit>")
			if _, loaded := initiallyFailed.LoadOrStore(content, struct{}{}); !loaded {
				return nil, chat.NewProviderError(
					chat.ProviderFailureUnavailable, http.StatusServiceUnavailable, "",
				)
			}
			return mapEvidenceResponse(content), nil
		},
	}

	result, err := mapIngestionDocument(context.Background(), ingestionDocumentMapRequest{
		Model: model, Units: units, RetryPolicy: immediateRetryPolicy(),
	})

	require.NoError(t, err)
	require.Len(t, result, len(units))
	require.Equal(t, int32(ingestionDocumentMapConcurrency), model.maximum.Load())
	require.Len(t, model.callSnapshot(), len(units)*2)
}

func TestMapProgressReportsBudgetAndRetryStatistics(t *testing.T) {
	content := "完整正文"
	budget := requireAnalysisBudget(t, 8192, content)
	transient := chat.NewProviderError(chat.ProviderFailureUnavailable, http.StatusServiceUnavailable, "")
	model := &ingestionRetryModel{steps: []ingestionRetryStep{
		{err: transient}, {response: mapEvidenceResponse("mapped")},
	}}
	var progress []types.IngestionDocumentAnalysisProgress

	result, err := mapIngestionDocument(context.Background(), ingestionDocumentMapRequest{
		Model: model, Units: []ingestionDocumentAnalysisUnit{newTestAnalysisUnit(0, 0, 4, content)},
		RetryPolicy: immediateRetryPolicy(), Budget: budget,
		Progress: func(event types.IngestionDocumentAnalysisProgress) {
			progress = append(progress, event)
		},
	})

	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Len(t, progress, 2)
	require.Equal(t, 1, progress[1].RetryCount)
	require.Zero(t, progress[1].FailedUnitAttempts)
	requireAnalysisProgressBudget(t, progress[0], budget)
	requireAnalysisProgressBudget(t, progress[1], budget)
	payload, marshalErr := json.Marshal(progress)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(payload), content)
	require.NotContains(t, string(payload), "mapped")
}

func TestMapProgressReportsFinalFailedAttemptsWithoutPartialResult(t *testing.T) {
	content := "完整正文"
	budget := requireAnalysisBudget(t, 8192, content)
	transient := chat.NewProviderError(chat.ProviderFailureUnavailable, http.StatusServiceUnavailable, "")
	model := &ingestionRetryModel{steps: []ingestionRetryStep{
		{err: transient}, {err: transient}, {err: transient},
	}}
	var progress []types.IngestionDocumentAnalysisProgress

	result, err := mapIngestionDocument(context.Background(), ingestionDocumentMapRequest{
		Model: model, Units: []ingestionDocumentAnalysisUnit{newTestAnalysisUnit(0, 0, 4, content)},
		RetryPolicy: immediateRetryPolicy(), Budget: budget,
		Progress: func(event types.IngestionDocumentAnalysisProgress) {
			progress = append(progress, event)
		},
	})

	require.Nil(t, result)
	require.Error(t, err)
	require.Len(t, progress, 2)
	terminal := progress[1]
	require.Zero(t, terminal.Completed)
	require.Zero(t, terminal.CoveredCharacters)
	require.Equal(t, 2, terminal.RetryCount)
	require.Equal(t, 3, terminal.FailedUnitAttempts)
	require.Equal(t, http.StatusServiceUnavailable, terminal.HTTPStatus)
	require.Equal(t, string(chat.ProviderFailureUnavailable), terminal.ProviderFailureKind)
	requireAnalysisProgressBudget(t, terminal, budget)
}

func TestReduceRetryPreservesRequestAndOrder(t *testing.T) {
	transient := chat.NewProviderError(chat.ProviderFailureUnavailable, http.StatusBadGateway, "")
	model := &ingestionRetryModel{steps: []ingestionRetryStep{
		{err: transient}, {response: mapEvidenceResponse("reduced")},
	}}
	input := []ingestionDocumentEvidence{
		validIngestionDocumentEvidence("first"), validIngestionDocumentEvidence("second"),
	}
	budget := requireAnalysisBudget(t, 8192, "source content")
	var progress []types.IngestionDocumentAnalysisProgress

	result, err := reduceIngestionDocument(context.Background(), ingestionDocumentReduceRequest{
		Model: model, Evidence: input, CoveredCharacters: 20,
		RetryPolicy: immediateRetryPolicy(), Budget: budget,
		Progress: func(event types.IngestionDocumentAnalysisProgress) {
			progress = append(progress, event)
		},
	})

	require.NoError(t, err)
	require.Equal(t, "reduced", result.Summary)
	calls := model.callSnapshot()
	require.Len(t, calls, 2)
	require.Equal(t, calls[0].messages, calls[1].messages)
	require.Equal(t, calls[0].options, calls[1].options)
	batch := decodeEvidenceBatchForTest(t, calls[0].messages[1].Content)
	require.Equal(t, []string{"first", "second"}, evidenceSummaries(batch.Evidence))
	require.Len(t, progress, 2)
	require.Equal(t, 1, progress[1].RetryCount)
	requireAnalysisProgressBudget(t, progress[1], budget)
}

func retryTestCall(model chat.Chat) ingestionDocumentAnalysisCall {
	return ingestionDocumentAnalysisCall{
		Model:    model,
		Messages: []chat.Message{{Role: "user", Content: "unchanged private body"}},
		Options:  ingestionDocumentAnalysisOptions(),
	}
}

func recordingRetryPolicy(waits *[]time.Duration) ingestionDocumentAnalysisRetryPolicy {
	return ingestionDocumentAnalysisRetryPolicy{Wait: func(_ context.Context, delay time.Duration) error {
		*waits = append(*waits, delay)
		return nil
	}}
}

func immediateRetryPolicy() ingestionDocumentAnalysisRetryPolicy {
	return ingestionDocumentAnalysisRetryPolicy{Wait: func(context.Context, time.Duration) error {
		return nil
	}}
}

func requireAnalysisProgressBudget(
	t *testing.T,
	event types.IngestionDocumentAnalysisProgress,
	budget ingestionDocumentAnalysisTokenBudget,
) {
	t.Helper()
	require.Equal(t, budget.ContextWindowTokens, event.ContextWindowTokens)
	require.Equal(t, budget.CompletionTokens, event.CompletionTokenBudget)
	require.Equal(t, budget.PromptSchemaTokens, event.PromptSchemaTokens)
	require.Equal(t, budget.SafetyTokens, event.SafetyTokens)
	require.Equal(t, budget.ContentTokens, event.ContentTokenBudget)
	require.Equal(t, budget.EstimatedSourceTokens, event.EstimatedSourceTokens)
}
