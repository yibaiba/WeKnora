package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	modelollama "github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestResolveImageForOllamaRejectsInternalURL(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	if data := resolveImageForOllama("http://169.254.169.254/latest/meta-data/"); data != nil {
		t.Fatalf("resolveImageForOllama returned data for blocked internal URL")
	}
}

func TestOllamaChatClassifiesNetworkFailureAsTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := server.URL
	server.Close()
	t.Setenv("OLLAMA_BASE_URL", baseURL)
	t.Setenv("OLLAMA_OPTIONAL", "false")
	service, err := modelollama.GetOllamaService()
	require.NoError(t, err)
	model, err := NewOllamaChat(&ChatConfig{
		ModelName: "test-model", ModelID: "test-model",
	}, service)
	require.NoError(t, err)
	ctx := types.WithRedactedLLMTracePayloads(context.Background())

	_, err = model.Chat(ctx, []Message{{Role: "user", Content: "private request"}}, nil)

	details, ok := ProviderErrorDetails(err)
	require.True(t, ok)
	require.Equal(t, ProviderFailureTransport, details.Kind)
}

func TestOllamaChatDoesNotClassifyInvalidJSONAsTransport(t *testing.T) {
	server := newInvalidJSONOllamaServer(t)
	defer server.Close()
	t.Setenv("OLLAMA_BASE_URL", server.URL)
	t.Setenv("OLLAMA_OPTIONAL", "false")
	service, err := modelollama.GetOllamaService()
	require.NoError(t, err)
	model, err := NewOllamaChat(&ChatConfig{
		ModelName: "test-model", ModelID: "test-model",
	}, service)
	require.NoError(t, err)
	ctx := types.WithRedactedLLMTracePayloads(context.Background())

	_, err = model.Chat(ctx, []Message{{Role: "user", Content: "private request"}}, nil)

	details, ok := ProviderErrorDetails(err)
	require.True(t, ok)
	require.Equal(t, ProviderFailureUnknown, details.Kind)
}

func newInvalidJSONOllamaServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"test-model:latest"}]}`))
		case "/api/chat":
			_, _ = w.Write([]byte("not-json\n"))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestResolveImageForOllamaBlocksRedirectToInternalURL(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer server.Close()

	if data := resolveImageForOllama(server.URL); data != nil {
		t.Fatalf("resolveImageForOllama returned data after redirect to blocked internal URL")
	}
}
