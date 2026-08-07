//go:build sqlite_fts5

package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

const semanticRetrievalTopK = 5

const (
	semanticQueryClassStructure = "structure"
	semanticQueryClassOrdinary  = "ordinary"
)

type semanticRetrievalFixture struct {
	ChunkSize    int                      `json:"chunk_size"`
	ChunkOverlap int                      `json:"chunk_overlap"`
	Markdown     string                   `json:"markdown"`
	Queries      []semanticRetrievalQuery `json:"queries"`
}

type semanticRetrievalQuery struct {
	Class           string `json:"class"`
	Kind            string `json:"kind"`
	Query           string `json:"query"`
	Marker          string `json:"marker"`
	RequiredContext string `json:"required_context"`
}

type indexedSemanticChunks struct {
	knowledgeBaseID  string
	metricIDs        map[string]int
	relevantIDs      map[string]int
	contextByChunkID map[string]string
}

type semanticEvalIndexRequest struct {
	repository *sqliteRepository
	prefix     string
	chunks     []chunker.Chunk
	queries    []semanticRetrievalQuery
	contexts   []string
	embeddings [][]float32
}

type semanticRecallRequest struct {
	repository          *sqliteRepository
	index               indexedSemanticChunks
	queries             []semanticRetrievalQuery
	requireStructureHit bool
	retrieve            func(semanticRetrievalQuery) ([]*types.IndexWithScore, error)
}

func TestSemanticChunkingRetrievalRecallAtFive(t *testing.T) {
	fixture := loadSemanticRetrievalFixture(t)
	repository := newSQLiteRetrieverTestRepository(t)
	semantic := semanticEvalChunks(t, fixture)
	baseline := chunker.SplitText(fixture.Markdown, chunker.SplitterConfig{
		ChunkSize: fixture.ChunkSize, ChunkOverlap: fixture.ChunkOverlap,
		Separators: []string{"\n\n", "\n", ". "},
	})
	require.Greater(t, len(semantic), semanticRetrievalTopK)
	require.Greater(t, len(baseline), semanticRetrievalTopK)

	semanticIndex := indexSemanticEvalChunks(t, semanticEvalIndexRequest{
		repository: repository, prefix: "semantic", chunks: semantic, queries: fixture.Queries,
	})
	baselineIndex := indexSemanticEvalChunks(t, semanticEvalIndexRequest{
		repository: repository, prefix: "baseline", chunks: baseline, queries: fixture.Queries,
	})
	semanticMetrics := evaluateSemanticRetrieval(t, semanticRecallRequest{
		repository: repository, index: semanticIndex, queries: fixture.Queries, requireStructureHit: true,
	})
	baselineMetrics := evaluateSemanticRetrieval(t, semanticRecallRequest{
		repository: repository, index: baselineIndex, queries: fixture.Queries,
	})

	require.GreaterOrEqual(t, semanticMetrics.Structure.RecallAtFive, 0.95)
	require.GreaterOrEqual(t, semanticMetrics.Structure.ContextCompletenessRate, 0.95)
	require.GreaterOrEqual(t, semanticMetrics.Ordinary.RecallAtFive, baselineMetrics.Ordinary.RecallAtFive)
	require.GreaterOrEqual(t, semanticMetrics.Overall.RecallAtFive, baselineMetrics.Overall.RecallAtFive)
	requireSemanticMetricRange(t, semanticMetrics)
	requireSemanticMetricRange(t, baselineMetrics)
	logSemanticEvaluation(t, "keyword_semantic", semanticMetrics)
	logSemanticEvaluation(t, "keyword_baseline", baselineMetrics)
}

func TestSemanticParentChildKeywordRetrievalPreservesContext(t *testing.T) {
	fixture := loadSemanticRetrievalFixture(t)
	repository := newSQLiteRetrieverTestRepository(t)
	chunks, contexts := semanticParentChildEvalChunks(t, fixture)
	require.Greater(t, len(chunks), semanticRetrievalTopK)
	index := indexSemanticEvalChunks(t, semanticEvalIndexRequest{
		repository: repository, prefix: "parent-child", chunks: chunks,
		contexts: contexts, queries: fixture.Queries,
	})

	report := evaluateSemanticRetrieval(t, semanticRecallRequest{
		repository: repository, index: index, queries: fixture.Queries,
		requireStructureHit: true,
	})

	require.GreaterOrEqual(t, report.Structure.RecallAtFive, 0.95)
	require.GreaterOrEqual(t, report.Structure.ContextCompletenessRate, 0.95)
	requireSemanticMetricRange(t, report)
	logSemanticEvaluation(t, "keyword_parent_child", report)
}

func semanticEvalChunks(t *testing.T, fixture semanticRetrievalFixture) []chunker.Chunk {
	t.Helper()
	document, err := chunker.AnalyzeSemanticDocument(fixture.Markdown, chunker.SemanticAnalysisOptions{})
	require.NoError(t, err)
	chunks, err := chunker.SplitSemanticDocument(fixture.Markdown, chunker.SplitterConfig{
		Strategy: chunker.StrategyAuto, ChunkSize: fixture.ChunkSize,
		ChunkOverlap: 0, AllowZeroOverlap: true,
	}, document)
	require.NoError(t, err)
	return chunks
}

