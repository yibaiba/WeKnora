package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const ingestionDocumentReduceInputMaxRunes = 16000

type ingestionDocumentEvidenceBatch struct {
	Level      int                         `json:"level"`
	GroupIndex int                         `json:"group_index"`
	Evidence   []ingestionDocumentEvidence `json:"evidence"`
}

type ingestionDocumentReduceGroup struct {
	Payload string
}

type ingestionDocumentReduceRequest struct {
	Model             chat.Chat
	Evidence          []ingestionDocumentEvidence
	CoveredCharacters int
	Progress          func(types.IngestionDocumentAnalysisProgress)
	RetryPolicy       ingestionDocumentAnalysisRetryPolicy
	Budget            ingestionDocumentAnalysisTokenBudget
}

type ingestionDocumentReduceLevelRequest struct {
	Model             chat.Chat
	Groups            []ingestionDocumentReduceGroup
	Level             int
	CoveredCharacters int
	Progress          func(types.IngestionDocumentAnalysisProgress)
	RetryPolicy       ingestionDocumentAnalysisRetryPolicy
	Budget            ingestionDocumentAnalysisTokenBudget
}

type ingestionDocumentReduceGroupRequest struct {
	Model       chat.Chat
	Group       ingestionDocumentReduceGroup
	Level       int
	Index       int
	RetryPolicy ingestionDocumentAnalysisRetryPolicy
}

func reduceIngestionDocument(
	ctx context.Context,
	request ingestionDocumentReduceRequest,
) (ingestionDocumentEvidence, error) {
	if len(request.Evidence) == 0 {
		return ingestionDocumentEvidence{}, newIngestionAdvisorRunError(
			ingestionAdvisorErrorDocumentAnalysis, "文档全文 Reduce 分析失败：没有可归并证据",
		)
	}
	current := append([]ingestionDocumentEvidence(nil), request.Evidence...)
	for level := 1; len(current) > 1; level++ {
		groups, err := groupIngestionDocumentEvidence(current, level)
		if err != nil {
			return ingestionDocumentEvidence{}, err
		}
		if len(groups) >= len(current) {
			return ingestionDocumentEvidence{}, newIngestionAdvisorRunError(
				ingestionAdvisorErrorDocumentAnalysis, "文档全文 Reduce 第 %d 层无法收敛", level,
			)
		}
		next, err := reduceIngestionDocumentLevel(ctx, ingestionDocumentReduceLevelRequest{
			Model: request.Model, Groups: groups, Level: level,
			CoveredCharacters: request.CoveredCharacters, Progress: request.Progress,
			RetryPolicy: request.RetryPolicy,
			Budget:      request.Budget,
		})
		if err != nil {
			return ingestionDocumentEvidence{}, err
		}
		current = next
	}
	return current[0], nil
}

func reduceIngestionDocumentLevel(
	ctx context.Context,
	request ingestionDocumentReduceLevelRequest,
) ([]ingestionDocumentEvidence, error) {
	started := time.Now()
	emitIngestionAnalysisProgress(request.Progress, ingestionAnalysisProgressWithBudget(types.IngestionDocumentAnalysisProgress{
		Phase: "reduce_document", Status: ingestionAnalysisProgressRunning,
		UnitCount: len(request.Groups), Level: request.Level,
		CoveredCharacters: request.CoveredCharacters,
	}, request.Budget))
	results := make([]ingestionDocumentEvidence, 0, len(request.Groups))
	retryCount := 0
	for index, group := range request.Groups {
		result, attempts, err := reduceIngestionDocumentGroup(ctx, ingestionDocumentReduceGroupRequest{
			Model: request.Model, Group: group, Level: request.Level, Index: index,
			RetryPolicy: request.RetryPolicy,
		})
		retryCount += max(attempts-1, 0)
		if err != nil {
			failure := ingestionDocumentAnalysisFailureDetails(err)
			emitReduceProgress(request.Progress, types.IngestionDocumentAnalysisProgress{
				Phase: "reduce_document", Status: ingestionAnalysisProgressFailed,
				UnitCount: len(request.Groups), Completed: len(results),
				Level: request.Level, CoveredCharacters: request.CoveredCharacters, Failed: true,
				RetryCount: retryCount, FailedUnitAttempts: failure.Attempts,
				FailureKind: failure.Kind, FailedUnit: failure.Unit,
				ProviderFailureKind: failure.ProviderKind,
				HTTPStatus:          failure.HTTPStatus, FailureParameter: failure.Parameter,
			}, request.Budget, started)
			return nil, err
		}
		results = append(results, result)
	}
	emitReduceProgress(request.Progress, types.IngestionDocumentAnalysisProgress{
		Phase: "reduce_document", Status: ingestionAnalysisProgressSucceeded,
		UnitCount: len(request.Groups), Completed: len(results),
		Level: request.Level, CoveredCharacters: request.CoveredCharacters,
		RetryCount: retryCount,
	}, request.Budget, started)
	return results, nil
}

