package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"golang.org/x/sync/errgroup"
)

const (
	ingestionDocumentMapConcurrency           = 4
	ingestionDocumentAnalysisCompletionTokens = 1024
	ingestionDocumentEvidenceMaxRunes         = 7500
	ingestionDocumentSummaryMaxRunes          = 1200
	ingestionDocumentSignalMaxRunes           = 400
	ingestionDocumentCandidateLimit           = 3
	ingestionDocumentSignalLimit              = 8
	ingestionAnalysisProgressRunning          = "running"
	ingestionAnalysisProgressSucceeded        = "succeeded"
	ingestionAnalysisProgressFailed           = "failed"
)

type ingestionDocumentEvidence struct {
	Summary                string   `json:"summary"`
	DocumentKindCandidates []string `json:"document_kind_candidates"`
	ContentModeCandidates  []string `json:"content_mode_candidates"`
	StructureSignals       []string `json:"structure_signals"`
	ChunkingSignals        []string `json:"chunking_signals"`
}

type ingestionDocumentMapRequest struct {
	Model    chat.Chat
	Units    []ingestionDocumentAnalysisUnit
	Progress func(types.IngestionDocumentAnalysisProgress)
}

type ingestionDocumentMapUnitRequest struct {
	Model      chat.Chat
	Unit       ingestionDocumentAnalysisUnit
	TotalUnits int
}

var ingestionDocumentEvidenceSchema = json.RawMessage(`{
  "type":"object",
  "properties":{
    "summary":{"type":"string","minLength":1,"maxLength":1200},
    "document_kind_candidates":{"type":"array","minItems":1,"maxItems":3,"items":{"type":"string","enum":["policy_manual","faq","tabular_data","report","meeting_notes","presentation","short_article","mixed_document"]}},
    "content_mode_candidates":{"type":"array","minItems":1,"maxItems":3,"items":{"type":"string","enum":["document","faq_candidate","wiki_candidate"]}},
    "structure_signals":{"type":"array","minItems":1,"maxItems":8,"items":{"type":"string","minLength":1,"maxLength":400}},
    "chunking_signals":{"type":"array","minItems":1,"maxItems":8,"items":{"type":"string","minLength":1,"maxLength":400}}
  },
  "required":["summary","document_kind_candidates","content_mode_candidates","structure_signals","chunking_signals"],
  "additionalProperties":false
}`)

