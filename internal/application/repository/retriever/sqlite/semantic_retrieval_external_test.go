//go:build sqlite_fts5 && retrieval_eval_external

package sqlite

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

const (
	semanticExternalCandidateTopK = 12
	semanticExternalTimeout       = 8 * time.Minute
	semanticRRFConstant           = 60.0
)

type semanticExternalConfig struct {
	embeddingBaseURL string
	embeddingAPIKey  string
	embeddingModel   string
	rerankBaseURL    string
	rerankAPIKey     string
	rerankModel      string
	rerankProvider   string
}

type semanticExternalEvaluator struct {
	ctx          context.Context
	repository   *sqliteRepository
	index        indexedSemanticChunks
	queryVectors map[string][]float32
	reranker     rerank.Reranker
}

func TestExternalSemanticChunkingRetrieval(t *testing.T) {
	config := loadSemanticExternalConfig(t)
	ctx, cancel := context.WithTimeout(t.Context(), semanticExternalTimeout)
	defer cancel()
	fixture := loadSemanticRetrievalFixture(t)
	semanticChunks := semanticEvalChunks(t, fixture)
	baselineChunks := chunker.SplitText(fixture.Markdown, chunker.SplitterConfig{
		ChunkSize: fixture.ChunkSize, ChunkOverlap: fixture.ChunkOverlap,
		Separators: []string{"\n\n", "\n", ". "},
	})
	embedder := newSemanticExternalEmbedder(t, config)
	semanticVectors := embedSemanticTexts(t, ctx, embedder, semanticChunks)
	baselineVectors := embedSemanticTexts(t, ctx, embedder, baselineChunks)
	queryVectors := embedSemanticQueries(t, ctx, embedder, fixture.Queries)
	repository := newSQLiteRetrieverTestRepository(t)
	semanticIndex := indexSemanticEvalChunks(t, semanticEvalIndexRequest{
		repository: repository, prefix: "external-semantic", chunks: semanticChunks,
		queries: fixture.Queries, embeddings: semanticVectors,
	})
	baselineIndex := indexSemanticEvalChunks(t, semanticEvalIndexRequest{
		repository: repository, prefix: "external-baseline", chunks: baselineChunks,
		queries: fixture.Queries, embeddings: baselineVectors,
	})
	reranker, err := rerank.NewReranker(&rerank.RerankerConfig{
		APIKey: config.rerankAPIKey, BaseURL: config.rerankBaseURL,
		ModelName: config.rerankModel, ModelID: "semantic-eval-reranker",
		Source: types.ModelSourceRemote, Provider: config.rerankProvider,
	})
	require.NoError(t, err)

	semanticEvaluator := semanticExternalEvaluator{
		ctx: ctx, repository: repository, index: semanticIndex,
		queryVectors: queryVectors, reranker: reranker,
	}
	baselineEvaluator := semanticExternalEvaluator{
		ctx: ctx, repository: repository, index: baselineIndex,
		queryVectors: queryVectors, reranker: reranker,
	}
	vectorReport := evaluateExternalSemanticMode(t, fixture.Queries, semanticIndex, semanticEvaluator.vector)
	baselineVector := evaluateExternalSemanticMode(t, fixture.Queries, baselineIndex, baselineEvaluator.vector)
	hybridReport := evaluateExternalSemanticMode(t, fixture.Queries, semanticIndex, semanticEvaluator.hybrid)
	baselineHybrid := evaluateExternalSemanticMode(t, fixture.Queries, baselineIndex, baselineEvaluator.hybrid)
	rerankReport := evaluateExternalSemanticMode(t, fixture.Queries, semanticIndex, semanticEvaluator.reranked)
	baselineRerank := evaluateExternalSemanticMode(t, fixture.Queries, baselineIndex, baselineEvaluator.reranked)

	for _, report := range []semanticEvaluationReport{
		vectorReport, baselineVector, hybridReport, baselineHybrid, rerankReport, baselineRerank,
	} {
		requireSemanticMetricRange(t, report)
	}
	logSemanticEvaluation(t, "external_vector_semantic", vectorReport)
	logSemanticEvaluation(t, "external_vector_baseline", baselineVector)
	logSemanticEvaluation(t, "external_hybrid_semantic", hybridReport)
	logSemanticEvaluation(t, "external_hybrid_baseline", baselineHybrid)
	logSemanticEvaluation(t, "external_rerank_semantic", rerankReport)
	logSemanticEvaluation(t, "external_rerank_baseline", baselineRerank)
	require.NoError(t, validateSemanticRolloutGate(vectorReport, baselineVector), "vector rollout gate")
	require.NoError(t, validateSemanticRolloutGate(hybridReport, baselineHybrid), "hybrid rollout gate")
	require.NoError(t, validateSemanticRolloutGate(rerankReport, baselineRerank), "rerank rollout gate")
}

