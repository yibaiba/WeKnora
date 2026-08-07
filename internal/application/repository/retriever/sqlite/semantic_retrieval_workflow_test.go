//go:build sqlite_fts5

package sqlite

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSemanticRetrievalWorkflowContract(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..")
	tests := []struct {
		path     string
		required []string
	}{
		{
			path: filepath.Join(root, ".github", "workflows", "app.yml"),
			required: []string{
				"go test -timeout=60s", "go test -tags sqlite_fts5 -timeout=60s",
				"semantic_retrieval_eval.json",
			},
		},
		{
			path: filepath.Join(root, ".github", "workflows", "docreader.yml"),
			required: []string{
				"Run DocReader Python test suite", "python -m unittest discover",
				"go test -timeout=60s ./docreader/client ./docreader/proto",
			},
		},
		{
			path: filepath.Join(root, ".github", "workflows", "semantic-retrieval-eval.yml"),
			required: []string{
				"schedule:", "NOT_EXECUTED", "sqlite_fts5 retrieval_eval_external",
				"SEMANTIC_EVAL_EMBEDDING_API_KEY", "SEMANTIC_EVAL_RERANK_API_KEY",
			},
		},
	}
	for _, test := range tests {
		raw, err := os.ReadFile(test.path)
		require.NoError(t, err)
		for _, expected := range test.required {
			require.Contains(t, string(raw), expected, "%s missing workflow contract", test.path)
		}
	}
}
