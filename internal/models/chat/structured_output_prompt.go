package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/sashabaranov/go-openai"
)

const structuredOutputExampleMaxDepth = 8

func withStructuredOutputPrompt(
	messages []openai.ChatCompletionMessage,
	schema json.RawMessage,
) ([]openai.ChatCompletionMessage, error) {
	prompt, err := buildStructuredOutputPrompt(schema)
	if err != nil {
		return nil, err
	}
	result := append([]openai.ChatCompletionMessage(nil), messages...)
	for index := range result {
		if result[index].Role != openai.ChatMessageRoleSystem {
			continue
		}
		content := strings.TrimSpace(result[index].Content)
		if content != "" {
			content += "\n\n"
		}
		result[index].Content = content + prompt
		return result, nil
	}
	return append([]openai.ChatCompletionMessage{{
		Role: openai.ChatMessageRoleSystem, Content: prompt,
	}}, result...), nil
}

func buildStructuredOutputPrompt(schema json.RawMessage) (string, error) {
	var schemaValue any
	decoder := json.NewDecoder(bytes.NewReader(schema))
	decoder.UseNumber()
	if err := decoder.Decode(&schemaValue); err != nil {
		return "", fmt.Errorf("decode structured output schema: %w", err)
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		return "", err
	}
	compactSchema, err := json.Marshal(schemaValue)
	if err != nil {
		return "", fmt.Errorf("encode structured output schema: %w", err)
	}
	example, err := json.Marshal(schemaExample(schemaValue, 0))
	if err != nil {
		return "", fmt.Errorf("encode structured output example: %w", err)
	}
	return fmt.Sprintf(`结构化输出契约：只返回一个符合下列 JSON Schema 的 JSON 对象。不得输出 Markdown 代码块、解释、前后缀或额外 JSON 值。响应必须以 { 开始并以 } 结束。示例中的值仅表示结构，实际值必须来自当前任务。
<json_schema>%s</json_schema>
<json_example>%s</json_example>`, compactSchema, example), nil
}

func ensureSingleJSONValue(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("structured output schema contains multiple JSON values")
	} else if err != io.EOF {
		return fmt.Errorf("decode structured output schema suffix: %w", err)
	}
	return nil
}

func schemaExample(value any, depth int) any {
	if depth >= structuredOutputExampleMaxDepth {
		return nil
	}
	schema, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if enum := schemaEnumExample(schema); enum != nil {
		return enum
	}
	switch schemaType(schema) {
	case "object":
		return schemaObjectExample(schema, depth+1)
	case "array":
		return []any{schemaExample(schema["items"], depth+1)}
	case "integer", "number":
		return schemaNumberExample(schema)
	case "boolean":
		return true
	case "string":
		return "value"
	default:
		return schemaAnyOfExample(schema, depth+1)
	}
}

func schemaType(schema map[string]any) string {
	if value, ok := schema["type"].(string); ok {
		return value
	}
	if values, ok := schema["type"].([]any); ok {
		for _, value := range values {
			if text, ok := value.(string); ok && text != "null" {
				return text
			}
		}
	}
	return ""
}

func schemaObjectExample(schema map[string]any, depth int) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	example := make(map[string]any, len(keys))
	for _, key := range keys {
		example[key] = schemaExample(properties[key], depth)
	}
	return example
}

func schemaEnumExample(schema map[string]any) any {
	values, _ := schema["enum"].([]any)
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

func schemaNumberExample(schema map[string]any) any {
	if minimum, ok := schema["minimum"]; ok {
		return minimum
	}
	return json.Number("0")
}

func schemaAnyOfExample(schema map[string]any, depth int) any {
	values, _ := schema["anyOf"].([]any)
	if len(values) == 0 {
		return nil
	}
	return schemaExample(values[0], depth)
}
