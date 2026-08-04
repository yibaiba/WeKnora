package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sirupsen/logrus"
)

const sensitiveToolError = `invalid regex query "customer-secret"`

type sensitiveLoggingTool struct{}

func (sensitiveLoggingTool) Name() string        { return "sensitive_logging" }
func (sensitiveLoggingTool) Description() string { return "test sensitive tool logging" }
func (sensitiveLoggingTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false}`)
}
func (sensitiveLoggingTool) Execute(ctx context.Context, _ json.RawMessage) (*types.ToolResult, error) {
	logger.Errorf(ctx, "tool internal error: %s", sensitiveToolError)
	return &types.ToolResult{Success: false, Error: sensitiveToolError}, nil
}

func TestRedactedToolExecutionKeepsDetailsForModelButNotLogs(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterTool(sensitiveLoggingTool{})
	ctx, logs := toolTestLogContext()
	ctx = WithRedactedToolPayloads(ctx)

	result, err := registry.ExecuteTool(ctx, "sensitive_logging", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ExecuteTool error: %v", err)
	}
	if !strings.Contains(result.Error, sensitiveToolError) {
		t.Fatalf("model error lost original detail: %q", result.Error)
	}
	if strings.Contains(logs.String(), "customer-secret") {
		t.Fatalf("sensitive tool detail leaked to logs: %s", logs.String())
	}
	if !strings.Contains(logs.String(), RedactedToolFailureMessage) {
		t.Fatalf("redacted failure summary missing from logs: %s", logs.String())
	}
}

func TestOrdinaryToolExecutionKeepsExistingLogDetails(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterTool(sensitiveLoggingTool{})
	ctx, logs := toolTestLogContext()

	_, err := registry.ExecuteTool(ctx, "sensitive_logging", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ExecuteTool error: %v", err)
	}
	if !strings.Contains(logs.String(), "customer-secret") {
		t.Fatalf("ordinary tool log was unexpectedly redacted: %s", logs.String())
	}
}

func toolTestLogContext() (context.Context, *bytes.Buffer) {
	var output bytes.Buffer
	log := logrus.New()
	log.SetOutput(&output)
	log.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true})
	ctx := context.WithValue(context.Background(), types.LoggerContextKey, logrus.NewEntry(log))
	return ctx, &output
}
