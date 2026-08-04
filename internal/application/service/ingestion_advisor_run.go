package service

import (
	"fmt"
	"sort"
	"strings"
	"sync"

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
			Round: event.Round, ToolName: event.ToolName,
			Status: event.Status, DurationMS: event.DurationMS,
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
		reasons = append(reasons, analysis.ReasonCodes...)
	}
	return &types.IngestionAdvisorResult{
		Analysis: analysis, Candidates: session.candidateSnapshot(),
		SelectedCandidateID:  session.selectedCandidateID(),
		SelectionReasonCodes: reasons, AgentRun: run,
	}
}

func validateIngestionAgentOutcome(state *types.AgentState, session *ingestionAgentSession) error {
	if err := firstFailedIngestionCoreTool(state); err != nil {
		return err
	}
	if state == nil {
		return fmt.Errorf("文档分析 Agent 未返回运行状态")
	}
	if state.TerminatedByTool == submitIngestionDecisionTool && session.decisionSnapshot() != nil {
		return ValidateIngestionAnalysis(session.decisionSnapshot())
	}
	if countAgentToolCalls(state) == 0 {
		return fmt.Errorf("模型不支持原生工具调用或未调用任何工具")
	}
	if state.StopReason == "max_iterations" {
		return fmt.Errorf("文档分析 Agent 达到 %d 轮上限但未提交决策", ingestionAdvisorMaxRounds)
	}
	return fmt.Errorf("文档分析 Agent 未通过 submit_ingestion_decision 提交决策")
}

func firstFailedIngestionCoreTool(state *types.AgentState) error {
	if state == nil {
		return nil
	}
	for _, step := range state.RoundSteps {
		for _, call := range step.ToolCalls {
			if !isIngestionCoreTool(call.Name) || (call.Result != nil && call.Result.Success) {
				continue
			}
			message := ""
			if call.Result != nil {
				message = call.Result.Error
			}
			if message == "" {
				message = "工具未返回成功结果"
			}
			return fmt.Errorf("入库核心工具 %s 执行失败: %s", call.Name, message)
		}
	}
	return nil
}

func isIngestionCoreTool(name string) bool {
	return name == inspectIngestionDocumentTool || name == previewIngestionChunkingTool ||
		name == submitIngestionDecisionTool
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

func classifyIngestionAgentExecutionError(err error) error {
	message := strings.ToLower(err.Error())
	unsupported := strings.Contains(message, "tool") &&
		(strings.Contains(message, "unsupported") || strings.Contains(message, "not support"))
	if unsupported {
		return fmt.Errorf("模型不支持原生工具调用: %w", err)
	}
	return fmt.Errorf("文档分析 Agent 执行失败: %w", err)
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