func loadSemanticExternalConfig(t *testing.T) semanticExternalConfig {
	t.Helper()
	values := map[string]string{
		"SEMANTIC_EVAL_EMBEDDING_BASE_URL": strings.TrimSpace(os.Getenv("SEMANTIC_EVAL_EMBEDDING_BASE_URL")),
		"SEMANTIC_EVAL_EMBEDDING_API_KEY":  strings.TrimSpace(os.Getenv("SEMANTIC_EVAL_EMBEDDING_API_KEY")),
		"SEMANTIC_EVAL_EMBEDDING_MODEL":    strings.TrimSpace(os.Getenv("SEMANTIC_EVAL_EMBEDDING_MODEL")),
		"SEMANTIC_EVAL_RERANK_BASE_URL":    strings.TrimSpace(os.Getenv("SEMANTIC_EVAL_RERANK_BASE_URL")),
		"SEMANTIC_EVAL_RERANK_API_KEY":     strings.TrimSpace(os.Getenv("SEMANTIC_EVAL_RERANK_API_KEY")),
		"SEMANTIC_EVAL_RERANK_MODEL":       strings.TrimSpace(os.Getenv("SEMANTIC_EVAL_RERANK_MODEL")),
	}
	missing := make([]string, 0)
	for name, value := range values {
		if value == "" {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing, "external retrieval evaluation credentials are required: %s", strings.Join(missing, ","))
	return semanticExternalConfig{
		embeddingBaseURL: values["SEMANTIC_EVAL_EMBEDDING_BASE_URL"],
		embeddingAPIKey:  values["SEMANTIC_EVAL_EMBEDDING_API_KEY"],
		embeddingModel:   values["SEMANTIC_EVAL_EMBEDDING_MODEL"],
		rerankBaseURL:    values["SEMANTIC_EVAL_RERANK_BASE_URL"],
		rerankAPIKey:     values["SEMANTIC_EVAL_RERANK_API_KEY"],
		rerankModel:      values["SEMANTIC_EVAL_RERANK_MODEL"],
		rerankProvider:   strings.TrimSpace(os.Getenv("SEMANTIC_EVAL_RERANK_PROVIDER")),
	}
}

func newSemanticExternalEmbedder(
	t *testing.T,
	config semanticExternalConfig,
) embedding.Embedder {
	t.Helper()
	result, err := embedding.NewOpenAIEmbedder(
		config.embeddingAPIKey, config.embeddingBaseURL, config.embeddingModel,
		0, 0, "semantic-eval-embedder", nil,
	)
	require.NoError(t, err)
	return result
}

func embedSemanticTexts(
	t *testing.T,
	ctx context.Context,
	embedder embedding.Embedder,
	chunks []chunker.Chunk,
) [][]float32 {
	t.Helper()
	texts := make([]string, len(chunks))
	for index, current := range chunks {
		texts[index] = current.EmbeddingContent()
	}
	vectors, err := embedder.BatchEmbed(ctx, texts)
	require.NoError(t, err)
	require.Len(t, vectors, len(texts))
	return vectors
}

func embedSemanticQueries(
	t *testing.T,
	ctx context.Context,
	embedder embedding.Embedder,
	queries []semanticRetrievalQuery,
) map[string][]float32 {
	t.Helper()
	texts := make([]string, len(queries))
	for index, query := range queries {
		texts[index] = query.Query
	}
	vectors, err := embedder.BatchEmbed(ctx, texts)
	require.NoError(t, err)
	require.Len(t, vectors, len(texts))
	result := make(map[string][]float32, len(queries))
	for index, query := range queries {
		result[query.Marker] = vectors[index]
	}
	return result
}

func evaluateExternalSemanticMode(
	t *testing.T,
	queries []semanticRetrievalQuery,
	index indexedSemanticChunks,
	retrieve func(semanticRetrievalQuery) ([]*types.IndexWithScore, error),
) semanticEvaluationReport {
	t.Helper()
	return evaluateSemanticRetrieval(t, semanticRecallRequest{
		index: index, queries: queries, retrieve: retrieve,
	})
}

func (e semanticExternalEvaluator) vector(query semanticRetrievalQuery) ([]*types.IndexWithScore, error) {
	results, err := e.repository.Retrieve(e.ctx, types.RetrieveParams{
		Embedding: e.queryVectors[query.Marker], KnowledgeBaseIDs: []string{e.index.knowledgeBaseID},
		TopK: semanticExternalCandidateTopK, RetrieverType: types.VectorRetrieverType,
	})
	return flattenSemanticRetrieval(results), err
}

func (e semanticExternalEvaluator) hybrid(query semanticRetrievalQuery) ([]*types.IndexWithScore, error) {
	results, err := e.repository.Retrieve(e.ctx, types.RetrieveParams{
		Query: query.Query, Embedding: e.queryVectors[query.Marker],
		KnowledgeBaseIDs: []string{e.index.knowledgeBaseID}, TopK: semanticExternalCandidateTopK,
	})
	if err != nil {
		return nil, err
	}
	return fuseSemanticEvalResults(results, semanticExternalCandidateTopK), nil
}

func (e semanticExternalEvaluator) reranked(query semanticRetrievalQuery) ([]*types.IndexWithScore, error) {
	candidates, err := e.hybrid(query)
	if err != nil {
		return nil, err
	}
	documents := make([]string, len(candidates))
	for index, candidate := range candidates {
		documents[index] = candidate.Content
	}
	ranks, err := e.reranker.Rerank(e.ctx, query.Query, documents)
	if err != nil {
		return nil, err
	}
	return orderSemanticEvalRerank(candidates, ranks)
}

func fuseSemanticEvalResults(results []*types.RetrieveResult, limit int) []*types.IndexWithScore {
	byChunk := make(map[string]*types.IndexWithScore)
	for _, group := range results {
		for rank, current := range group.Results {
			candidate := byChunk[current.ChunkID]
			if candidate == nil {
				cloned := *current
				cloned.Score = 0
				candidate = &cloned
				byChunk[current.ChunkID] = candidate
			}
			candidate.Score += 1 / (semanticRRFConstant + float64(rank+1))
		}
	}
	merged := make([]*types.IndexWithScore, 0, len(byChunk))
	for _, current := range byChunk {
		merged = append(merged, current)
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })
	return merged[:min(limit, len(merged))]
}

func orderSemanticEvalRerank(
	candidates []*types.IndexWithScore,
	ranks []rerank.RankResult,
) ([]*types.IndexWithScore, error) {
	sort.SliceStable(ranks, func(i, j int) bool { return ranks[i].RelevanceScore > ranks[j].RelevanceScore })
	result := make([]*types.IndexWithScore, 0, len(ranks))
	seen := make(map[int]bool)
	for _, rank := range ranks {
		if rank.Index < 0 || rank.Index >= len(candidates) || seen[rank.Index] {
			return nil, fmt.Errorf("rerank response contains invalid index %d", rank.Index)
		}
		seen[rank.Index] = true
		cloned := *candidates[rank.Index]
		cloned.Score = rank.RelevanceScore
		result = append(result, &cloned)
	}
	return result, nil
}
