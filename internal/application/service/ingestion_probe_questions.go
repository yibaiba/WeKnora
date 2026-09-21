package service

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker/evalquery"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	ingestionProbeQuestionCount = 3
	ingestionProbeMaxAttempts   = 6
	ingestionProbeTimeout       = 45 * time.Second
)

func (s *knowledgeService) attachIngestionProbeQuestions(
	ctx context.Context,
	run ingestionAdvisorRun,
	analysis *types.IngestionAnalysis,
) {
	if analysis == nil {
		return
	}
	probes, err := s.generateIngestionProbeQuestions(ctx, run)
	if err != nil {
		logger.Warnf(ctx, "ingestion probe questions skipped for %s: %v", run.Knowledge.ID, err)
		analysis.ProbeQuestions = &types.IngestionProbeQuestions{
			Status: types.IngestionProbeStatusFailed,
			Reason: probeFailureReason(err),
		}
		return
	}
	analysis.ProbeQuestions = probes
}

func (s *knowledgeService) generateIngestionProbeQuestions(
	ctx context.Context,
	run ingestionAdvisorRun,
) (*types.IngestionProbeQuestions, error) {
	if s == nil || s.modelService == nil {
		return &types.IngestionProbeQuestions{
			Status: types.IngestionProbeStatusSkipped,
			Reason: "model_unavailable",
		}, nil
	}
	if run.KB == nil || strings.TrimSpace(run.KB.SummaryModelID) == "" {
		return &types.IngestionProbeQuestions{
			Status: types.IngestionProbeStatusSkipped,
			Reason: "model_unavailable",
		}, nil
	}
	if utf8.RuneCountInString(strings.TrimSpace(run.Content)) == 0 {
		return &types.IngestionProbeQuestions{
			Status: types.IngestionProbeStatusSkipped,
			Reason: "empty_content",
		}, nil
	}
	if ctx.Err() != nil {
		return &types.IngestionProbeQuestions{
			Status: types.IngestionProbeStatusSkipped,
			Reason: "context_canceled",
		}, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, ingestionProbeTimeout)
	defer cancel()
	chatModel, err := s.modelService.GetChatModel(probeCtx, run.KB.SummaryModelID)
	if err != nil {
		return nil, err
	}
	probeCtx = types.WithLLMCallMetadata(probeCtx, "ingestion_probe_questions", "")
	report, err := evalquery.Generate(evalquery.GenerateRequest{
		Markdown:    run.Content,
		Count:       ingestionProbeQuestionCount,
		MaxAttempts: ingestionProbeMaxAttempts,
		Seed:        1,
		Chat:        ingestionProbeChat{ctx: probeCtx, model: chatModel},
	})
	if err != nil {
		return nil, err
	}
	if len(report.Accepted) == 0 {
		return &types.IngestionProbeQuestions{
			Status: types.IngestionProbeStatusFailed,
			Reason: "no_questions",
		}, nil
	}
	questions := make([]types.IngestionProbeQuestion, 0, len(report.Accepted))
	for _, item := range report.Accepted {
		questions = append(questions, types.IngestionProbeQuestion{
			Query:   item.Query,
			Excerpt: item.Excerpt,
			Marker:  item.Marker,
		})
	}
	return &types.IngestionProbeQuestions{
		Status:    types.IngestionProbeStatusGenerated,
		Questions: questions,
	}, nil
}

func probeFailureReason(err error) string {
	if err == nil {
		return "unknown"
	}
	if strings.Contains(strings.ToLower(err.Error()), "cancel") {
		return "context_canceled"
	}
	return "chat_error"
}

type ingestionProbeChat struct {
	ctx   context.Context
	model chat.Chat
}

func (c ingestionProbeChat) Complete(prompt string) (string, error) {
	thinking := false
	response, err := c.model.Chat(c.ctx, []chat.Message{
		{Role: "user", Content: prompt},
	}, &chat.ChatOptions{
		Temperature:         0,
		MaxCompletionTokens: 800,
		Thinking:            &thinking,
	})
	if err != nil {
		return "", err
	}
	if response == nil {
		return "", nil
	}
	return response.Content, nil
}
