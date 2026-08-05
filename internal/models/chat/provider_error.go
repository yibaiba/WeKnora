package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	providerMaximumRetryAfter                         = 30 * time.Second
)

// ProviderError exposes only provider metadata that is safe to persist.
// Response bodies and provider messages are deliberately not retained.
type ProviderError struct {
	kind       ProviderFailureKind
	statusCode int
	parameter  string
	retryAfter *time.Duration
	cause      error
}

type ProviderFailureDetails struct {
	Kind       ProviderFailureKind
	StatusCode int
	Parameter  string
	RetryAfter *time.Duration
}

type safeProviderErrorConfig struct {
	Kind       ProviderFailureKind
	StatusCode int
	Parameter  string
	RetryAfter *time.Duration
}

type providerHTTPFailure struct {
	StatusCode       int
	Body             []byte
	RetryAfterHeader string
}

type providerErrorConfig struct {
	StatusCode   int
	Parameter    string
	ProviderType string
	RetryAfter   *time.Duration
}

func NewProviderError(kind ProviderFailureKind, statusCode int, parameter string) error {
	return newSafeProviderError(safeProviderErrorConfig{
		Kind: kind, StatusCode: statusCode, Parameter: parameter,
	})
}

func NewProviderErrorWithDetails(details ProviderFailureDetails) error {
	var retryAfter *time.Duration
	if details.RetryAfter != nil {
		retryAfter = normalizedRetryAfter(details.StatusCode, *details.RetryAfter)
	}
	return newSafeProviderError(safeProviderErrorConfig{
		Kind: details.Kind, StatusCode: details.StatusCode, Parameter: details.Parameter,
		RetryAfter: retryAfter,
	})
}

func newSafeProviderError(config safeProviderErrorConfig) error {
	if config.StatusCode < 100 || config.StatusCode > 599 {
		config.StatusCode = 0
		config.RetryAfter = nil
	}
	return &ProviderError{
		kind: safeProviderFailureKind(config.Kind), statusCode: config.StatusCode,
		parameter: safeProviderParameter(config.Parameter), retryAfter: config.RetryAfter,
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
	if e.retryAfter != nil {
		message += ", retry_after=" + e.retryAfter.String()
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
	details := ProviderFailureDetails{
		Kind: providerErr.kind, StatusCode: providerErr.statusCode, Parameter: providerErr.parameter,
	}
	if providerErr.retryAfter != nil {
		retryAfter := *providerErr.retryAfter
		details.RetryAfter = &retryAfter
	}
	return details, true
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
		return newProviderError(providerErrorConfig{
			StatusCode: apiErr.HTTPStatusCode, Parameter: parameter, ProviderType: apiErr.Type,
		})
	}
	var requestErr *openai.RequestError
	if errors.As(err, &requestErr) {
		return providerHTTPError(providerHTTPFailure{
			StatusCode: requestErr.HTTPStatusCode, Body: requestErr.Body,
		})
	}
	return &ProviderError{kind: ProviderFailureTransport}
}

func providerHTTPErrorForContext(
	ctx context.Context,
	failure providerHTTPFailure,
) error {
	if !types.LLMTracePayloadsRedacted(ctx) {
		return fmt.Errorf(
			"API request failed with status %d: %s", failure.StatusCode, string(failure.Body),
		)
	}
	return providerHTTPError(failure)
}

func providerHTTPError(failure providerHTTPFailure) error {
	var envelope struct {
		Error struct {
			Type  string  `json:"type"`
			Param *string `json:"param"`
		} `json:"error"`
	}
	_ = json.Unmarshal(failure.Body, &envelope)
	parameter := ""
	if envelope.Error.Param != nil {
		parameter = safeProviderParameter(*envelope.Error.Param)
	}
	return newProviderError(providerErrorConfig{
		StatusCode: failure.StatusCode, Parameter: parameter, ProviderType: envelope.Error.Type,
		RetryAfter: parseProviderRetryAfter(
			failure.StatusCode, failure.RetryAfterHeader, time.Now(),
		),
	})
}

func newProviderError(config providerErrorConfig) *ProviderError {
	return &ProviderError{
		kind:       providerFailureKind(config.StatusCode, config.ProviderType),
		statusCode: config.StatusCode,
		parameter:  safeProviderParameter(config.Parameter),
		retryAfter: config.RetryAfter,
	}
}

func parseProviderRetryAfter(statusCode int, value string, now time.Time) *time.Duration {
	if statusCode != http.StatusTooManyRequests {
		return nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			return nil
		}
		maximumSeconds := int64(providerMaximumRetryAfter / time.Second)
		if seconds > maximumSeconds {
			seconds = maximumSeconds
		}
		return normalizedRetryAfter(statusCode, time.Duration(seconds)*time.Second)
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return nil
	}
	delay := retryAt.Sub(now)
	if delay < 0 {
		delay = 0
	}
	return normalizedRetryAfter(statusCode, delay)
}

func normalizedRetryAfter(statusCode int, value time.Duration) *time.Duration {
	if statusCode != http.StatusTooManyRequests || value < 0 {
		return nil
	}
	value = min(value, providerMaximumRetryAfter)
	return &value
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
