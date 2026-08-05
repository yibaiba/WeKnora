package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

const (
	llmDebugChildModeEnv   = "WEKNORA_LLM_DEBUG_REDACTION_CHILD"
	llmDebugRequestSecret  = "private map document"
	llmDebugResponseSecret = "private reduce evidence"
)

type llmDebugPayloadChat struct{}

func (*llmDebugPayloadChat) GetModelName() string { return "debug-payload-model" }
func (*llmDebugPayloadChat) GetModelID() string   { return "debug-payload-model" }

func (*llmDebugPayloadChat) Chat(
	context.Context, []Message, *ChatOptions,
) (*types.ChatResponse, error) {
	return &types.ChatResponse{Content: llmDebugResponseSecret}, nil
}

func (*llmDebugPayloadChat) ChatStream(
	context.Context, []Message, *ChatOptions,
) (<-chan types.StreamResponse, error) {
	stream := make(chan types.StreamResponse, 1)
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: llmDebugResponseSecret}
	close(stream)
	return stream, nil
}

func TestDebugChatHonorsRedactedPayloadContext(t *testing.T) {
	if mode := os.Getenv(llmDebugChildModeEnv); mode != "" {
		runLLMDebugChild(t, mode == "redacted")
		return
	}

	redactedLogs := runLLMDebugSubprocess(t, "redacted")
	require.Empty(t, redactedLogs)
	ordinaryLogs := runLLMDebugSubprocess(t, "ordinary")
	require.Contains(t, ordinaryLogs, llmDebugRequestSecret)
	require.Contains(t, ordinaryLogs, llmDebugResponseSecret)
}

func runLLMDebugChild(t *testing.T, redacted bool) {
	t.Helper()
	ctx := context.Background()
	if redacted {
		ctx = types.WithRedactedLLMTracePayloads(ctx)
	}
	model := &debugChat{inner: &llmDebugPayloadChat{}}
	messages := []Message{{Role: "user", Content: llmDebugRequestSecret}}
	_, err := model.Chat(ctx, messages, nil)
	require.NoError(t, err)
	stream, err := model.ChatStream(ctx, messages, nil)
	require.NoError(t, err)
	for range stream {
	}
}

func runLLMDebugSubprocess(t *testing.T, mode string) string {
	t.Helper()
	directory := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestDebugChatHonorsRedactedPayloadContext$")
	command.Env = append(os.Environ(), llmDebugChildModeEnv+"="+mode, "LLM_DEBUG_LOG="+directory)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	return readLLMDebugDirectory(t, directory)
}

func readLLMDebugDirectory(t *testing.T, directory string) string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	var contents strings.Builder
	for _, entry := range entries {
		payload, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		require.NoError(t, err)
		contents.Write(payload)
	}
	return contents.String()
}
