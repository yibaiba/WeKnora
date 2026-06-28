package wecom_wedrive

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type flexibleInt64 int64

func (v *flexibleInt64) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == `""` {
		*v = 0
		return nil
	}
	if len(data) > 0 && data[0] == '"' {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		if raw == "" {
			*v = 0
			return nil
		}
		i, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("flexible int: expected integer string, got %q", raw)
		}
		*v = flexibleInt64(i)
		return nil
	}
	var i int64
	if err := json.Unmarshal(data, &i); err != nil {
		return fmt.Errorf("flexible int: expected integer or string, got %s: %w", data, err)
	}
	*v = flexibleInt64(i)
	return nil
}

type fileListCollection []WeDriveFile

func (c *fileListCollection) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == `{}` {
		*c = nil
		return nil
	}
	if len(data) > 0 && data[0] == '[' {
		var files []WeDriveFile
		if err := json.Unmarshal(data, &files); err != nil {
			return err
		}
		*c = files
		return nil
	}
	var wrapped struct {
		Item []WeDriveFile `json:"item"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	*c = wrapped.Item
	return nil
}
