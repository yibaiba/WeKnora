package wecom_wedrive

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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

type flexibleString string

func (v *flexibleString) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == `""` {
		*v = ""
		return nil
	}
	if len(data) > 0 && data[0] == '"' {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		*v = flexibleString(strings.TrimSpace(raw))
		return nil
	}
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch value := raw.(type) {
	case float64:
		if value == float64(int64(value)) {
			*v = flexibleString(strconv.FormatInt(int64(value), 10))
			return nil
		}
	case bool:
		if value {
			*v = "true"
		} else {
			*v = "false"
		}
		return nil
	}
	return fmt.Errorf("flexible string: expected string, integer, or bool, got %s", data)
}

type flexibleStringList []string

func (v *flexibleStringList) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == `""` {
		*v = nil
		return nil
	}
	var values []flexibleString
	if len(data) > 0 && data[0] == '[' {
		if err := json.Unmarshal(data, &values); err != nil {
			return err
		}
		out := make([]string, 0, len(values))
		for _, value := range values {
			if s := strings.TrimSpace(string(value)); s != "" {
				out = append(out, s)
			}
		}
		*v = out
		return nil
	}
	var single flexibleString
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	if s := strings.TrimSpace(string(single)); s != "" {
		*v = []string{s}
	} else {
		*v = nil
	}
	return nil
}
