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
	ingestionDocumentCandidateLimit           = 3
	ingestionAnalysisProgressRunning          = "running"
	ingestionAnalysisProgressSucceeded        = "succeeded"
	ingestionAnalysisProgressFailed           = "failed"
)

type ingestionDocumentEvidence struct {
	Summary                string   `json:"summary"`
	DocumentKindCandidates []string `json:"document_kind_candidates"`
	ContentModeCandidates  []string `json:"content_mode_candidates"`
	DominantStructures     []string `json:"dominant_structures"`
	BoundaryPriorities     []string `json:"boundary_priorities"`
	RiskSignals            []string `json:"risk_signals"`
}

var allowedIngestionDominantStructures = map[string]struct{}{
	"section_body": {}, "table": {}, "repeated_records": {}, "faq": {},
	"list": {}, "code": {}, "image_text": {}, "mixed": {},
}

var allowedIngestionBoundaryPriorities = map[string]struct{}{
	"section": {}, "paragraph": {}, "record": {}, "table_row": {},
	"list_item": {}, "faq_pair": {}, "code_block": {},
}

var allowedIngestionRiskSignals = map[string]struct{}{
	"flat_table": {}, "unreliable_headings": {}, "ocr_noise": {},
	"repeated_headers_footers": {}, "oversize_atomic": {}, "mixed_layout": {},
}

type ingestionDocumentMapRequest struct {
	Model       chat.Chat
	Units       []ingestionDocumentAnalysisUnit
	Progress    func(types.IngestionDocumentAnalysisProgress)
	RetryPolicy ingestionDocumentAnalysisRetryPolicy
	Budget      ingestionDocumentAnalysisTokenBudget
}

type ingestionDocumentMapUnitRequest struct {
	Model       chat.Chat
	Unit        ingestionDocumentAnalysisUnit
	TotalUnits  int
	RetryPolicy ingestionDocumentAnalysisRetryPolicy
}

var ingestionDocumentEvidenceSchema = json.RawMessage(`{
  "type":"object",
  "properties":{
    "summary":{"type":"string","minLength":1,"maxLength":1200},
    "document_kind_candidates":{"type":"array","minItems":1,"maxItems":3,"items":{"type":"string","enum":["policy_manual","faq","tabular_data","report","meeting_notes","presentation","short_article","mixed_document"]}},
    "content_mode_candidates":{"type":"array","minItems":1,"maxItems":3,"items":{"type":"string","enum":["document","faq_candidate","wiki_candidate"]}},
    "dominant_structures":{"type":"array","minItems":1,"maxItems":8,"items":{"type":"string","enum":["section_body","table","repeated_records","faq","list","code","image_text","mixed"]}},
    "boundary_priorities":{"type":"array","minItems":1,"maxItems":7,"items":{"type":"string","enum":["section","paragraph","record","table_row","list_item","faq_pair","code_block"]}},
    "risk_signals":{"type":"array","minItems":0,"maxItems":6,"items":{"type":"string","enum":["flat_table","unreliable_headings","ocr_noise","repeated_headers_footers","oversize_atomic","mixed_layout"]}}
  },
  "required":["summary","document_kind_candidates","content_mode_candidates","dominant_structures","boundary_priorities","risk_signals"],
  "additionalProperties":false
}`)

