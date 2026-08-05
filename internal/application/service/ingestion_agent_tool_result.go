package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

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
	return &types.ToolResult{
		Success: false,
		Error:   err.Error(),
		Failure: safeIngestionToolFailure(err),
	}, nil
}

func ingestionToolJSON(value any, data map[string]interface{}) (*types.ToolResult, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &types.ToolResult{Success: true, Output: string(payload), Data: data}, nil
}