func reduceIngestionDocumentGroup(
	ctx context.Context,
	request ingestionDocumentReduceGroupRequest,
) (ingestionDocumentEvidence, int, error) {
	if request.Model == nil {
		return ingestionDocumentEvidence{}, 0,
			documentAnalysisFailureWithAttempts(documentAnalysisFailureRequest{
				Stage: fmt.Sprintf("Reduce 第 %d 层", request.Level),
				Unit:  request.Index, Cause: fmt.Errorf("模型未配置"),
			})
	}
	callCtx := sensitiveIngestionLLMContext(ctx, types.LLMCallPurposeIngestionDocumentReduce)
	call, err := callIngestionDocumentAnalysis(callCtx, ingestionDocumentAnalysisCall{
		Model: request.Model, Messages: []chat.Message{
			{Role: "system", Content: ingestionDocumentReduceSystemPrompt},
			{Role: "user", Content: request.Group.Payload},
		}, Options: ingestionDocumentAnalysisOptions(),
	}, request.RetryPolicy)
	if err != nil {
		return ingestionDocumentEvidence{}, call.Attempts,
			documentAnalysisFailureWithAttempts(documentAnalysisFailureRequest{
				Stage: fmt.Sprintf("Reduce 第 %d 层", request.Level),
				Unit:  request.Index, Cause: err, Attempts: call.Attempts,
			})
	}
	result, err := decodeIngestionDocumentEvidence(call.Response)
	if err != nil {
		return ingestionDocumentEvidence{}, call.Attempts,
			invalidDocumentAnalysisFailureWithAttempts(
				fmt.Sprintf("Reduce 第 %d 层", request.Level), request.Index, call.Attempts,
			)
	}
	return result, call.Attempts, nil
}

func groupIngestionDocumentEvidence(
	evidence []ingestionDocumentEvidence,
	level int,
) ([]ingestionDocumentReduceGroup, error) {
	groups := make([]ingestionDocumentReduceGroup, 0, (len(evidence)+1)/2)
	current := make([]ingestionDocumentEvidence, 0)
	for _, item := range evidence {
		candidate := append(append([]ingestionDocumentEvidence(nil), current...), item)
		payload, err := serializeIngestionDocumentEvidenceBatch(candidate, level, len(groups))
		if err != nil {
			return nil, err
		}
		if utf8.RuneCountInString(payload) <= ingestionDocumentReduceInputMaxRunes {
			current = candidate
			continue
		}
		if len(current) == 0 {
			return nil, reduceGroupingFailure(level)
		}
		group, err := newIngestionDocumentReduceGroup(current, level, len(groups))
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
		current = []ingestionDocumentEvidence{item}
	}
	if len(current) > 0 {
		group, err := newIngestionDocumentReduceGroup(current, level, len(groups))
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	for _, group := range groups {
		if utf8.RuneCountInString(group.Payload) > ingestionDocumentReduceInputMaxRunes {
			return nil, reduceGroupingFailure(level)
		}
	}
	return groups, nil
}

func newIngestionDocumentReduceGroup(
	evidence []ingestionDocumentEvidence,
	level int,
	index int,
) (ingestionDocumentReduceGroup, error) {
	payload, err := serializeIngestionDocumentEvidenceBatch(evidence, level, index)
	if err != nil {
		return ingestionDocumentReduceGroup{}, err
	}
	return ingestionDocumentReduceGroup{
		Payload: payload,
	}, nil
}

func serializeIngestionDocumentEvidenceBatch(
	evidence []ingestionDocumentEvidence,
	level int,
	index int,
) (string, error) {
	payload, err := json.Marshal(ingestionDocumentEvidenceBatch{
		Level: level, GroupIndex: index, Evidence: evidence,
	})
	if err != nil {
		return "", newIngestionAdvisorRunError(
			ingestionAdvisorErrorDocumentAnalysis, "文档全文 Reduce 第 %d 层输入序列化失败", level,
		)
	}
	return string(payload), nil
}

func reduceGroupingFailure(level int) error {
	return newIngestionAdvisorRunError(
		ingestionAdvisorErrorDocumentAnalysis,
		"文档全文 Reduce 第 %d 层输入超过 %d rune 上限", level, ingestionDocumentReduceInputMaxRunes,
	)
}

func emitReduceProgress(
	progress func(types.IngestionDocumentAnalysisProgress),
	event types.IngestionDocumentAnalysisProgress,
	budget ingestionDocumentAnalysisTokenBudget,
	started time.Time,
) {
	event.DurationMS = time.Since(started).Milliseconds()
	emitIngestionAnalysisProgress(progress, ingestionAnalysisProgressWithBudget(event, budget))
}

const ingestionDocumentReduceSystemPrompt = `你是文档全文分析器的 Reduce 阶段。按输入顺序归并全部证据，输出与 Map 相同的严格 JSON，不得输出 Markdown 或额外字段。
保留跨单元的主旨、文档类型和内容模式候选，并从 Schema 枚举归并主导结构、边界优先级与风险；不得输出源位置或最终边界，不得丢弃尾部证据或补充输入之外的事实。`
