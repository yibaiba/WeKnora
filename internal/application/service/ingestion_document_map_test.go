package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type ingestionMapCall struct {
	messages []chat.Message
	options  chat.ChatOptions
	redacted bool
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type ingestionMapModelStub struct {
	response func(context.Context, []chat.Message) (*types.ChatResponse, error)
	release  chan struct{}
	once     sync.Once
	current  atomic.Int32
	maximum  atomic.Int32

	mu    sync.Mutex
	calls []ingestionMapCall
}

func (m *ingestionMapModelStub) Chat(
	ctx context.Context,
	messages []chat.Message,
	options *chat.ChatOptions,
) (*types.ChatResponse, error) {
	current := m.current.Add(1)
	defer m.current.Add(-1)
	updateAtomicMaximum(&m.maximum, current)
	if m.release != nil {
		if current == ingestionDocumentMapConcurrency {
			m.once.Do(func() { close(m.release) })
		}
		select {
		case <-m.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	logger.Infof(ctx, "sensitive-map-log")
	m.mu.Lock()
	m.calls = append(m.calls, ingestionMapCall{
		messages: append([]chat.Message(nil), messages...), options: *options,
		redacted: types.LLMTracePayloadsRedacted(ctx),
	})
	m.mu.Unlock()
	return m.response(ctx, messages)
}

func (m *ingestionMapModelStub) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("unexpected streaming call")
}

func (m *ingestionMapModelStub) GetModelName() string { return "map-model" }
func (m *ingestionMapModelStub) GetModelID() string   { return "map-model-id" }

func (m *ingestionMapModelStub) callSnapshot() []ingestionMapCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ingestionMapCall(nil), m.calls...)
}

func TestMapIngestionDocumentUsesFourWorkersAndRestoresOrder(t *testing.T) {
	units := ingestionMapTestUnits(10)
	model := &ingestionMapModelStub{
		release: make(chan struct{}),
		response: func(_ context.Context, messages []chat.Message) (*types.ChatResponse, error) {
			content := textBetween(messages[1].Content, "<document_unit>\n", "\n</document_unit>")
			return mapEvidenceResponse(content), nil
		},
	}
	var progress []types.IngestionDocumentAnalysisProgress
	logs := &lockedBuffer{}
	logger.SetOutput(logs)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })

	result, err := mapIngestionDocument(context.Background(), ingestionDocumentMapRequest{
		Model: model, Units: units, Progress: func(event types.IngestionDocumentAnalysisProgress) {
			progress = append(progress, event)
		},
	})

	require.NoError(t, err)
	require.Len(t, result, len(units))
	for index, evidence := range result {
		require.Equal(t, units[index].Content, evidence.Summary)
	}
	require.Equal(t, int32(ingestionDocumentMapConcurrency), model.maximum.Load())
	calls := model.callSnapshot()
	require.Len(t, calls, len(units))
	for _, call := range calls {
		require.True(t, call.redacted)
		require.Equal(t, float64(0), call.options.Temperature)
		require.True(t, call.options.TemperatureSet)
		require.Equal(t, ingestionDocumentAnalysisCompletionTokens, call.options.MaxCompletionTokens)
		require.Equal(t, ingestionDocumentAnalysisCompletionTokens, call.options.MaxTokens)
		require.JSONEq(t, string(ingestionDocumentEvidenceSchema), string(call.options.Format))
	}
	require.NotContains(t, logs.String(), "sensitive-map-log")
	require.Equal(t, []types.IngestionDocumentAnalysisProgress{{
		Phase: "map_document", Status: ingestionAnalysisProgressRunning,
		UnitCount: len(units), CoveredCharacters: len(units) * len([]rune(units[0].Content)),
	}, {
		Phase: "map_document", Status: ingestionAnalysisProgressSucceeded,
		UnitCount: len(units), Completed: len(units),
		CoveredCharacters: len(units) * len([]rune(units[0].Content)),
	}}, progressWithoutDuration(progress))
}

