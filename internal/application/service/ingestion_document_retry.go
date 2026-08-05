package service

import (
	"context"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const ingestionDocumentAnalysisMaximumAttempts = 3

type ingestionDocumentAnalysisRetryWait func(context.Context, time.Duration) error

type ingestionDocumentAnalysisRetryPolicy struct {
	Wait ingestionDocumentAnalysisRetryWait
}

type ingestionDocumentAnalysisCall struct {
	Model    chat.Chat
	Messages []chat.Message
	Options  *chat.ChatOptions
}

type ingestionDocumentAnalysisCallResult struct {
	Response *types.ChatResponse
	Attempts int
}

func callIngestionDocumentAnalysis(
	ctx context.Context,
	request ingestionDocumentAnalysisCall,
	policy ingestionDocumentAnalysisRetryPolicy,
) (ingestionDocumentAnalysisCallResult, error) {
	for attempt := 1; attempt <= ingestionDocumentAnalysisMaximumAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ingestionDocumentAnalysisCallResult{Attempts: attempt - 1}, err
		}
		response, err := request.Model.Chat(ctx, request.Messages, request.Options)
		if err == nil {
			return ingestionDocumentAnalysisCallResult{Response: response, Attempts: attempt}, nil
		}
		result := ingestionDocumentAnalysisCallResult{Attempts: attempt}
		if attempt == ingestionDocumentAnalysisMaximumAttempts || !retryIngestionDocumentAnalysis(ctx, err) {
			return result, err
		}
		if err := waitForIngestionDocumentAnalysisRetry(
			ctx, policy, ingestionDocumentAnalysisRetryDelay(err, attempt),
		); err != nil {
			return result, err
		}
	}
	panic("unreachable ingestion document retry state")
}

func retryIngestionDocumentAnalysis(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	details, ok := chat.ProviderErrorDetails(err)
	if !ok || details.Kind == chat.ProviderFailureTimeout {
		return false
	}
	if details.Kind == chat.ProviderFailureTransport {
		return true
	}
	switch details.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func ingestionDocumentAnalysisRetryDelay(err error, failedAttempt int) time.Duration {
	if details, ok := chat.ProviderErrorDetails(err); ok &&
		details.StatusCode == http.StatusTooManyRequests && details.RetryAfter != nil {
		return *details.RetryAfter
	}
	if failedAttempt == 1 {
		return time.Second
	}
	return 2 * time.Second
}

func waitForIngestionDocumentAnalysisRetry(
	ctx context.Context,
	policy ingestionDocumentAnalysisRetryPolicy,
	delay time.Duration,
) error {
	if policy.Wait != nil {
		return policy.Wait(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
