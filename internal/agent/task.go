package agent

import (
	"context"
	"sync"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	taskEventRoundStarted = "round_started"
	taskEventToolStarted  = "tool_started"
	taskEventToolFinished = "tool_finished"
	taskEventRunFinished  = "run_finished"
)

type taskRuntimeKey struct{}

type taskRuntime struct {
	options interfaces.AgentTaskOptions

	mu           sync.RWMutex
	terminatedBy string
}

func withTaskRuntime(ctx context.Context, options interfaces.AgentTaskOptions) context.Context {
	return context.WithValue(ctx, taskRuntimeKey{}, &taskRuntime{options: options})
}

func taskRuntimeFromContext(ctx context.Context) *taskRuntime {
	runtime, _ := ctx.Value(taskRuntimeKey{}).(*taskRuntime)
	return runtime
}

func maxIterationsForRun(ctx context.Context, configured int) int {
	runtime := taskRuntimeFromContext(ctx)
	if runtime != nil && runtime.options.MaxIterations > 0 {
		return runtime.options.MaxIterations
	}
	return configured
}

func shouldSkipFinalAnswer(ctx context.Context) bool {
	runtime := taskRuntimeFromContext(ctx)
	return runtime != nil && runtime.options.SkipFinalAnswer
}

func configuredTerminationTools(ctx context.Context) []string {
	runtime := taskRuntimeFromContext(ctx)
	if runtime == nil {
		return nil
	}
	if len(runtime.options.TerminationTools) > 0 {
		return runtime.options.TerminationTools
	}
	if runtime.options.TerminationTool == "" {
		return nil
	}
	return []string{runtime.options.TerminationTool}
}

func isConfiguredTerminationTool(ctx context.Context, toolName string) bool {
	for _, configured := range configuredTerminationTools(ctx) {
		if configured == toolName {
			return true
		}
	}
	return false
}

func emitTaskEvent(ctx context.Context, event interfaces.AgentTaskEvent) {
	runtime := taskRuntimeFromContext(ctx)
	if runtime == nil || runtime.options.StructuredEventFn == nil {
		return
	}
	runtime.options.StructuredEventFn(event)
}

func markTerminationTool(ctx context.Context, toolName string, success bool) {
	runtime := taskRuntimeFromContext(ctx)
	if runtime == nil || !success || !isConfiguredTerminationTool(ctx, toolName) {
		return
	}
	runtime.mu.Lock()
	runtime.terminatedBy = toolName
	runtime.mu.Unlock()
}

func terminationToolHit(ctx context.Context) (string, bool) {
	runtime := taskRuntimeFromContext(ctx)
	if runtime == nil {
		return "", false
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.terminatedBy, runtime.terminatedBy != ""
}
