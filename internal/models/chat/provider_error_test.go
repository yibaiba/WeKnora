package chat

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/require"
)

func TestProviderHTTPErrorForRedactedContextKeepsOnlySafeMetadata(t *testing.T) {
	ctx := types.WithRedactedLLMTracePayloads(context.Background())
	err := providerHTTPErrorForContext(ctx, providerHTTPFailure{
		StatusCode: 400,
		Body:       []byte(`{"error":{"type":"invalid_request_error","param":"response_format.json_schema","message":"private document"}}`),
	})

	details, ok := ProviderErrorDetails(err)
	require.True(t, ok)
	require.Equal(t, ProviderFailureRequestInvalid, details.Kind)
	require.Equal(t, 400, details.StatusCode)
	require.Equal(t, "response_format", details.Parameter)
	require.NotContains(t, err.Error(), "private document")
}

func TestProviderHTTPErrorKeepsOriginalBodyOutsideRedactedContext(t *testing.T) {
	err := providerHTTPErrorForContext(context.Background(), providerHTTPFailure{
		StatusCode: 400, Body: []byte(`{"error":"diagnostic"}`),
	})

	require.ErrorContains(t, err, "diagnostic")
	_, typed := ProviderErrorDetails(err)
	require.False(t, typed)
}

func TestProviderCallErrorExtractsSDKMetadataWithoutMessage(t *testing.T) {
	parameter := "max_completion_tokens"
	sdkErr := &openai.APIError{
		HTTPStatusCode: 400, Param: &parameter, Type: "invalid_request_error",
		Message: "private provider message",
	}
	err := providerCallError(types.WithRedactedLLMTracePayloads(context.Background()), sdkErr)

	details, ok := ProviderErrorDetails(err)
	require.True(t, ok)
	require.Equal(t, ProviderFailureRequestInvalid, details.Kind)
	require.Equal(t, "max_completion_tokens", details.Parameter)
	require.NotContains(t, err.Error(), "private provider message")
	require.False(t, errors.Is(err, sdkErr))
}

func TestNewProviderErrorRejectsUntrustedMetadata(t *testing.T) {
	err := NewProviderError(ProviderFailureKind("private-kind"), 999, "private-document-fragment")

	details, ok := ProviderErrorDetails(err)
	require.True(t, ok)
	require.Equal(t, ProviderFailureUnknown, details.Kind)
	require.Zero(t, details.StatusCode)
	require.Empty(t, details.Parameter)
	require.NotContains(t, err.Error(), "private")
}

func TestParseProviderRetryAfterAcceptsSecondsAndHTTPDate(t *testing.T) {
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		value  string
		expect time.Duration
	}{
		{name: "seconds", value: "2", expect: 2 * time.Second},
		{name: "zero", value: "0", expect: 0},
		{name: "capped", value: "45", expect: 30 * time.Second},
		{name: "http date", value: now.Add(7 * time.Second).Format(http.TimeFormat), expect: 7 * time.Second},
		{name: "past http date", value: now.Add(-time.Second).Format(http.TimeFormat), expect: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delay := parseProviderRetryAfter(http.StatusTooManyRequests, test.value, now)
			require.NotNil(t, delay)
			require.Equal(t, test.expect, *delay)
		})
	}
}

func TestParseProviderRetryAfterRejectsInvalidOrNonRateLimitValues(t *testing.T) {
	now := time.Now()
	for _, value := range []string{"", "-1", "1.5", "not-a-date"} {
		require.Nil(t, parseProviderRetryAfter(http.StatusTooManyRequests, value, now))
	}
	require.Nil(t, parseProviderRetryAfter(http.StatusServiceUnavailable, "5", now))
}

func TestNewProviderErrorWithDetailsExposesSafeRetryAfterCopy(t *testing.T) {
	retryAfter := 45 * time.Second
	err := NewProviderErrorWithDetails(ProviderFailureDetails{
		Kind: ProviderFailureRateLimited, StatusCode: http.StatusTooManyRequests,
		RetryAfter: &retryAfter,
	})

	details, ok := ProviderErrorDetails(err)
	require.True(t, ok)
	require.NotNil(t, details.RetryAfter)
	require.Equal(t, 30*time.Second, *details.RetryAfter)
	*details.RetryAfter = time.Second
	secondRead, ok := ProviderErrorDetails(err)
	require.True(t, ok)
	require.Equal(t, 30*time.Second, *secondRead.RetryAfter)
}