func mapIngestionDocument(
	ctx context.Context,
	request ingestionDocumentMapRequest,
) ([]ingestionDocumentEvidence, error) {
	started := time.Now()
	emitIngestionAnalysisProgress(request.Progress, types.IngestionDocumentAnalysisProgress{
		Phase: "map_document", Status: ingestionAnalysisProgressRunning,
		UnitCount: len(request.Units), CoveredCharacters: ingestionAnalysisUnitCoverage(request.Units),
	})
	results := make([]ingestionDocumentEvidence, len(request.Units))
	var completed atomic.Int64
	var covered atomic.Int64
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(ingestionDocumentMapConcurrency)
	for index := range request.Units {
		index := index
		group.Go(func() error {
			evidence, err := analyzeIngestionDocumentUnit(groupCtx, ingestionDocumentMapUnitRequest{
				Model: request.Model, Unit: request.Units[index], TotalUnits: len(request.Units),
			})
			if err != nil {
				return err
			}
			results[index] = evidence
			completed.Add(1)
			covered.Add(int64(request.Units[index].End - request.Units[index].Start))
			return nil
		})
	}
	err := group.Wait()
	status := ingestionAnalysisProgressSucceeded
	failure := ingestionDocumentAnalysisFailureDetails(err)
	if err != nil {
		status = ingestionAnalysisProgressFailed
	}
	emitIngestionAnalysisProgress(request.Progress, types.IngestionDocumentAnalysisProgress{
		Phase: "map_document", Status: status,
		UnitCount: len(request.Units), Completed: int(completed.Load()),
		DurationMS: time.Since(started).Milliseconds(), CoveredCharacters: int(covered.Load()), Failed: err != nil,
		FailureKind: failure.Kind, FailedUnit: failure.Unit,
		ProviderFailureKind: failure.ProviderKind,
		HTTPStatus:          failure.HTTPStatus, FailureParameter: failure.Parameter,
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func ingestionAnalysisUnitCoverage(units []ingestionDocumentAnalysisUnit) int {
	total := 0
	for _, unit := range units {
		total += unit.End - unit.Start
	}
	return total
}

func analyzeIngestionDocumentUnit(
	ctx context.Context,
	request ingestionDocumentMapUnitRequest,
) (ingestionDocumentEvidence, error) {
	if request.Model == nil {
		return ingestionDocumentEvidence{}, documentAnalysisFailure("Map", request.Unit.Index, errors.New("模型未配置"))
	}
	messages := []chat.Message{
		{Role: "system", Content: ingestionDocumentMapSystemPrompt},
		{Role: "user", Content: buildIngestionDocumentMapPrompt(request.Unit, request.TotalUnits)},
	}
	callCtx := sensitiveIngestionLLMContext(ctx, "ingestion_document_map")
	response, err := request.Model.Chat(callCtx, messages, ingestionDocumentAnalysisOptions())
	if err != nil {
		return ingestionDocumentEvidence{}, documentAnalysisFailure("Map", request.Unit.Index, err)
	}
	evidence, err := decodeIngestionDocumentEvidence(response)
	if err != nil {
		return ingestionDocumentEvidence{}, invalidDocumentAnalysisFailure("Map", request.Unit.Index)
	}
	return evidence, nil
}

func buildIngestionDocumentMapPrompt(unit ingestionDocumentAnalysisUnit, totalUnits int) string {
	return fmt.Sprintf(
		"分析单元 %d/%d，原文 rune 范围 [%d,%d)。只根据该单元提取证据。\n<document_unit>\n%s\n</document_unit>",
		unit.Index+1, totalUnits, unit.Start, unit.End, unit.Content,
	)
}

func ingestionDocumentAnalysisOptions() *chat.ChatOptions {
	thinking := false
	return &chat.ChatOptions{
		// Provider adapters translate MaxTokens for models that require max_completion_tokens.
		// Setting both here sends conflicting fields to generic OpenAI-compatible endpoints.
		Temperature: 0, TemperatureSet: true, MaxTokens: ingestionDocumentAnalysisCompletionTokens,
		Thinking: &thinking, Format: ingestionDocumentEvidenceSchema,
	}
}

func sensitiveIngestionLLMContext(ctx context.Context, purpose string) context.Context {
	ctx = types.WithLLMCallMetadata(ctx, purpose, "")
	ctx = types.WithRedactedLLMTracePayloads(ctx)
	return logger.WithSuppressedOutput(ctx)
}

func decodeIngestionDocumentEvidence(response *types.ChatResponse) (ingestionDocumentEvidence, error) {
	if response == nil {
		return ingestionDocumentEvidence{}, fmt.Errorf("模型未返回响应")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(response.Content))
	decoder.DisallowUnknownFields()
	var evidence ingestionDocumentEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return ingestionDocumentEvidence{}, fmt.Errorf("响应不符合 JSON Schema")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return ingestionDocumentEvidence{}, fmt.Errorf("响应包含额外 JSON 值")
	}
	if err := validateIngestionDocumentEvidence(evidence); err != nil {
		return ingestionDocumentEvidence{}, err
	}
	return evidence, nil
}

func validateIngestionDocumentEvidence(evidence ingestionDocumentEvidence) error {
	if err := validateEvidenceText(evidence.Summary, ingestionDocumentSummaryMaxRunes, "summary"); err != nil {
		return err
	}
	if err := validateEvidenceCandidates(evidence.DocumentKindCandidates, allowedDocumentKinds, "document_kind_candidates"); err != nil {
		return err
	}
	if err := validateEvidenceCandidates(evidence.ContentModeCandidates, allowedContentModes, "content_mode_candidates"); err != nil {
		return err
	}
	if err := validateEvidenceSignals(evidence.StructureSignals, "structure_signals"); err != nil {
		return err
	}
	if err := validateEvidenceSignals(evidence.ChunkingSignals, "chunking_signals"); err != nil {
		return err
	}
	payload, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("全文证据无法序列化")
	}
	if utf8.RuneCount(payload) > ingestionDocumentEvidenceMaxRunes {
		return fmt.Errorf("全文证据超过 %d rune 上限", ingestionDocumentEvidenceMaxRunes)
	}
	return nil
}

func validateEvidenceText(value string, limit int, field string) error {
	if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > limit {
		return fmt.Errorf("%s 为空或超过 %d rune", field, limit)
	}
	return nil
}

func validateEvidenceCandidates(values []string, allowed map[string]struct{}, field string) error {
	if len(values) == 0 || len(values) > ingestionDocumentCandidateLimit {
		return fmt.Errorf("%s 数量必须在 1 到 %d 之间", field, ingestionDocumentCandidateLimit)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := allowed[value]; !ok {
			return fmt.Errorf("%s 包含不支持的候选", field)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%s 包含重复候选", field)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateEvidenceSignals(values []string, field string) error {
	if len(values) == 0 || len(values) > ingestionDocumentSignalLimit {
		return fmt.Errorf("%s 数量必须在 1 到 %d 之间", field, ingestionDocumentSignalLimit)
	}
	for _, value := range values {
		if err := validateEvidenceText(value, ingestionDocumentSignalMaxRunes, field); err != nil {
			return err
		}
	}
	return nil
}

func emitIngestionAnalysisProgress(
	progress func(types.IngestionDocumentAnalysisProgress),
	event types.IngestionDocumentAnalysisProgress,
) {
	if progress != nil {
		progress(event)
	}
}

const ingestionDocumentMapSystemPrompt = `你是文档全文分析器的 Map 阶段。分析一个连续原文单元，输出严格 JSON，不得输出 Markdown 或额外字段。
输出必须概括该单元，列出文档类型候选、内容模式候选、结构信号和切分信号。候选值必须来自给定 JSON Schema。信号必须是可供后续归并和切分决策使用的具体证据，不得引用外部知识。`
