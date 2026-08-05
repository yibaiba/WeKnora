package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/require"
)

func TestProviderHTTPErrorForRedactedContextKeepsOnlySafeMetadata(t *testing.T) {
	ctx := types.WithRedactedLLMTracePayloads(context.Background())
	err := providerHTTPErrorForContext(
		ctx, 400,
		[]byte(`{"error":{"type":"invalid_request_error","param":"response_format.json_schema","message":"private document"}}`),
	)

	details, ok := ProviderErrorDetails(err)
	require.True(t, ok)
	require.Equal(t, ProviderFailureRequestInvalid, details.Kind)
	require.Equal(t, 400, details.StatusCode)
	require.Equal(t, "response_format", details.Parameter)
	require.NotContains(t, err.Error(), "private document")
}

func TestProviderHTTPErrorKeepsOriginalBodyOutsideRedactedContext(t *testing.T) {
	err := providerHTTPErrorForContext(context.Background(), 400, []byte(`{"error":"diagnostic"}`))

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