func mapIngestionDocument(
	ctx context.Context,
	request ingestionDocumentMapRequest,
) ([]ingestionDocumentEvidence, error) {
	started := time.Now()
	emitIngestionAnalysisProgress(request.Progress, ingestionAnalysisProgressWithBudget(types.IngestionDocumentAnalysisProgress{
		Phase: "map_document", Status: ingestionAnalysisProgressRunning,
		UnitCount: len(request.Units), CoveredCharacters: ingestionAnalysisUnitCoverage(request.Units),
	}, request.Budget))
	results := make([]ingestionDocumentEvidence, len(request.Units))
	var completed atomic.Int64
	var covered atomic.Int64
	var retries atomic.Int64
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(ingestionDocumentMapConcurrency)
	for index := range request.Units {
		index := index
		group.Go(func() error {
			evidence, attempts, err := analyzeIngestionDocumentUnit(groupCtx, ingestionDocumentMapUnitRequest{
				Model: request.Model, Unit: request.Units[index], TotalUnits: len(request.Units),
				RetryPolicy: request.RetryPolicy,
			})
			retries.Add(int64(max(attempts-1, 0)))
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
	emitIngestionAnalysisProgress(request.Progress, ingestionAnalysisProgressWithBudget(types.IngestionDocumentAnalysisProgress{
		Phase: "map_document", Status: status,
		UnitCount: len(request.Units), Completed: int(completed.Load()),
		DurationMS: time.Since(started).Milliseconds(), CoveredCharacters: int(covered.Load()), Failed: err != nil,
		RetryCount: int(retries.Load()), FailedUnitAttempts: failure.Attempts,
		FailureKind: failure.Kind, FailedUnit: failure.Unit,
		ProviderFailureKind: failure.ProviderKind,
		HTTPStatus:          failure.HTTPStatus, FailureParameter: failure.Parameter,
	}, request.Budget))
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
) (ingestionDocumentEvidence, int, error) {
	if request.Model == nil {
		return ingestionDocumentEvidence{}, 0,
			documentAnalysisFailureWithAttempts(documentAnalysisFailureRequest{
				Stage: "Map", Unit: request.Unit.Index, Cause: errors.New("模型未配置"),
			})
	}
	messages := []chat.Message{
		{Role: "system", Content: ingestionDocumentMapSystemPrompt},
		{Role: "user", Content: buildIngestionDocumentMapPrompt(request.Unit, request.TotalUnits)},
	}
	callCtx := sensitiveIngestionLLMContext(ctx, types.LLMCallPurposeIngestionDocumentMap)
	call, err := callIngestionDocumentAnalysis(callCtx, ingestionDocumentAnalysisCall{
		Model: request.Model, Messages: messages, Options: ingestionDocumentAnalysisOptions(),
	}, request.RetryPolicy)
	if err != nil {
		return ingestionDocumentEvidence{}, call.Attempts,
			documentAnalysisFailureWithAttempts(documentAnalysisFailureRequest{
				Stage: "Map", Unit: request.Unit.Index, Cause: err, Attempts: call.Attempts,
			})
	}
	evidence, err := decodeIngestionDocumentEvidence(call.Response)
	if err != nil {
		return ingestionDocumentEvidence{}, call.Attempts,
			invalidDocumentAnalysisFailureWithAttempts("Map", request.Unit.Index, call.Attempts)
	}
	return evidence, call.Attempts, nil
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
	if err := validateEvidenceEnumSet(evidence.DominantStructures, evidenceEnumSpec{
		field: "dominant_structures", minimum: 1, maximum: 8, allowed: allowedIngestionDominantStructures,
	}); err != nil {
		return err
	}
	if err := validateEvidenceEnumSet(evidence.BoundaryPriorities, evidenceEnumSpec{
		field: "boundary_priorities", minimum: 1, maximum: 7, allowed: allowedIngestionBoundaryPriorities,
	}); err != nil {
		return err
	}
	if err := validateEvidenceEnumSet(evidence.RiskSignals, evidenceEnumSpec{
		field: "risk_signals", maximum: 6, allowed: allowedIngestionRiskSignals,
	}); err != nil {
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

type evidenceEnumSpec struct {
	field   string
	minimum int
	maximum int
	allowed map[string]struct{}
}

func validateEvidenceEnumSet(values []string, spec evidenceEnumSpec) error {
	if len(values) < spec.minimum || len(values) > spec.maximum {
		return fmt.Errorf("%s 数量必须在 %d 到 %d 之间", spec.field, spec.minimum, spec.maximum)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := spec.allowed[value]; !ok {
			return fmt.Errorf("%s 包含不支持的枚举", spec.field)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%s 包含重复枚举", spec.field)
		}
		seen[value] = struct{}{}
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

func ingestionAnalysisProgressWithBudget(
	event types.IngestionDocumentAnalysisProgress,
	budget ingestionDocumentAnalysisTokenBudget,
) types.IngestionDocumentAnalysisProgress {
	event.ContextWindowTokens = budget.ContextWindowTokens
	event.CompletionTokenBudget = budget.CompletionTokens
	event.PromptSchemaTokens = budget.PromptSchemaTokens
	event.SafetyTokens = budget.SafetyTokens
	event.ContentTokenBudget = budget.ContentTokens
	event.EstimatedSourceTokens = budget.EstimatedSourceTokens
	return event
}

const ingestionDocumentMapSystemPrompt = `你是文档全文分析器的 Map 阶段。分析一个连续原文单元，输出严格 JSON，不得输出 Markdown 或额外字段。
输出必须概括该单元，并从 Schema 枚举中选择文档类型、内容模式、主导结构、边界优先级和风险信号。不得输出源位置、最终分块边界、标题正文或 Schema 之外的自由文本结构信号。`
