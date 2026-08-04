package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/chat"
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

	mu             sync.RWMutex
	terminationHit bool
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

func configuredTerminationTool(ctx context.Context) string {
	runtime := taskRuntimeFromContext(ctx)
	if runtime == nil {
		return ""
	}
	return runtime.options.TerminationTool
}

func finalRoundTool(ctx context.Context, round, maxIterations int) string {
	runtime := taskRuntimeFromContext(ctx)
	if runtime == nil || round != maxIterations {
		return ""
	}
	return strings.TrimSpace(runtime.options.FinalRoundTool)
}

func taskToolsForRound(
	ctx context.Context,
	tools []chat.Tool,
	round, maxIterations int,
) ([]chat.Tool, error) {
	required := finalRoundTool(ctx, round, maxIterations)
	if required == "" {
		return tools, nil
	}
	for _, tool := range tools {
		if tool.Function.Name == required {
			return []chat.Tool{tool}, nil
		}
	}
	return nil, fmt.Errorf("agent task final-round tool %q is not registered", required)
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
	if runtime == nil || !success || runtime.options.TerminationTool != toolName {
		return
	}
	runtime.mu.Lock()
	runtime.terminationHit = true
	runtime.mu.Unlock()
}

func terminationToolHit(ctx context.Context) (string, bool) {
	runtime := taskRuntimeFromContext(ctx)
	if runtime == nil {
		return "", false
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.options.TerminationTool, runtime.terminationHit
}
