package chat

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/Tencent/WeKnora/internal/types"
	ollamaapi "github.com/ollama/ollama/api"
)

func ollamaProviderError(ctx context.Context, err error) error {
	if err == nil || !types.LLMTracePayloadsRedacted(ctx) {
		return err
	}
	if ctx.Err() != nil {
		return providerCallError(ctx, ctx.Err())
	}
	var statusErr ollamaapi.StatusError
	if errors.As(err, &statusErr) {
		return newProviderError(providerErrorConfig{StatusCode: statusErr.StatusCode})
	}
	var authErr ollamaapi.AuthorizationError
	if errors.As(err, &authErr) {
		return newProviderError(providerErrorConfig{StatusCode: authErr.StatusCode})
	}
	if isExplicitProviderTransportError(err) {
		return NewProviderError(ProviderFailureTransport, 0, "")
	}
	return NewProviderError(ProviderFailureUnknown, 0, "")
}

func isExplicitProviderTransportError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}
