package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	openai "github.com/sashabaranov/go-openai"
)

type ProviderFailureKind string

const (
	ProviderFailureRequestInvalid ProviderFailureKind = "request_invalid"
	ProviderFailureAuthentication ProviderFailureKind = "authentication_failed"
	ProviderFailureRateLimited    ProviderFailureKind = "rate_limited"
	ProviderFailureUnavailable    ProviderFailureKind = "provider_unavailable"
	ProviderFailureTimeout        ProviderFailureKind = "timeout_or_canceled"
	ProviderFailureTransport      ProviderFailureKind = "transport_failed"
	ProviderFailureUnknown        ProviderFailureKind = "provider_failed"
)

// ProviderError exposes only provider metadata that is safe to persist.
// Response bodies and provider messages are deliberately not retained.
type ProviderError struct {
	kind       ProviderFailureKind
	statusCode int
	parameter  string
	cause      error
}

type ProviderFailureDetails struct {
	Kind       ProviderFailureKind
	StatusCode int
	Parameter  string
}

func NewProviderError(kind ProviderFailureKind, statusCode int, parameter string) error {
	if statusCode < 100 || statusCode > 599 {
		statusCode = 0
	}
	return &ProviderError{
		kind: safeProviderFailureKind(kind), statusCode: statusCode,
		parameter: safeProviderParameter(parameter),
	}
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "provider request failed"
	}
	message := fmt.Sprintf("provider request failed: kind=%s", e.kind)
	if e.statusCode > 0 {
		message += fmt.Sprintf(", status=%d", e.statusCode)
	}
	if e.parameter != "" {
		message += ", parameter=" + e.parameter
	}
	return message
}

func (e *ProviderError) Unwrap() error              { return e.cause }
func (e *ProviderError) SafeForObservability() bool { return true }

func ProviderErrorDetails(err error) (ProviderFailureDetails, bool) {
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		return ProviderFailureDetails{}, false
	}
	return ProviderFailureDetails{
		Kind: providerErr.kind, StatusCode: providerErr.statusCode, Parameter: providerErr.parameter,
	}, true
}

func providerCallError(ctx context.Context, err error) error {
	if err == nil || !types.LLMTracePayloadsRedacted(ctx) {
		return err
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		cause := context.Cause(ctx)
		if cause == nil && errors.Is(err, context.DeadlineExceeded) {
			cause = context.DeadlineExceeded
		}
		if cause == nil {
			cause = context.Canceled
		}
		return &ProviderError{kind: ProviderFailureTimeout, cause: cause}
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		parameter := ""
		if apiErr.Param != nil {
			parameter = safeProviderParameter(*apiErr.Param)
		}
		return newProviderError(apiErr.HTTPStatusCode, parameter, apiErr.Type)
	}
	var requestErr *openai.RequestError
	if errors.As(err, &requestErr) {
		return providerHTTPError(requestErr.HTTPStatusCode, requestErr.Body)
	}
	return &ProviderError{kind: ProviderFailureTransport}
}

func providerHTTPErrorForContext(ctx context.Context, statusCode int, body []byte) error {
	if !types.LLMTracePayloadsRedacted(ctx) {
		return fmt.Errorf("API request failed with status %d: %s", statusCode, string(body))
	}
	return providerHTTPError(statusCode, body)
}

func providerHTTPError(statusCode int, body []byte) error {
	var envelope struct {
		Error struct {
			Type  string  `json:"type"`
			Param *string `json:"param"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	parameter := ""
	if envelope.Error.Param != nil {
		parameter = safeProviderParameter(*envelope.Error.Param)
	}
	return newProviderError(statusCode, parameter, envelope.Error.Type)
}

func newProviderError(statusCode int, parameter, providerType string) *ProviderError {
	return &ProviderError{
		kind:       providerFailureKind(statusCode, providerType),
		statusCode: statusCode,
		parameter:  safeProviderParameter(parameter),
	}
}

func safeProviderFailureKind(kind ProviderFailureKind) ProviderFailureKind {
	switch kind {
	case ProviderFailureRequestInvalid, ProviderFailureAuthentication, ProviderFailureRateLimited,
		ProviderFailureUnavailable, ProviderFailureTimeout, ProviderFailureTransport, ProviderFailureUnknown:
		return kind
	default:
		return ProviderFailureUnknown
	}
}

func providerFailureKind(statusCode int, providerType string) ProviderFailureKind {
	switch statusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		return ProviderFailureRequestInvalid
	case http.StatusUnauthorized, http.StatusForbidden:
		return ProviderFailureAuthentication
	case http.StatusRequestTimeout:
		return ProviderFailureTimeout
	case http.StatusTooManyRequests:
		return ProviderFailureRateLimited
	}
	if statusCode >= http.StatusInternalServerError {
		return ProviderFailureUnavailable
	}
	if strings.EqualFold(providerType, "invalid_request_error") {
		return ProviderFailureRequestInvalid
	}
	return ProviderFailureUnknown
}

func safeProviderParameter(parameter string) string {
	for _, root := range []string{
		"response_format", "output_config", "max_tokens", "max_completion_tokens",
		"temperature", "tools", "tool_choice", "parallel_tool_calls",
	} {
		if parameter == root || strings.HasPrefix(parameter, root+".") || strings.HasPrefix(parameter, root+"[") {
			return root
		}
	}
	return ""
}
