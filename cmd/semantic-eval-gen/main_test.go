package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJoinAPIURL(t *testing.T) {
	require.Equal(t, "https://api.openai.com/v1/chat/completions", joinAPIURL("https://api.openai.com/v1", "chat/completions"))
	require.Equal(t, "https://api.openai.com/v1/chat/completions", joinAPIURL("https://api.openai.com", "/chat/completions"))
	require.Equal(t, "http://127.0.0.1:8000/v1/embeddings", joinAPIURL("http://127.0.0.1:8000/", "embeddings"))
}

func TestRunGeneratesOrdinaryQueries(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.md")
	require.NoError(t, os.WriteFile(source, []byte("The calibration interval remains thirty days for all regional sensors.\n"), 0o644))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			_, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"question\":\"How long is the calibration interval for regional sensors?\",\"references\":[\"The calibration interval remains thirty days for all regional sensors.\"]}"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	err := run([]string{
		"-source", source,
		"-count", "1",
		"-seed", "1",
		"-chat-base-url", server.URL,
		"-chat-api-key", "test-key",
		"-chat-model", "test-chat",
	}, stdout, stderr, func(string) string { return "" })
	require.NoError(t, err)
	require.Contains(t, stderr.String(), "accepted=1")
	require.Contains(t, stderr.String(), "skipping cosine")
	var fixture struct {
		Queries []struct {
			Class  string `json:"class"`
			Kind   string `json:"kind"`
			Query  string `json:"query"`
			Marker string `json:"marker"`
		} `json:"queries"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout.String()), &fixture))
	require.Len(t, fixture.Queries, 1)
	require.Equal(t, "ordinary", fixture.Queries[0].Class)
	require.Equal(t, "text", fixture.Queries[0].Kind)
	require.NotEmpty(t, fixture.Queries[0].Marker)
}

func TestRunMergesStructureAndWritesOut(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "fixture.json")
	outPath := filepath.Join(dir, "out.json")
	require.NoError(t, os.WriteFile(fixturePath, []byte(`{
  "chunk_size": 120,
  "chunk_overlap": 16,
  "markdown": "The calibration interval remains thirty days for all regional sensors.\nThe archive verification job runs every Friday.\n",
  "queries": [{"class":"structure","kind":"table","query":"Authentication Result","marker":"TC-LOGIN","required_context":"| Case |","excerpt":"| TC-LOGIN |"}]
}`), 0o644))
	var chatCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			chatCalls++
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"question\":\"How long is the calibration interval for regional sensors?\",\"references\":[\"The calibration interval remains thirty days for all regional sensors.\"]}"}}]}`))
		case strings.HasSuffix(r.URL.Path, "/embeddings"):
			var req struct {
				Input []string `json:"input"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			var b strings.Builder
			b.WriteString(`{"data":[`)
			for i := range req.Input {
				if i > 0 {
					b.WriteByte(',')
				}
				fmt.Fprintf(&b, `{"index":%d,"embedding":[1,0]}`, i)
			}
			b.WriteString(`]}`)
			_, _ = w.Write([]byte(b.String()))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	stderr := &strings.Builder{}
	err := run([]string{
		"-fixture", fixturePath,
		"-out", outPath,
		"-count", "1",
		"-seed", "1",
		"-chat-base-url", server.URL,
		"-chat-model", "test-chat",
		"-embed-base-url", server.URL,
		"-embed-model", "test-embed",
	}, io.Discard, stderr, func(string) string { return "" })
	require.NoError(t, err)
	require.Equal(t, 1, chatCalls)
	require.Contains(t, stderr.String(), "warning: markers not in markdown: TC-LOGIN")
	raw, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"class": "structure"`)
	require.Contains(t, string(raw), `"class": "ordinary"`)
	require.Contains(t, string(raw), "How long is the calibration interval")
}

func TestRunRefusesEmptyReplace(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.md")
	require.NoError(t, os.WriteFile(source, []byte("The calibration interval remains thirty days for all regional sensors.\n"), 0o644))
	outPath := filepath.Join(t.TempDir(), "out.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not json"}}]}`))
	}))
	t.Cleanup(server.Close)
	err := run([]string{
		"-source", source,
		"-out", outPath,
		"-count", "1",
		"-replace-ordinary",
		"-chat-base-url", server.URL,
		"-chat-model", "test-chat",
	}, io.Discard, io.Discard, func(string) string { return "" })
	require.Error(t, err)
	require.Contains(t, err.Error(), "refusing to replace")
	_, statErr := os.Stat(outPath)
	require.Error(t, statErr)
}

func TestRunRequiresChatAndInput(t *testing.T) {
	err := run(nil, io.Discard, io.Discard, func(string) string { return "" })
	require.Error(t, err)
	err = run([]string{"-source", "missing.md"}, io.Discard, io.Discard, func(string) string { return "" })
	require.Error(t, err)
	require.Contains(t, err.Error(), "chat endpoint required")
}
