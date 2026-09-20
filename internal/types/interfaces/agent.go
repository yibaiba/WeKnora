package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
)

// AgentStreamEvent represents a streaming event from the agent
type AgentStreamEvent struct {
	Type      string                 `json:"type"`      // "thought", "tool_call", "tool_result", "final_answer", "error", "references"
	Content   string                 `json:"content"`   // Incremental content
	Data      map[string]interface{} `json:"data"`      // Additional structured data
	Done      bool                   `json:"done"`      // Whether this is the last event
	Iteration int                    `json:"iteration"` // Current iteration number
}

// AgentTaskEvent is a redacted lifecycle event for non-chat Agent runs. It
// intentionally excludes model reasoning, tool arguments, and tool output.
type AgentTaskEvent struct {
	Kind       string
	ToolCallID string
	Round      int
	ToolName   string
	Status     string
	DurationMS int64
	Failure    *types.ToolFailure
}

// AgentTaskOptions configures a bounded, non-chat Agent run. Zero values keep
// the normal Agent defaults unless explicitly documented otherwise.
type AgentTaskOptions struct {
	SystemPrompt      string
	MaxIterations     int
	TerminationTool   string
	TerminationTools  []string
	SkipFinalAnswer   bool
	StructuredEventFn func(AgentTaskEvent)
}

// AgentTaskRequest groups task-only execution input without changing Execute.
type AgentTaskRequest struct {
	SessionID  string
	MessageID  string
	Query      string
	LLMContext []chat.Message
	Options    AgentTaskOptions
}

// AgentEngine defines the interface for agent execution engine
type AgentEngine interface {
	// Execute executes the agent with conversation history and returns a stream of events
	// imageURLs is optional - when provided, images are passed to the LLM as multimodal content
	Execute(
		ctx context.Context,
		sessionID, messageID, query string,
		llmContext []chat.Message,
		imageURLs ...[]string,
	) (*types.AgentState, error)

	// ExecuteTask runs the same ReAct loop with explicit task-only controls.
	ExecuteTask(ctx context.Context, request AgentTaskRequest) (*types.AgentState, error)

	// SetMemoryPrompt supplies the long-term memory envelope for this run.
	// It must be called before Execute; an empty string is a no-op.
	SetMemoryPrompt(prompt string)

	// SetSteerSink enables mid-run message injection for this run. Nil
	// (default) disables it; when set, the engine drains user-appended
	// messages at every round boundary and persists accepted ones through
	// the sink. Must be called before Execute.
	SetSteerSink(sink types.SteerSink)

	// SetContextCheckpointSink enables persisting compaction summaries that
	// end on a stored turn, so later turns start from them. Nil (default)
	// keeps compaction local to the turn. Must be called before Execute.
	SetContextCheckpointSink(sink types.ContextCheckpointSink)
}

// AgentService defines the interface for agent-related operations
type AgentService interface {
	// CreateAgentEngine creates an agent engine with the given configuration and EventBus.
	// Conversation history is loaded by the caller (see service.LoadAgentHistory) and
	// passed into AgentEngine.Execute; the engine itself is stateless across turns.
	CreateAgentEngine(
		ctx context.Context,
		config *types.AgentConfig,
		chatModel chat.Chat,
		rerankModel rerank.Reranker,
		eventBus *event.EventBus,
		sessionID, assistantMessageID string,
	) (AgentEngine, error)

	// ValidateConfig validates an agent configuration
	ValidateConfig(config *types.AgentConfig) error
}
