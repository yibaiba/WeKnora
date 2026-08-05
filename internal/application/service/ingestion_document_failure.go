package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
)

const (
	ingestionAnalysisFailureProviderCall      = "provider_call_failed"
	ingestionAnalysisFailureStrictSchema      = "strict_schema_unsupported"
	ingestionAnalysisFailureRequestParameters = "request_parameters_unsupported"
	ingestionAnalysisFailureRateLimited       = "provider_rate_limited"
	ingestionAnalysisFailureInvalidResponse   = "invalid_structured_response"
	ingestionAnalysisFailureRequestRejected   = "provider_request_rejected"
	ingestionAnalysisFailureTimeout           = "timeout_or_canceled"
)

type ingestionDocumentAnalysisError struct {
	cause    error
	metadata ingestionDocumentAnalysisFailureMetadata
}

type documentAnalysisFailureRequest struct {
	Stage    string
	Unit     int
	Cause    error
	Attempts int
}

func documentAnalysisFailure(stage string, unit int, cause error) error {
	return documentAnalysisFailureWithAttempts(documentAnalysisFailureRequest{
		Stage: stage, Unit: unit, Cause: cause, Attempts: 1,
	})
}

func documentAnalysisFailureWithAttempts(request documentAnalysisFailureRequest) error {
	metadata := ingestionDocumentAnalysisFailureMetadata{
		Kind: classifyIngestionDocumentAnalysisFailure(request.Cause),
		Unit: request.Unit + 1, Attempts: request.Attempts,
	}
	if providerErr, ok := chat.ProviderErrorDetails(request.Cause); ok {
		metadata.ProviderKind = string(providerErr.Kind)
		metadata.HTTPStatus = providerErr.StatusCode
		metadata.Parameter = providerErr.Parameter
	}
	return newDocumentAnalysisFailure(request.Stage, metadata)
}

func invalidDocumentAnalysisFailure(stage string, unit int) error {
	return invalidDocumentAnalysisFailureWithAttempts(stage, unit, 1)
}

func invalidDocumentAnalysisFailureWithAttempts(stage string, unit, attempts int) error {
	return newDocumentAnalysisFailure(stage, ingestionDocumentAnalysisFailureMetadata{
		Kind: ingestionAnalysisFailureInvalidResponse, Unit: unit + 1, Attempts: attempts,
	})
}

func newDocumentAnalysisFailure(stage string, metadata ingestionDocumentAnalysisFailureMetadata) error {
	safe := newIngestionAdvisorRunError(
		ingestionAdvisorErrorDocumentAnalysis, "文档全文 %s 分析失败（单元 %d）：%s%s",
		stage, metadata.Unit, ingestionDocumentAnalysisFailureLabel(metadata.Kind),
		ingestionDocumentAnalysisFailureSuffix(metadata),
	)
	return &ingestionDocumentAnalysisError{cause: safe, metadata: metadata}
}

func (e *ingestionDocumentAnalysisError) Error() string { return e.cause.Error() }
func (e *ingestionDocumentAnalysisError) Unwrap() error { return e.cause }

type ingestionDocumentAnalysisFailureMetadata struct {
	Kind         string
	Unit         int
	ProviderKind string
	HTTPStatus   int
	Parameter    string
	Attempts     int
}

func ingestionDocumentAnalysisFailureDetails(err error) ingestionDocumentAnalysisFailureMetadata {
	var failure *ingestionDocumentAnalysisError
	if errors.As(err, &failure) {
		return failure.metadata
	}
	return ingestionDocumentAnalysisFailureMetadata{}
}

func ingestionDocumentAnalysisFailureSuffix(metadata ingestionDocumentAnalysisFailureMetadata) string {
	details := make([]string, 0, 3)
	if metadata.ProviderKind != "" {
		details = append(details, "供应商错误 "+metadata.ProviderKind)
	}
	if metadata.HTTPStatus > 0 {
		details = append(details, fmt.Sprintf("HTTP %d", metadata.HTTPStatus))
	}
	if metadata.Parameter != "" {
		details = append(details, "参数 "+metadata.Parameter)
	}
	if len(details) == 0 {
		return ""
	}
	return "（" + strings.Join(details, "，") + "）"
}

func classifyIngestionDocumentAnalysisFailure(cause error) string {
	if errors.Is(cause, context.DeadlineExceeded) || errors.Is(cause, context.Canceled) {
		return ingestionAnalysisFailureTimeout
	}
	providerErr, ok := chat.ProviderErrorDetails(cause)
	if !ok {
		return ingestionAnalysisFailureProviderCall
	}
	if providerErr.Parameter == "response_format" || providerErr.Parameter == "output_config" {
		return ingestionAnalysisFailureStrictSchema
	}
	if providerErr.Parameter == "max_tokens" || providerErr.Parameter == "max_completion_tokens" ||
		providerErr.Parameter == "temperature" {
		return ingestionAnalysisFailureRequestParameters
	}
	switch providerErr.Kind {
	case chat.ProviderFailureTimeout:
		return ingestionAnalysisFailureTimeout
	case chat.ProviderFailureRateLimited:
		return ingestionAnalysisFailureRateLimited
	case chat.ProviderFailureRequestInvalid:
		return ingestionAnalysisFailureRequestRejected
	default:
		return ingestionAnalysisFailureProviderCall
	}
}

func ingestionDocumentAnalysisFailureLabel(kind string) string {
	switch kind {
	case ingestionAnalysisFailureStrictSchema:
		return "模型或供应商不支持严格 JSON Schema"
	case ingestionAnalysisFailureRequestParameters:
		return "供应商不支持全文分析请求参数"
	case ingestionAnalysisFailureRateLimited:
		return "供应商限流"
	case ingestionAnalysisFailureInvalidResponse:
		return "模型返回结构无效"
	case ingestionAnalysisFailureRequestRejected:
		return "供应商拒绝全文分析请求"
	case ingestionAnalysisFailureTimeout:
		return "调用超时或已取消"
	default:
		return "模型调用失败"
	}
}