func TestMapIngestionDocumentFailsWholeBatchWithoutEchoingSensitiveErrors(t *testing.T) {
	secret := "raw-unit-and-provider-secret"
	units := ingestionMapTestUnits(6)
	var progress []types.IngestionDocumentAnalysisProgress
	model := &ingestionMapModelStub{response: func(_ context.Context, messages []chat.Message) (*types.ChatResponse, error) {
		if strings.Contains(messages[1].Content, "unit-03") {
			return nil, fmt.Errorf("provider failed and echoed %s", secret)
		}
		return mapEvidenceResponse("ok"), nil
	}}

	result, err := mapIngestionDocument(context.Background(), ingestionDocumentMapRequest{
		Model: model, Units: units, Progress: func(event types.IngestionDocumentAnalysisProgress) {
			progress = append(progress, event)
		},
	})

	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorDocumentAnalysis, ingestionAdvisorRunErrorCode(err))
	require.NotContains(t, err.Error(), secret)
	require.Len(t, progress, 2)
	require.Equal(t, ingestionAnalysisProgressRunning, progress[0].Status)
	require.Equal(t, ingestionAnalysisProgressFailed, progress[1].Status)
	require.True(t, progress[1].Failed)
}

func TestDecodeIngestionDocumentEvidenceRejectsInvalidStructures(t *testing.T) {
	tests := []string{
		`{"summary":"ok","document_kind_candidates":["report"],"content_mode_candidates":["document"],"structure_signals":["sections"],"chunking_signals":["headings"],"extra":true}`,
		`{"summary":"ok","document_kind_candidates":["unknown"],"content_mode_candidates":["document"],"structure_signals":["sections"],"chunking_signals":["headings"]}`,
		`{"summary":"ok","document_kind_candidates":["report"],"content_mode_candidates":["document"],"structure_signals":[],"chunking_signals":["headings"]}`,
		"```json\n" + validMapEvidenceJSON("ok") + "\n```",
		validMapEvidenceJSON(strings.Repeat("a", ingestionDocumentSummaryMaxRunes+1)),
		`{"summary":"ok","document_kind_candidates":["report","report"],"content_mode_candidates":["document"],"structure_signals":["sections"],"chunking_signals":["headings"]}`,
	}
	for _, raw := range tests {
		_, err := decodeIngestionDocumentEvidence(&types.ChatResponse{Content: raw})
		require.Error(t, err)
	}
	_, err := decodeIngestionDocumentEvidence(nil)
	require.Error(t, err)
}

func TestIngestionDocumentEvidenceSchemaUsesSupportedStrictKeywords(t *testing.T) {
	require.NotContains(t, string(ingestionDocumentEvidenceSchema), "uniqueItems")
}

func ingestionMapTestUnits(count int) []ingestionDocumentAnalysisUnit {
	units := make([]ingestionDocumentAnalysisUnit, count)
	for index := range units {
		content := fmt.Sprintf("unit-%02d", index)
		units[index] = ingestionDocumentAnalysisUnit{
			Index: index, Start: index * len([]rune(content)),
			End: (index + 1) * len([]rune(content)), Content: content,
		}
	}
	return units
}

func mapEvidenceResponse(summary string) *types.ChatResponse {
	return &types.ChatResponse{Content: validMapEvidenceJSON(summary)}
}

func validMapEvidenceJSON(summary string) string {
	payload, err := json.Marshal(ingestionDocumentEvidence{
		Summary: summary, DocumentKindCandidates: []string{types.IngestionDocumentKindReport},
		ContentModeCandidates: []string{types.IngestionContentModeDocument},
		StructureSignals:      []string{"sectioned"}, ChunkingSignals: []string{"prefer headings"},
	})
	if err != nil {
		panic(err)
	}
	return string(payload)
}

func textBetween(value, prefix, suffix string) string {
	start := strings.Index(value, prefix)
	end := strings.LastIndex(value, suffix)
	if start < 0 || end < start {
		return ""
	}
	return value[start+len(prefix) : end]
}

func updateAtomicMaximum(target *atomic.Int32, value int32) {
	for {
		current := target.Load()
		if value <= current || target.CompareAndSwap(current, value) {
			return
		}
	}
}

func progressWithoutDuration(
	events []types.IngestionDocumentAnalysisProgress,
) []types.IngestionDocumentAnalysisProgress {
	result := append([]types.IngestionDocumentAnalysisProgress(nil), events...)
	for index := range result {
		result[index].DurationMS = 0
	}
	return result
}
