package service

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

func validateIngestionAgentOutcome(state *types.AgentState, session *ingestionAgentSession) error {
	if state == nil {
		return fmt.Errorf("文档分析 Agent 未返回运行状态")
	}
	analysis := session.decisionSnapshot()
	if ingestionTerminationMatchesDecision(state.TerminatedByTool, analysis) {
		return validateIngestionAnalysisWithConstraints(analysis, session.constraints)
	}
	if err := unresolvedIngestionCoreToolFailure(state); err != nil {
		return err
	}
	if countAgentToolCalls(state) == 0 {
		return newIngestionAdvisorRunError(
			ingestionAdvisorErrorToolCalling, "模型不支持原生工具调用或未调用任何工具",
		)
	}
	if state.StopReason == "max_iterations" {
		return ingestionAdvisorMaxRoundsError()
	}
	return newIngestionAdvisorRunError(
		ingestionAdvisorErrorNotSubmitted, "文档分析 Agent 未通过允许的提交工具完成决策",
	)
}

func ingestionTerminationMatchesDecision(toolName string, analysis *types.IngestionAnalysis) bool {
	if analysis == nil {
		return false
	}
	if ingestionAppliedMode(analysis) == types.IngestionAppliedModeFallback {
		return toolName == submitIngestionFallbackTool
	}
	return toolName == submitIngestionDecisionTool
}

func ingestionAdvisorMaxRoundsError() error {
	return newIngestionAdvisorRunError(
		ingestionAdvisorErrorMaxRounds,
		"文档分析 Agent 达到 %d 轮上限但未提交决策", ingestionAdvisorMaxRounds,
	)
}

func unresolvedIngestionCoreToolFailure(state *types.AgentState) error {
	if state == nil {
		return nil
	}
	failures := ingestionUnresolvedCoreFailures{}
	for _, step := range state.RoundSteps {
		for index := range step.ToolCalls {
			failures.record(&step.ToolCalls[index])
		}
	}
	if failures.submit != nil {
		return ingestionCoreToolFailure(*failures.submit)
	}
	if failures.fallback != nil {
		return ingestionCoreToolFailure(*failures.fallback)
	}
	if failures.preview != nil {
		return ingestionCoreToolFailure(*failures.preview)
	}
	return nil
}

type ingestionUnresolvedCoreFailures struct {
	preview  *types.ToolCall
	submit   *types.ToolCall
	fallback *types.ToolCall
}

func (f *ingestionUnresolvedCoreFailures) record(call *types.ToolCall) {
	if !isIngestionCoreTool(call.Name) {
		return
	}
	succeeded := call.Result != nil && call.Result.Success
	switch call.Name {
	case previewIngestionChunkingTool:
		f.preview = failedIngestionToolCall(call, succeeded)
	case submitIngestionDecisionTool:
		f.submit = failedIngestionToolCall(call, succeeded)
	case submitIngestionFallbackTool:
		f.fallback = failedIngestionToolCall(call, succeeded)
	}
}

func failedIngestionToolCall(call *types.ToolCall, succeeded bool) *types.ToolCall {
	if succeeded {
		return nil
	}
	return call
}

func ingestionCoreToolFailure(call types.ToolCall) error {
	return newIngestionAdvisorRunError(
		ingestionAdvisorErrorCandidate, "%s", safeCoreToolFailureMessage(call),
	)
}

func safeCoreToolFailureMessage(call types.ToolCall) string {
	if call.Result == nil || call.Result.Failure == nil {
		return fmt.Sprintf("入库核心工具 %s 执行失败，详情已脱敏", call.Name)
	}
	failure := call.Result.Failure
	return fmt.Sprintf(
		"入库核心工具 %s 执行失败（错误码 %s，字段 %s，约束 %s）",
		call.Name, failureCode(failure),
		safeFailureValue(failureField(failure)), safeFailureValue(failureConstraint(failure)),
	)
}

func safeFailureValue(value string) string {
	if value == "" {
		return "未指定"
	}
	return value
}

func isIngestionCoreTool(name string) bool {
	return name == previewIngestionChunkingTool || name == submitIngestionDecisionTool ||
		name == submitIngestionFallbackTool
}

func countAgentToolCalls(state *types.AgentState) int {
	if state == nil {
		return 0
	}
	total := 0
	for _, step := range state.RoundSteps {
		total += len(step.ToolCalls)
	}
	return total
}
