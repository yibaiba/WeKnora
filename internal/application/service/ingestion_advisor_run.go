package service

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func newIngestionAgentRun(
	availableTools []string,
	warnings []types.IngestionAgentWarning,
) types.IngestionAgentRun {
	return types.IngestionAgentRun{
		MaxRounds: ingestionAdvisorMaxRounds, AvailableTools: append([]string(nil), availableTools...),
		Warnings: append([]types.IngestionAgentWarning(nil), warnings...),
	}
}

func ingestionProgressReceiver(progress func(types.IngestionAgentStep)) func(interfaces.AgentTaskEvent) {
	if progress == nil {
		return nil
	}
	var mu sync.Mutex
	return func(event interfaces.AgentTaskEvent) {
		if event.Kind != "tool_started" && event.Kind != "tool_finished" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		progress(types.IngestionAgentStep{
			ToolCallID: event.ToolCallID, Round: event.Round, ToolName: event.ToolName,
			Status: event.Status, DurationMS: event.DurationMS,
			FailureCode:       failureCode(event.Failure),
			FailureField:      failureField(event.Failure),
			FailureConstraint: failureConstraint(event.Failure),
		})
	}
}

func buildIngestionAgentRun(base types.IngestionAgentRun, state *types.AgentState) types.IngestionAgentRun {
	if state == nil {
		return base
	}
	base.ActualRounds = len(state.RoundSteps)
	base.StopReason = state.StopReason
	for _, step := range state.RoundSteps {
		base = appendIngestionAgentStep(base, step)
	}
	base.Warnings = sortedWarningCopy(base.Warnings)
	return base
}

func appendIngestionAgentStep(base types.IngestionAgentRun, step types.AgentStep) types.IngestionAgentRun {
	for _, call := range step.ToolCalls {
		entry := types.IngestionAgentStep{
			Round: step.Iteration + 1, ToolName: call.Name,
			Status: "failed", DurationMS: call.Duration,
		}
		if call.Result != nil {
			entry.FailureCode = failureCode(call.Result.Failure)
			entry.FailureField = failureField(call.Result.Failure)
			entry.FailureConstraint = failureConstraint(call.Result.Failure)
		}
		if call.Result != nil && call.Result.Success {
			entry.Status = "succeeded"
		}
		entry.CandidateID, entry.Score = candidateSummary(call.Result)
		base.Steps = append(base.Steps, entry)
		if entry.Status == "failed" && !isIngestionCoreTool(call.Name) {
			base.Warnings = appendAgentWarning(base.Warnings, types.IngestionAgentWarning{
				Code: "optional_tool_failed", Tool: call.Name,
				Message: "可选只读工具执行失败",
			})
		}
	}
	return base
}

func failureCode(failure *types.ToolFailure) string {
	safe := sanitizeIngestionToolFailure(failure)
	if safe == nil {
		return ""
	}
	return safe.Code
}

func failureField(failure *types.ToolFailure) string {
	safe := sanitizeIngestionToolFailure(failure)
	if safe == nil {
		return ""
	}
	return safe.Field
}

func failureConstraint(failure *types.ToolFailure) string {
	safe := sanitizeIngestionToolFailure(failure)
	if safe == nil {
		return ""
	}
	return safe.Constraint
}

func appendAgentWarning(
	warnings []types.IngestionAgentWarning,
	warning types.IngestionAgentWarning,
) []types.IngestionAgentWarning {
	for _, existing := range warnings {
		if existing.Code == warning.Code && existing.Tool == warning.Tool {
			return warnings
		}
	}
	return append(warnings, warning)
}

func candidateSummary(result *types.ToolResult) (string, float64) {
	if result == nil || result.Data == nil {
		return "", 0
	}
	id, _ := result.Data["candidate_id"].(string)
	score, _ := result.Data["score"].(float64)
	return id, score
}

func buildIngestionAdvisorResult(
	session *ingestionAgentSession,
	run types.IngestionAgentRun,
) *types.IngestionAdvisorResult {
	analysis := session.decisionSnapshot()
	reasons := []string(nil)
	if analysis != nil {
		reasons = session.selectionReasons()
		if ingestionAppliedMode(analysis) == types.IngestionAppliedModeFallback {
			reasons = append([]string(nil), analysis.FallbackReasonCodes...)
		}
	}
	return &types.IngestionAdvisorResult{
		Analysis: analysis, Candidates: session.candidateSnapshot(),
		SelectedCandidateID:  session.selectedCandidateID(),
		SelectionReasonCodes: reasons, AgentRun: run,
		SemanticDocument: session.semanticDocumentSnapshot(),
	}
}

func classifyIngestionAgentExecutionError(err error) error {
	code := ingestionAdvisorErrorExecution
	summary := "文档分析 Agent 执行失败"
	providerErr, typed := chat.ProviderErrorDetails(err)
	if typed && isToolCallingProviderParameter(providerErr.Parameter) {
		code = ingestionAdvisorErrorToolCalling
		summary = "供应商拒绝原生工具调用参数"
	}
	if !typed {
		return newIngestionAdvisorRunError(code, "%s，供应商错误详情已脱敏", summary)
	}
	return newIngestionAdvisorRunError(code, "%s%s", summary, ingestionProviderErrorSuffix(providerErr))
}

func ingestionProviderErrorSuffix(details chat.ProviderFailureDetails) string {
	parts := []string{"类型 " + string(details.Kind)}
	if details.StatusCode > 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", details.StatusCode))
	}
	if details.Parameter != "" {
		parts = append(parts, "参数 "+details.Parameter)
	}
	return "（" + strings.Join(parts, "，") + "）"
}

func isToolCallingProviderParameter(parameter string) bool {
	return parameter == "tools" || parameter == "tool_choice" || parameter == "parallel_tool_calls"
}

func sortedWarningCopy(warnings []types.IngestionAgentWarning) []types.IngestionAgentWarning {
	result := append([]types.IngestionAgentWarning(nil), warnings...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Tool == result[j].Tool {
			return result[i].Code < result[j].Code
		}
		return result[i].Tool < result[j].Tool
	})
	return result
}
