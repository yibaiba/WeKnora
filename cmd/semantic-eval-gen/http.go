package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultHTTPTimeout = 60 * time.Second
	maxErrorBody       = 4096
)

func joinAPIURL(base, path string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	path = strings.TrimLeft(path, "/")
	if strings.HasSuffix(base, "/v1") || strings.Contains(base, "/v1/") {
		return base + "/" + path
	}
	return base + "/v1/" + path
}

type openAIChat struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
}

func newOpenAIChat(baseURL, apiKey, model string) *openAIChat {
	return &openAIChat{
		endpoint: joinAPIURL(baseURL, "chat/completions"),
		apiKey:   apiKey,
		model:    model,
		client:   &http.Client{Timeout: defaultHTTPTimeout},
	}
}

func (c *openAIChat) Complete(prompt string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":       c.model,
		"temperature": 0,
		"max_tokens":  1024,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
	if err != nil {
		return "", err
	}
	raw, err := c.post(body)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("decode chat response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("chat api: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("chat api returned no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

func (c *openAIChat) post(body []byte) ([]byte, error) {
	return postJSON(c.client, c.endpoint, c.apiKey, body)
}

type openAIEmbedder struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
}

func newOpenAIEmbedder(baseURL, apiKey, model string) *openAIEmbedder {
	return &openAIEmbedder{
		endpoint: joinAPIURL(baseURL, "embeddings"),
		apiKey:   apiKey,
		model:    model,
		client:   &http.Client{Timeout: defaultHTTPTimeout},
	}
}

func (c *openAIEmbedder) Embed(texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{
		"model": c.model,
		"input": texts,
	})
	if err != nil {
		return nil, err
	}
	raw, err := postJSON(c.client, c.endpoint, c.apiKey, body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, fmt.Errorf("embedding api: %s", parsed.Error.Message)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embedding api returned %d vectors, want %d", len(parsed.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for _, row := range parsed.Data {
		if row.Index < 0 || row.Index >= len(out) {
			return nil, fmt.Errorf("embedding api returned out-of-range index %d", row.Index)
		}
		if out[row.Index] != nil {
			return nil, fmt.Errorf("embedding api returned duplicate index %d", row.Index)
		}
		out[row.Index] = row.Embedding
	}
	for i, vec := range out {
		if vec == nil {
			return nil, fmt.Errorf("embedding api missing index %d", i)
		}
	}
	return out, nil
}

func postJSON(client *http.Client, endpoint, apiKey string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > maxErrorBody {
			msg = msg[:maxErrorBody]
		}
		return nil, fmt.Errorf("http %s: %s", resp.Status, msg)
	}
	return raw, nil
}
