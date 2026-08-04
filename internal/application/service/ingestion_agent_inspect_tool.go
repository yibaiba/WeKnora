package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
)

func decodeIngestionToolInput(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("包含额外 JSON 值")
		}
		return err
	}
	return nil
}

func ingestionToolFailure(err error) (*types.ToolResult, error) {
	return &types.ToolResult{Success: false, Error: err.Error()}, nil
}

func ingestionToolJSON(value any, data map[string]interface{}) (*types.ToolResult, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &types.ToolResult{Success: true, Output: string(payload), Data: data}, nil
}

type inspectIngestionDocumentInput struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

type inspectIngestionDocumentOutput struct {
	Offset          int                          `json:"offset"`
	NextOffset      int                          `json:"next_offset"`
	TotalCharacters int                          `json:"total_characters"`
	HasMore         bool                         `json:"has_more"`
	Content         string                       `json:"content"`
	Statistics      types.DocumentStructureStats `json:"statistics"`
}

type inspectIngestionDocument struct {
	agenttools.BaseTool
	session *ingestionAgentSession
}

func newInspectIngestionDocument(session *ingestionAgentSession) *inspectIngestionDocument {
	return &inspectIngestionDocument{
		BaseTool: agenttools.NewBaseTool(inspectIngestionDocumentTool, "Read the current ingestion document by rune offset and inspect its full-document statistics.", json.RawMessage(`{"type":"object","properties":{"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":8000}},"required":["offset","limit"],"additionalProperties":false}`)),
		session:  session,
	}
}

func (t *inspectIngestionDocument) Execute(
	_ context.Context,
	raw json.RawMessage,
) (*types.ToolResult, error) {
	var input inspectIngestionDocumentInput
	if err := decodeIngestionToolInput(raw, &input); err != nil {
		return ingestionToolFailure(fmt.Errorf("读取文档参数无效: %w", err))
	}
	runes := []rune(t.session.content)
	if input.Offset < 0 || input.Offset > len(runes) {
		return ingestionToolFailure(fmt.Errorf("offset 必须在 0 到 %d 之间", len(runes)))
	}
	if input.Limit < 1 || input.Limit > maxIngestionInspectRunes {
		return ingestionToolFailure(fmt.Errorf("limit 必须在 1 到 %d 之间", maxIngestionInspectRunes))
	}
	end := min(input.Offset+input.Limit, len(runes))
	return ingestionToolJSON(inspectIngestionDocumentOutput{
		Offset: input.Offset, NextOffset: end, TotalCharacters: len(runes),
		HasMore: end < len(runes), Content: string(runes[input.Offset:end]),
		Statistics: t.session.profile.Statistics,
	}, map[string]interface{}{"offset": input.Offset, "next_offset": end, "has_more": end < len(runes)})
}
