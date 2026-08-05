package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"golang.org/x/sync/errgroup"
)

// langfuseToolOutputPreview caps the Output field we send to Langfuse for a
// tool call. Tool outputs are already truncated by the registry to
// DefaultMaxToolOutput (16KB) before this point, but rendering 16KB in the
// Langfuse UI for every tool call is noisy. We keep a generous slice so the
// gist is preserved, and include the original length in metadata.
const langfuseToolOutputPreview = 4000

// truncateForLangfuse returns s truncated to at most n runes, with a "…"
// marker appended when truncated. Runes (not bytes) are used so multi-byte
// CJK content is never split mid-character.
func truncateForLangfuse(s string, n int) string {
	if n <= 0 || len(s) == 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// argKeys returns the sorted list of top-level keys in a tool's argument
// map. Used when we choose not to send the raw arguments to Langfuse
// (e.g. database_query's SQL) but still want to signal what was passed in.
func argKeys(args map[string]any) []string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// traceArgumentValue keeps valid model-emitted JSON structured in Langfuse
// while preserving malformed payloads verbatim for diagnosis.
func traceArgumentValue(raw string) interface{} {
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	return value
}

// buildToolSpanInput exposes both sides of the model-context boundary:
// model_arguments is exactly what the model emitted (including temporary
// handles), while resolved_arguments is the durable payload actually executed.
// Langfuse never performs mapping itself; it only observes the registry's audit.
func buildToolSpanInput(tc types.LLMToolCall, resolvedArgs map[string]any, sensitive bool) map[string]interface{} {
	modelArguments := tc.ModelArguments
	if modelArguments == "" {
		modelArguments = tc.Function.Arguments
	}
	resolution := tc.ArgumentResolution
	if resolution == "" {
		resolution = modelcontext.ArgumentResolutionUnchanged
	}
	if sensitive {
		modelArgKeys := []string(nil)
		if parsed, ok := traceArgumentValue(modelArguments).(map[string]interface{}); ok {
			modelArgKeys = argKeys(parsed)
		}
		return map[string]interface{}{
			"tool_call_id":            tc.ID,
			"model_arg_keys":          modelArgKeys,
			"resolved_arg_keys":       argKeys(resolvedArgs),
			"argument_resolution":     resolution,
			"unresolved_handle_count": len(tc.UnresolvedHandles),
			"args_redacted":           true,
		}
	}
	return map[string]interface{}{
		"tool_call_id":        tc.ID,
		"model_arguments":     traceArgumentValue(modelArguments),
		"resolved_arguments":  resolvedArgs,
		"argument_resolution": resolution,
		"unresolved_handles":  tc.UnresolvedHandles,
	}
}

// finishToolSpan serialises a completed tool call into a Langfuse span
// update. Extracted from runToolCall so the tool-call pipeline keeps
// a single assignment per line and the observability-specific logic
// (payload shaping, error classification) lives in one place.
func finishToolSpan(
	span *langfuse.Span,
	tc types.ToolCall,
	execErr error,
	durationMs int64,
	redacted bool,
) {
	if span == nil {
		return
	}
	success := tc.Result != nil && tc.Result.Success
	output := map[string]interface{}{
		"success":     success,
		"duration_ms": durationMs,
	}
	if tc.Result != nil && !redacted {
		if tc.Result.Output != "" {
			output["output"] = truncateForLangfuse(tc.Result.Output, langfuseToolOutputPreview)
			output["output_len"] = len(tc.Result.Output)
		}
		if tc.Result.Error != "" {
			output["error"] = tc.Result.Error
		}
		if len(tc.Result.Data) > 0 {
			// Data is structured but can be arbitrarily large (e.g. full
			// search-result payloads). Only report key shape so Langfuse
			// users see what was surfaced without blowing up trace size.
			output["data_keys"] = dataKeys(tc.Result.Data)
		}
		if len(tc.Result.Images) > 0 {
			output["image_count"] = len(tc.Result.Images)
		}
	}
	spanErr := toolSpanError(tc, execErr, redacted)
	span.Finish(output, map[string]interface{}{
		"success":     success,
		"duration_ms": durationMs,
		"redacted":    redacted,
	}, spanErr)
}

func toolSpanError(tc types.ToolCall, execErr error, redacted bool) error {
	if execErr == nil && (tc.Result == nil || tc.Result.Success) {
		return nil
	}
	if redacted {
		return errors.New(agenttools.RedactedToolFailureMessage)
	}
	if execErr != nil {
		return execErr
	}
	message := tc.Result.Error
	if message == "" {
		message = "tool returned success=false"
	}
	return errors.New(message)
}

// dataKeys returns the sorted top-level keys of a tool's Data map.
func dataKeys(data map[string]interface{}) []string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// toolDisplayNames maps internal tool names to user-friendly display labels.
var toolDisplayNames = map[string]string{
	agenttools.ToolThinking:            "深度思考",
	agenttools.ToolTodoWrite:           "制定计划",
	agenttools.ToolGrepChunks:          "关键词搜索",
	agenttools.ToolKnowledgeSearch:     "知识搜索",
	agenttools.ToolListKnowledgeChunks: "查看文档分块",
	agenttools.ToolQueryKnowledgeGraph: "查询知识图谱",
	agenttools.ToolGetDocumentInfo:     "获取文档信息",
	agenttools.ToolDatabaseQuery:       "查询数据",
	agenttools.ToolDataAnalysis:        "数据分析",
	agenttools.ToolDataSchema:          "查看数据结构",
	agenttools.ToolWebSearch:           "搜索网页",
	agenttools.ToolWebFetch:            "获取网页",
	agenttools.ToolExecuteSkillScript:  "执行技能脚本",
	agenttools.ToolReadSkill:           "读取技能",
}

// toolHintSensitiveArgs lists tools whose arguments should NOT be shown in hints
// (e.g., database_query exposes raw SQL which leaks implementation details).
var toolHintSensitiveArgs = map[string]bool{
	agenttools.ToolDatabaseQuery: true,
}

// formatToolHint returns a concise human-readable hint for a tool call, e.g. `搜索网页("query text")`.
// Uses display names instead of internal tool names, and hides sensitive arguments.
func formatToolHint(name string, args map[string]any) string {
	displayName := name
	if dn, ok := toolDisplayNames[name]; ok {
		displayName = dn
	}

	if len(args) == 0 || toolHintSensitiveArgs[name] {
		return displayName
	}
	for _, v := range args {
		if s, ok := v.(string); ok {
			if len(s) > 40 {
				s = s[:40] + "…"
			}
			return fmt.Sprintf(`%s("%s")`, displayName, s)
		}
	}
	return displayName
}

// executeToolCalls runs every tool call in the LLM response, appending results to step.ToolCalls.
// It also emits tool-call and tool-result events, and optionally runs reflection after each call.
// When ParallelToolCalls is enabled and there are 2+ tool calls, they execute concurrently.
func (e *AgentEngine) executeToolCalls(
	ctx context.Context, response *types.ChatResponse,
	step *types.AgentStep, iteration int, sessionID, assistantMessageID string,
) {
	if len(response.ToolCalls) == 0 {
		return
	}

	round := iteration + 1
	n := len(response.ToolCalls)
	logger.Infof(ctx, "[Agent][Round-%d] Executing %d tool call(s)", round, n)

	// A task submission is authoritative. Run termination calls before the
	// parallel batch so a successful submit does not wait for, or get negated
	// by, unrelated sibling work. Ordinary chat runs have no termination tool.
	if e.config.ParallelToolCalls && n >= 2 {
		batch := taskTerminationBatch{
			ctx: ctx, response: response, step: step, iteration: iteration,
			round: round, sessionID: sessionID, assistantMessageID: assistantMessageID,
		}
		remaining, found, terminated := e.executeTaskTerminationCalls(batch)
		if terminated {
			return
		}
		if found {
			e.executeRemainingParallel(batch, remaining)
			return
		}
		e.executeToolCallsParallel(ctx, response, step, iteration, sessionID, assistantMessageID)
		return
	}

	for i, tc := range response.ToolCalls {
		e.executeSingleToolCall(ctx, tc, i, step, iteration, round, sessionID, assistantMessageID)
		if _, terminated := terminationToolHit(ctx); terminated {
			return
		}
	}
}

type taskTerminationBatch struct {
	ctx                context.Context
	response           *types.ChatResponse
	step               *types.AgentStep
	iteration          int
	round              int
	sessionID          string
	assistantMessageID string
}

func (e *AgentEngine) executeTaskTerminationCalls(
	batch taskTerminationBatch,
) (remaining []types.LLMToolCall, found, terminated bool) {
	terminationTool := configuredTerminationTool(batch.ctx)
	if terminationTool == "" {
		return nil, false, false
	}
	remaining = make([]types.LLMToolCall, 0, len(batch.response.ToolCalls))
	for index, toolCall := range batch.response.ToolCalls {
		if toolCall.Function.Name != terminationTool {
			remaining = append(remaining, toolCall)
			continue
		}
		found = true
		e.executeSingleToolCall(
			batch.ctx, toolCall, index, batch.step, batch.iteration, batch.round,
			batch.sessionID, batch.assistantMessageID,
		)
		if _, terminated = terminationToolHit(batch.ctx); terminated {
			return nil, true, true
		}
	}
	return remaining, found, false
}

func (e *AgentEngine) executeRemainingParallel(
	batch taskTerminationBatch,
	remaining []types.LLMToolCall,
) {
	if len(remaining) == 0 {
		return
	}
	response := *batch.response
	response.ToolCalls = remaining
	e.executeToolCallsParallel(
		batch.ctx, &response, batch.step, batch.iteration,
		batch.sessionID, batch.assistantMessageID,
	)
}

// executeToolCallsParallel runs all tool calls concurrently using errgroup,
// collecting results in original order.
func (e *AgentEngine) executeToolCallsParallel(
	ctx context.Context, response *types.ChatResponse,
	step *types.AgentStep, iteration int, sessionID, assistantMessageID string,
) {
	round := iteration + 1
	n := len(response.ToolCalls)
	logger.Infof(ctx, "[Agent][Round-%d] Parallel execution of %d tool calls", round, n)

	results := make([]types.ToolCall, n)
	var mu sync.Mutex
	g, gCtx := errgroup.WithContext(ctx)

	for i, tc := range response.ToolCalls {
		i, tc := i, tc // capture loop vars
		g.Go(func() error {
			toolCall := e.runToolCall(gCtx, tc, i, iteration, round, sessionID, assistantMessageID)
			mu.Lock()
			results[i] = toolCall
			mu.Unlock()
			return nil // best-effort: don't cancel siblings on failure
		})
	}

	_ = g.Wait()

	// Append results and emit events in original order
	for _, toolCall := range results {
		step.ToolCalls = append(step.ToolCalls, toolCall)

		result := toolCall.Result
		if result == nil {
			result = &types.ToolResult{Success: false, Error: "no result"}
		}

		eventResult := taskSafeToolResult(ctx, result)
		e.eventBus.Emit(ctx, event.Event{
			ID:        toolCall.ID + "-tool-result",
			Type:      event.EventAgentToolResult,
			SessionID: sessionID,
			Data: event.AgentToolResultData{
				ToolCallID: toolCall.ID,
				ToolName:   toolCall.Name,
				Output:     eventResult.Output,
				Error:      eventResult.Error,
				Success:    eventResult.Success,
				Duration:   toolCall.Duration,
				Iteration:  iteration,
				Data:       eventResult.Data,
			},
		})

		e.eventBus.Emit(ctx, event.Event{
			ID:        toolCall.ID + "-tool-exec",
			Type:      event.EventAgentTool,
			SessionID: sessionID,
			Data: event.AgentActionData{
				Iteration:  iteration,
				ToolName:   toolCall.Name,
				ToolInput:  taskSafeToolArgs(ctx, toolCall.Args),
				ToolOutput: eventResult.Output,
				Success:    eventResult.Success,
				Error:      eventResult.Error,
				Duration:   toolCall.Duration,
			},
		})
	}
}

// executeSingleToolCall runs one tool call sequentially (original behavior).
func (e *AgentEngine) executeSingleToolCall(
	ctx context.Context, tc types.LLMToolCall, i int,
	step *types.AgentStep, iteration, round int, sessionID, assistantMessageID string,
) {
	toolCall := e.runToolCall(ctx, tc, i, iteration, round, sessionID, assistantMessageID)
	step.ToolCalls = append(step.ToolCalls, toolCall)

	result := toolCall.Result
	if result == nil {
		result = &types.ToolResult{Success: false, Error: "no result"}
	}

	eventResult := taskSafeToolResult(ctx, result)
	e.eventBus.Emit(ctx, event.Event{
		ID:        toolCall.ID + "-tool-result",
		Type:      event.EventAgentToolResult,
		SessionID: sessionID,
		Data: event.AgentToolResultData{
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			Output:     eventResult.Output,
			Error:      eventResult.Error,
			Success:    eventResult.Success,
			Duration:   toolCall.Duration,
			Iteration:  iteration,
			Data:       eventResult.Data,
		},
	})

	e.eventBus.Emit(ctx, event.Event{
		ID:        toolCall.ID + "-tool-exec",
		Type:      event.EventAgentTool,
		SessionID: sessionID,
		Data: event.AgentActionData{
			Iteration:  iteration,
			ToolName:   toolCall.Name,
			ToolInput:  taskSafeToolArgs(ctx, toolCall.Args),
			ToolOutput: eventResult.Output,
			Success:    eventResult.Success,
			Error:      eventResult.Error,
			Duration:   toolCall.Duration,
		},
	})
}

// runToolCall handles argument parsing, execution, logging, and pipeline events for a single tool call.
// It returns the completed ToolCall struct. Safe to call from multiple goroutines.
func (e *AgentEngine) runToolCall(
	ctx context.Context, tc types.LLMToolCall, i int,
	iteration, round int, sessionID, assistantMessageID string,
) (completed types.ToolCall) {
	tc.ID = agenttools.NormalizeToolCallID(tc.ID, tc.Function.Name, i)
	total := "?" // unknown in isolation; callers log the batch size
	toolTag := fmt.Sprintf("[Agent][Round-%d][Tool %s (%d/%s)]",
		round, tc.Function.Name, i+1, total)
	taskStart := time.Now()
	emitTaskEvent(ctx, interfaces.AgentTaskEvent{
		Kind: taskEventToolStarted, ToolCallID: tc.ID,
		Round: round, ToolName: tc.Function.Name, Status: "running",
	})
	defer func() {
		success := completed.Result != nil && completed.Result.Success
		markTerminationTool(ctx, tc.Function.Name, success)
		status := "failed"
		if success {
			status = "succeeded"
		}
		duration := completed.Duration
		if duration == 0 {
			duration = time.Since(taskStart).Milliseconds()
		}
		var failure *types.ToolFailure
		if completed.Result != nil {
			failure = cloneToolFailure(completed.Result.Failure)
		}
		emitTaskEvent(ctx, interfaces.AgentTaskEvent{
			Kind:       taskEventToolFinished,
			ToolCallID: tc.ID,
			Round:      round,
			ToolName:   tc.Function.Name,
			Status:     status,
			DurationMS: duration,
			Failure:    failure,
		})
	}()

	var args map[string]any
	argsStr := tc.Function.Arguments
	if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
		repaired := agenttools.RepairJSON(argsStr)
		if repairErr := json.Unmarshal([]byte(repaired), &args); repairErr != nil {
			logger.Errorf(ctx, "%s Failed to parse arguments (repair failed): %v", toolTag, err)
			return types.ToolCall{
				ID:               tc.ID,
				Name:             tc.Function.Name,
				Args:             map[string]any{"_raw": argsStr},
				ProviderMetadata: tc.ProviderMetadata,
				Result: &types.ToolResult{
					Success: false,
					Failure: &types.ToolFailure{
						Code: "TOOL_ARGUMENTS_INVALID", Constraint: "valid_json",
					},
					Error: fmt.Sprintf(
						"Failed to parse tool arguments: %v", err,
					) + "\n\n[Analyze the error above and try a different approach.]",
				},
			}
		}
		logger.Warnf(ctx, "%s Repaired malformed JSON arguments", toolTag)
		// The initial model-context pass could not inspect malformed JSON.
		// Decode the repaired payload before execution, while preserving the
		// exact provider payload already stored in tc.ModelArguments.
		decoded := tc
		decoded.ModelArguments = ""
		decoded.Function.Arguments = repaired
		decodedCalls := []types.LLMToolCall{decoded}
		e.modelContext.DecodeToolCalls(decodedCalls)
		tc.Function.Arguments = decodedCalls[0].Function.Arguments
		tc.ArgumentResolution = decodedCalls[0].ArgumentResolution
		tc.UnresolvedHandles = decodedCalls[0].UnresolvedHandles
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return types.ToolCall{
				ID:               tc.ID,
				Name:             tc.Function.Name,
				Args:             map[string]any{"_raw": tc.Function.Arguments},
				ProviderMetadata: tc.ProviderMetadata,
				Result: &types.ToolResult{
					Success: false,
					Failure: &types.ToolFailure{
						Code: "TOOL_ARGUMENTS_INVALID", Constraint: "valid_json",
					},
					Error: fmt.Sprintf("Failed to parse repaired tool arguments: %v", err),
				},
			}
		}
	}

	if !agenttools.ToolPayloadsRedacted(ctx) {
		logger.Debugf(ctx, "%s Args: %s", toolTag, tc.Function.Arguments)
	}

	toolCallStartTime := time.Now()

	// Emit tool hint for UI progress display
	toolHint := formatToolHint(tc.Function.Name, taskSafeToolArgs(ctx, args))
	e.eventBus.Emit(ctx, event.Event{
		ID:        tc.ID + "-tool-hint",
		Type:      event.EventAgentToolCall,
		SessionID: sessionID,
		Data: event.AgentToolCallData{
			ToolCallID: tc.ID,
			ToolName:   tc.Function.Name,
			Arguments:  taskSafeToolArgs(ctx, args),
			Iteration:  iteration,
			Hint:       toolHint,
		},
	})

	common.PipelineInfo(ctx, "Agent", "tool_call_start", map[string]interface{}{
		"iteration":    iteration,
		"round":        round,
		"tool":         tc.Function.Name,
		"tool_call_id": tc.ID,
		"tool_index":   fmt.Sprintf("%d/%s", i+1, total),
	})

	// Open a Langfuse span for the tool invocation so the Langfuse UI shows
	// trace → agent.execute → agent.round.N → agent.tool.<name>, alongside
	// any nested generations (embedding/rerank/VLM) that the tool itself
	// triggers. No-op when Langfuse is disabled.
	mgr := langfuse.GetManager()
	// database_query's SQL is treated as sensitive by the UI hint layer
	// (toolHintSensitiveArgs) because it exposes implementation details.
	// Mirror that policy for Langfuse: redact raw arguments to avoid
	// leaking raw SQL into the observability backend.
	redacted := agenttools.ToolPayloadsRedacted(ctx)
	toolSpanInput := buildToolSpanInput(tc, args, redacted || toolHintSensitiveArgs[tc.Function.Name])
	argumentResolution, _ := toolSpanInput["argument_resolution"].(string)
	toolCtx, toolSpan := mgr.StartSpan(ctx, langfuse.SpanOptions{
		Name:  "agent.tool." + tc.Function.Name,
		Input: toolSpanInput,
		Metadata: map[string]interface{}{
			"iteration":               iteration,
			"round":                   round,
			"tool_index":              i + 1,
			"tool_call_id":            tc.ID,
			"session_id":              sessionID,
			"argument_resolution":     argumentResolution,
			"unresolved_handle_count": len(tc.UnresolvedHandles),
		},
	})

	principal, _ := types.PrincipalFromContext(ctx)
	toolExecCtx := agenttools.WithToolExecContext(toolCtx, &agenttools.ToolExecContext{
		SessionID:          sessionID,
		AssistantMessageID: assistantMessageID,
		EventBus:           e.eventBus,
		ToolCallID:         tc.ID,
		UserID:             principal.StorageID(),
		// ApprovalCtx keeps the round-level ctx without the per-tool 60s timeout,
		// so MCP tool human-approval (issue #1173) can legitimately block longer.
		ApprovalCtx: toolCtx,
		ExecTimeout: defaultToolExecTimeout,
	})

	var result *types.ToolResult
	var err error
	if len(tc.UnresolvedHandles) > 0 {
		// A temporary handle is not an application identity. Fail before tool
		// execution so a hallucinated/stale cN/dN/bN/wN/iN/res:// token can
		// never reach persistence, an external service, or a routing decision.
		err = fmt.Errorf("tool arguments contain unresolved model handles: %v", tc.UnresolvedHandles)
	} else {
		execCtx, toolCancel := context.WithTimeout(toolExecCtx, defaultToolExecTimeout)
		result, err = e.toolRegistry.ExecuteTool(
			execCtx, tc.Function.Name,
			json.RawMessage(tc.Function.Arguments),
		)
		toolCancel()
	}
	duration := time.Since(toolCallStartTime).Milliseconds()

	toolCall := types.ToolCall{
		ID:               tc.ID,
		Name:             tc.Function.Name,
		Args:             args,
		Result:           result,
		Duration:         duration,
		ProviderMetadata: tc.ProviderMetadata,
	}

	if err != nil {
		logger.Errorf(ctx, "%s Failed in %dms: %s", toolTag, duration,
			agenttools.ToolErrorForObservability(ctx, err.Error()))
		toolCall.Result = &types.ToolResult{
			Success: false,
			Error:   err.Error(),
		}
	} else {
		success := result != nil && result.Success
		outputLen := 0
		if result != nil {
			outputLen = len(result.Output)
		}
		logger.Infof(ctx, "%s Completed in %dms: success=%v, output=%d chars",
			toolTag, duration, success, outputLen)
	}

	finishToolSpan(toolSpan, toolCall, err, duration, redacted)
	toolSuccess := toolCall.Result != nil && toolCall.Result.Success

	// Pipeline event for monitoring
	pipelineFields := map[string]interface{}{
		"iteration":    iteration,
		"round":        round,
		"tool":         tc.Function.Name,
		"tool_call_id": tc.ID,
		"duration_ms":  duration,
		"success":      toolSuccess,
	}
	if toolCall.Result != nil && toolCall.Result.Error != "" {
		pipelineFields["error"] = agenttools.ToolErrorForObservability(ctx, toolCall.Result.Error)
	}
	if err != nil {
		common.PipelineError(ctx, "Agent", "tool_call_result", pipelineFields)
	} else if toolSuccess {
		common.PipelineInfo(ctx, "Agent", "tool_call_result", pipelineFields)
	} else {
		common.PipelineWarn(ctx, "Agent", "tool_call_result", pipelineFields)
	}

	if !redacted && toolCall.Result != nil && toolCall.Result.Output != "" {
		preview := toolCall.Result.Output
		if len(preview) > 500 {
			preview = preview[:500] + "... (truncated)"
		}
		logger.Debugf(ctx, "%s Output preview:\n%s", toolTag, preview)
	}
	if !redacted && toolCall.Result != nil && toolCall.Result.Error != "" {
		logger.Debugf(ctx, "%s Tool error: %s", toolTag, toolCall.Result.Error)
	}

	return toolCall
}

func taskSafeToolArgs(ctx context.Context, args map[string]any) map[string]any {
	if agenttools.ToolPayloadsRedacted(ctx) {
		return nil
	}
	return args
}

func taskSafeToolResult(ctx context.Context, result *types.ToolResult) *types.ToolResult {
	if !agenttools.ToolPayloadsRedacted(ctx) || result == nil {
		return result
	}
	return &types.ToolResult{
		Success: result.Success,
		Error:   agenttools.ToolErrorForObservability(ctx, result.Error),
		Failure: cloneToolFailure(result.Failure),
	}
}

func cloneToolFailure(failure *types.ToolFailure) *types.ToolFailure {
	if failure == nil {
		return nil
	}
	copy := *failure
	return &copy
}
