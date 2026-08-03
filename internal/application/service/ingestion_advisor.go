package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

const ingestionAdvisorTimeout = 90 * time.Second

var (
	allowedDocumentKinds = map[string]struct{}{
		types.IngestionDocumentKindPolicyManual: {}, types.IngestionDocumentKindFAQ: {},
		types.IngestionDocumentKindTabularData: {}, types.IngestionDocumentKindReport: {},
		types.IngestionDocumentKindMeetingNotes: {}, types.IngestionDocumentKindPresentation: {},
		types.IngestionDocumentKindShortArticle: {}, types.IngestionDocumentKindMixedDocument: {},
	}
	allowedContentModes = map[string]struct{}{
		types.IngestionContentModeDocument: {}, types.IngestionContentModeFAQCandidate: {},
		types.IngestionContentModeWikiCandidate: {},
	}
	allowedChunkingStrategies = map[string]struct{}{
		"auto": {}, "heading": {}, "heuristic": {}, "legacy": {}, "recursive": {},
	}
	allowedIngestionSeparators = map[string]struct{}{
		"\n\n": {}, "\n": {}, "。": {}, "！": {}, "？": {}, "；": {}, ";": {}, " ": {},
	}
)

type modelIngestionAdvisor struct {
	modelService interfaces.ModelService
}

type advisorModelResponse struct {
	DocumentKind           string                                `json:"document_kind"`
	Confidence             float64                               `json:"confidence"`
	RecommendedContentMode string                                `json:"recommended_content_mode"`
	ReasonCodes            []string                              `json:"reason_codes"`
	Summary                string                                `json:"summary"`
	RecommendedChunking    types.IngestionChunkingRecommendation `json:"recommended_chunking"`
}

func NewIngestionAdvisor(modelService interfaces.ModelService) interfaces.IngestionAdvisor {
	return &modelIngestionAdvisor{modelService: modelService}
}

// ValidateIngestionAdvisorConfig rejects unsupported modes and prompt
// versions without normalizing or mutating the upload payload.
func ValidateIngestionAdvisorConfig(config *types.IngestionAdvisorConfig) error {
	if config == nil {
		return nil
	}
	if config.Mode != types.IngestionAdvisorModeSmart && config.Mode != types.IngestionAdvisorModeOff {
		return fmt.Errorf("ingestion_advisor.mode %q 不受支持", config.Mode)
	}
	if config.PromptVersion != "" && config.PromptVersion != types.IngestionPromptVersionV1 {
		return fmt.Errorf("ingestion_advisor.prompt_version %q 不受支持", config.PromptVersion)
	}
	return nil
}

func ingestionPromptVersion(config *types.IngestionAdvisorConfig) string {
	if config != nil && config.PromptVersion != "" {
		return config.PromptVersion
	}
	return types.IngestionPromptVersionV1
}

