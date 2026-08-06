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

	"github.com/Tencent/WeKnora/internal/application/service/metric"
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
	Query           string `json:"query"`
	Marker          string `json:"marker"`
	RequiredContext string `json:"required_context"`
}

type indexedSemanticChunks struct {
	knowledgeBaseID string
	metricIDs       map[string]int
	relevantIDs     map[string]int
}

type semanticEvalIndexRequest struct {
	repository *sqliteRepository
	prefix     string
	chunks     []chunker.Chunk
	queries    []semanticRetrievalQuery
}

type semanticRecallRequest struct {
	repository          *sqliteRepository
	index               indexedSemanticChunks
	queries             []semanticRetrievalQuery
	requireStructureHit bool
}

type semanticRecallResult struct {
	overall   float64
	structure float64
	ordinary  float64
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
	semanticRecall := evaluateSemanticRecall(t, semanticRecallRequest{
		repository: repository, index: semanticIndex, queries: fixture.Queries, requireStructureHit: true,
	})
	baselineRecall := evaluateSemanticRecall(t, semanticRecallRequest{
		repository: repository, index: baselineIndex, queries: fixture.Queries,
	})

	require.Equal(t, 1.0, semanticRecall.structure)
	require.GreaterOrEqual(t, semanticRecall.ordinary, baselineRecall.ordinary)
	require.GreaterOrEqual(t, semanticRecall.overall, baselineRecall.overall)
	t.Logf(
		"Recall@5 semantic overall=%.3f structure=%.3f ordinary=%.3f; "+
			"baseline overall=%.3f ordinary=%.3f",
		semanticRecall.overall, semanticRecall.structure, semanticRecall.ordinary,
		baselineRecall.overall, baselineRecall.ordinary,
	)
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

func indexSemanticEvalChunks(
	t *testing.T,
	request semanticEvalIndexRequest,
) indexedSemanticChunks {
	t.Helper()
	index := indexedSemanticChunks{
		knowledgeBaseID: "kb-" + request.prefix,
		metricIDs:       make(map[string]int, len(request.chunks)),
		relevantIDs:     make(map[string]int, len(request.queries)),
	}
	for position, current := range request.chunks {
		chunkID := fmt.Sprintf("%s-%03d", request.prefix, position)
		index.metricIDs[chunkID] = position + 1
		for _, query := range request.queries {
			if index.relevantIDs[query.Marker] == 0 && strings.Contains(current.Content, query.Marker) {
				index.relevantIDs[query.Marker] = position + 1
			}
		}
		require.NoError(t, request.repository.Save(context.Background(), &types.IndexInfo{
			Content: current.EmbeddingContent(), SourceID: "source-" + chunkID,
			SourceType: types.ChunkSourceType, ChunkID: chunkID,
			KnowledgeID: "document-" + request.prefix, KnowledgeBaseID: index.knowledgeBaseID,
			IsEnabled: true,
		}, nil))
	}
	for _, query := range request.queries {
		require.NotZero(t, index.relevantIDs[query.Marker], "missing marker %q", query.Marker)
	}
	return index
}

func evaluateSemanticRecall(
	t *testing.T,
	request semanticRecallRequest,
) semanticRecallResult {
	t.Helper()
	recallMetric := metric.NewRecallMetric()
	total := 0.0
	classTotals := make(map[string]float64)
	classCounts := make(map[string]int)
	for _, query := range request.queries {
		results, err := request.repository.Retrieve(context.Background(), types.RetrieveParams{
			Query: query.Query, KnowledgeBaseIDs: []string{request.index.knowledgeBaseID},
			TopK: semanticRetrievalTopK, RetrieverType: types.KeywordsRetrieverType,
		})
		require.NoError(t, err)
		retrieved := flattenSemanticRetrieval(results)
		metricIDs := make([]int, 0, len(retrieved))
		for _, result := range retrieved {
			metricID := request.index.metricIDs[result.ChunkID]
			require.NotZero(t, metricID, "unknown retrieved chunk %q", result.ChunkID)
			metricIDs = append(metricIDs, metricID)
		}
		recall := recallMetric.Compute(&types.MetricInput{
			RetrievalGT: [][]int{{request.index.relevantIDs[query.Marker]}}, RetrievalIDs: metricIDs,
		})
		total += recall
		classTotals[query.Class] += recall
		classCounts[query.Class]++
		if request.requireStructureHit && query.Class == semanticQueryClassStructure {
			require.Equal(t, 1.0, recall, "structure query %q missed target evidence", query.Query)
			requireSemanticRetrievedContext(t, retrieved, query)
		}
	}
	return semanticRecallResult{
		overall:   total / float64(len(request.queries)),
		structure: averageSemanticRecall(classTotals, classCounts, semanticQueryClassStructure),
		ordinary:  averageSemanticRecall(classTotals, classCounts, semanticQueryClassOrdinary),
	}
}

func averageSemanticRecall(totals map[string]float64, counts map[string]int, class string) float64 {
	if counts[class] == 0 {
		return 0
	}
	return totals[class] / float64(counts[class])
}

func requireSemanticRetrievedContext(
	t *testing.T,
	results []*types.IndexWithScore,
	query semanticRetrievalQuery,
) {
	t.Helper()
	for _, result := range results {
		if !strings.Contains(result.Content, query.Marker) {
			continue
		}
		require.Contains(t, result.Content, query.RequiredContext)
		return
	}
	require.FailNow(t, "target evidence not returned", query.Marker)
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
	return fixture
}