func semanticParentChildEvalChunks(
	t *testing.T,
	fixture semanticRetrievalFixture,
) ([]chunker.Chunk, []string) {
	t.Helper()
	document, err := chunker.AnalyzeSemanticDocument(fixture.Markdown, chunker.SemanticAnalysisOptions{})
	require.NoError(t, err)
	result, err := chunker.SplitParentChildSemanticDocument(chunker.SemanticParentChildRequest{
		Content: fixture.Markdown, Document: document,
		ParentConfig: chunker.SplitterConfig{
			Strategy: chunker.StrategyAuto, ChunkSize: fixture.ChunkSize * 3,
			ChunkOverlap: 0, AllowZeroOverlap: true,
		},
		ChildConfig: chunker.SplitterConfig{
			Strategy: chunker.StrategyAuto, ChunkSize: fixture.ChunkSize,
			ChunkOverlap: 0, AllowZeroOverlap: true,
		},
	})
	require.NoError(t, err)
	chunks := make([]chunker.Chunk, len(result.Children))
	contexts := make([]string, len(result.Children))
	for index, child := range result.Children {
		chunks[index] = child.Chunk
		contexts[index] = child.EmbeddingContent()
		if child.ParentIndex >= 0 {
			contexts[index] = result.Parents[child.ParentIndex].EmbeddingContent()
		}
	}
	return chunks, contexts
}

func indexSemanticEvalChunks(
	t *testing.T,
	request semanticEvalIndexRequest,
) indexedSemanticChunks {
	t.Helper()
	index := indexedSemanticChunks{
		knowledgeBaseID:  "kb-" + request.prefix,
		metricIDs:        make(map[string]int, len(request.chunks)),
		relevantIDs:      make(map[string]int, len(request.queries)),
		contextByChunkID: make(map[string]string, len(request.chunks)),
	}
	for position, current := range request.chunks {
		chunkID := fmt.Sprintf("%s-%03d", request.prefix, position)
		index.metricIDs[chunkID] = position + 1
		index.contextByChunkID[chunkID] = current.EmbeddingContent()
		if position < len(request.contexts) && request.contexts[position] != "" {
			index.contextByChunkID[chunkID] = request.contexts[position]
		}
		for _, query := range request.queries {
			if index.relevantIDs[query.Marker] == 0 && strings.Contains(current.Content, query.Marker) {
				index.relevantIDs[query.Marker] = position + 1
			}
		}
		var params map[string]any
		if position < len(request.embeddings) {
			params = map[string]any{"embedding": map[string][]float32{
				"source-" + chunkID: request.embeddings[position],
			}}
		}
		require.NoError(t, request.repository.Save(context.Background(), &types.IndexInfo{
			Content: current.EmbeddingContent(), SourceID: "source-" + chunkID,
			SourceType: types.ChunkSourceType, ChunkID: chunkID,
			KnowledgeID: "document-" + request.prefix, KnowledgeBaseID: index.knowledgeBaseID,
			IsEnabled: true,
		}, params))
	}
	for _, query := range request.queries {
		require.NotZero(t, index.relevantIDs[query.Marker], "missing marker %q", query.Marker)
	}
	return index
}

func evaluateSemanticRetrieval(
	t *testing.T,
	request semanticRecallRequest,
) semanticEvaluationReport {
	t.Helper()
	accumulator := newSemanticMetricAccumulator()
	for _, query := range request.queries {
		retrieved, err := retrieveSemanticEvalQuery(request, query)
		require.NoError(t, err)
		for _, result := range retrieved {
			metricID := request.index.metricIDs[result.ChunkID]
			require.NotZero(t, metricID, "unknown retrieved chunk %q", result.ChunkID)
		}
		metrics := computeSemanticQueryMetrics(request.index, query, retrieved)
		accumulator.add(query.Class, metrics)
		if request.requireStructureHit && query.Class == semanticQueryClassStructure {
			require.Equal(t, 1.0, metrics.RecallAtFive, "structure query %q missed target evidence", query.Query)
			require.Equal(t, 1.0, metrics.ContextCompletenessRate, "structure query %q lost context", query.Query)
		}
	}
	return accumulator.report()
}

func retrieveSemanticEvalQuery(
	request semanticRecallRequest,
	query semanticRetrievalQuery,
) ([]*types.IndexWithScore, error) {
	if request.retrieve != nil {
		return request.retrieve(query)
	}
	results, err := request.repository.Retrieve(context.Background(), types.RetrieveParams{
		Query: query.Query, KnowledgeBaseIDs: []string{request.index.knowledgeBaseID},
		TopK: semanticRetrievalTopK, RetrieverType: types.KeywordsRetrieverType,
	})
	return flattenSemanticRetrieval(results), err
}

func flattenSemanticRetrieval(results []*types.RetrieveResult) []*types.IndexWithScore {
	var flattened []*types.IndexWithScore
	for _, result := range results {
		flattened = append(flattened, result.Results...)
	}
	return flattened
}

func loadSemanticRetrievalFixture(t *testing.T) semanticRetrievalFixture {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "infrastructure", "chunker", "testdata", "semantic_retrieval_eval.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var fixture semanticRetrievalFixture
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.NotEmpty(t, fixture.Markdown)
	require.NotEmpty(t, fixture.Queries)
	requireSemanticFixtureCoverage(t, fixture.Queries)
	return fixture
}

func requireSemanticFixtureCoverage(t *testing.T, queries []semanticRetrievalQuery) {
	t.Helper()
	kinds := make(map[string]bool)
	for _, query := range queries {
		require.Contains(t, []string{semanticQueryClassStructure, semanticQueryClassOrdinary}, query.Class)
		require.NotEmpty(t, query.Kind)
		kinds[query.Kind] = true
	}
	for _, required := range []string{"text", "table", "faq", "record", "code", "toc_body", "cross_page"} {
		require.True(t, kinds[required], "fixture missing query kind %q", required)
	}
}