func (a *modelIngestionAdvisor) Analyze(
	ctx context.Context,
	request types.IngestionAdvisorRequest,
) (*types.IngestionAnalysis, error) {
	if strings.TrimSpace(request.ModelID) == "" {
		return nil, fmt.Errorf("知识库未配置摘要模型，无法执行文档智能分析")
	}
	if request.PromptVersion != types.IngestionPromptVersionV1 {
		return nil, fmt.Errorf("不支持的文档分析 Prompt 版本 %q", request.PromptVersion)
	}
	if a == nil || a.modelService == nil {
		return nil, fmt.Errorf("文档智能分析服务未配置")
	}
	model, err := a.modelService.GetChatModel(ctx, request.ModelID)
	if err != nil {
		return nil, fmt.Errorf("加载文档分析模型失败: %w", err)
	}

	prompt, err := buildIngestionAdvisorPrompt(request.Content)
	if err != nil {
		return nil, fmt.Errorf("构建文档分析请求失败: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, ingestionAdvisorTimeout)
	defer cancel()
	callCtx = types.WithLLMCallMetadata(callCtx, "document_analysis", "")
	response, err := model.Chat(callCtx, []chat.Message{{Role: "user", Content: prompt}}, &chat.ChatOptions{
		Temperature: 0,
		Format:      utils.GenerateSchema[advisorModelResponse](),
	})
	if err != nil {
		return nil, fmt.Errorf("文档分析模型调用失败: %w", err)
	}
	if response == nil {
		return nil, fmt.Errorf("文档分析模型返回空响应")
	}
	parsed, err := parseIngestionAdvisorResponse(response.Content)
	if err != nil {
		return nil, err
	}
	return &types.IngestionAnalysis{
		DocumentKind:           parsed.DocumentKind,
		Confidence:             parsed.Confidence,
		RecommendedContentMode: parsed.RecommendedContentMode,
		ReasonCodes:            append([]string(nil), parsed.ReasonCodes...),
		Summary:                parsed.Summary,
		RecommendedChunking:    cloneChunkingRecommendation(parsed.RecommendedChunking),
		ModelID:                request.ModelID,
		PromptVersion:          request.PromptVersion,
	}, nil
}

func buildIngestionAdvisorPrompt(content string) (string, error) {
	payload, err := json.Marshal(BuildIngestionDocumentProfile(content))
	if err != nil {
		return "", err
	}
	return `你是文档智能入库顾问。根据全文结构统计和抽样内容判断文档画像，并只返回符合指定 JSON Schema 的对象。
document_kind 只能是 policy_manual、faq、tabular_data、report、meeting_notes、presentation、short_article、mixed_document。
recommended_content_mode 只能是 document、faq_candidate、wiki_candidate；它只是标注，不能改变知识库类型。
strategy 只能是 auto、heading、heuristic、legacy、recursive。
chunk_size 为 100–4000；chunk_overlap 为 0–min(500, chunk_size/2)。
parent_chunk_size 为 512–8192；child_chunk_size 为 64–2048 且不能大于 parent_chunk_size。
separators 只能从 ["\\n\\n","\\n","。","！","？","；",";"," "] 选择。
不要输出 Markdown、解释文字、额外字段或默认占位值。

文档数据：` + string(payload), nil
}

func parseIngestionAdvisorResponse(raw string) (*advisorModelResponse, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var response advisorModelResponse
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("文档分析模型返回的 JSON 无效: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("文档分析模型返回的 JSON 包含额外内容: %w", err)
	}
	if err := validateAdvisorModelResponse(response); err != nil {
		return nil, fmt.Errorf("文档分析模型返回参数校验失败: %w", err)
	}
	return &response, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("存在第二个 JSON 值")
}

func validateAdvisorModelResponse(response advisorModelResponse) error {
	return ValidateIngestionAnalysis(&types.IngestionAnalysis{
		DocumentKind:           response.DocumentKind,
		Confidence:             response.Confidence,
		RecommendedContentMode: response.RecommendedContentMode,
		ReasonCodes:            response.ReasonCodes,
		Summary:                response.Summary,
		RecommendedChunking:    response.RecommendedChunking,
	})
}

// ValidateIngestionAnalysis protects the pipeline from both remote model
// responses and injected advisor implementations.
func ValidateIngestionAnalysis(analysis *types.IngestionAnalysis) error {
	if analysis == nil {
		return fmt.Errorf("文档分析结果为空")
	}
	if _, ok := allowedDocumentKinds[analysis.DocumentKind]; !ok {
		return fmt.Errorf("document_kind %q 不受支持", analysis.DocumentKind)
	}
	if analysis.Confidence < 0 || analysis.Confidence > 1 {
		return fmt.Errorf("confidence 必须在 0 到 1 之间")
	}
	if _, ok := allowedContentModes[analysis.RecommendedContentMode]; !ok {
		return fmt.Errorf("recommended_content_mode %q 不受支持", analysis.RecommendedContentMode)
	}
	if len(analysis.ReasonCodes) == 0 || strings.TrimSpace(analysis.Summary) == "" {
		return fmt.Errorf("reason_codes 和 summary 不能为空")
	}
	for _, code := range analysis.ReasonCodes {
		if strings.TrimSpace(code) == "" {
			return fmt.Errorf("reason_codes 不能包含空值")
		}
	}
	return ValidateIngestionChunkingRecommendation(analysis.RecommendedChunking)
}

func ValidateIngestionChunkingRecommendation(value types.IngestionChunkingRecommendation) error {
	if _, ok := allowedChunkingStrategies[value.Strategy]; !ok {
		return fmt.Errorf("strategy %q 不受支持", value.Strategy)
	}
	if value.ChunkSize < 100 || value.ChunkSize > 4000 {
		return fmt.Errorf("chunk_size 必须在 100 到 4000 之间")
	}
	maxOverlap := min(500, value.ChunkSize/2)
	if value.ChunkOverlap < 0 || value.ChunkOverlap > maxOverlap {
		return fmt.Errorf("chunk_overlap 必须在 0 到 %d 之间", maxOverlap)
	}
	if value.ParentChunkSize < 512 || value.ParentChunkSize > 8192 {
		return fmt.Errorf("parent_chunk_size 必须在 512 到 8192 之间")
	}
	if value.ChildChunkSize < 64 || value.ChildChunkSize > 2048 {
		return fmt.Errorf("child_chunk_size 必须在 64 到 2048 之间")
	}
	if value.ChildChunkSize > value.ParentChunkSize {
		return fmt.Errorf("child_chunk_size 不能大于 parent_chunk_size")
	}
	if len(value.Separators) == 0 {
		return fmt.Errorf("separators 不能为空")
	}
	for _, separator := range value.Separators {
		if _, ok := allowedIngestionSeparators[separator]; !ok {
			return fmt.Errorf("separator %q 不受支持", separator)
		}
	}
	return nil
}

func cloneChunkingRecommendation(value types.IngestionChunkingRecommendation) types.IngestionChunkingRecommendation {
	value.Separators = append([]string(nil), value.Separators...)
	return value
}
