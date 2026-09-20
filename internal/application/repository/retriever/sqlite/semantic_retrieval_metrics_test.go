//go:build sqlite_fts5

package sqlite

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/Tencent/WeKnora/internal/application/service/metric"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

const semanticStructureRecallGate = 0.95

type semanticMetricSet struct {
	RecallAtFive            float64
	MRR                     float64
	NDCGAtFive              float64
	ContextCompletenessRate float64
	IrrelevantContextRate   float64
	TokenIoU                float64
	ContextRecall           float64
	ContextPrecision        float64
}

type semanticEvaluationReport struct {
	Overall   semanticMetricSet
	Structure semanticMetricSet
	Ordinary  semanticMetricSet
	ByKind    map[string]semanticMetricSet
}

type semanticMetricAccumulator struct {
	totals     map[string]semanticMetricSet
	counts     map[string]int
	kindTotals map[string]semanticMetricSet
	kindCounts map[string]int
}

func newSemanticMetricAccumulator() *semanticMetricAccumulator {
	return &semanticMetricAccumulator{
		totals:     make(map[string]semanticMetricSet),
		counts:     make(map[string]int),
		kindTotals: make(map[string]semanticMetricSet),
		kindCounts: make(map[string]int),
	}
}

func (a *semanticMetricAccumulator) add(class, kind string, value semanticMetricSet) {
	a.totals[""] = addSemanticMetricSets(a.totals[""], value)
	a.counts[""]++
	a.totals[class] = addSemanticMetricSets(a.totals[class], value)
	a.counts[class]++
	if kind != "" {
		a.kindTotals[kind] = addSemanticMetricSets(a.kindTotals[kind], value)
		a.kindCounts[kind]++
	}
}

func (a *semanticMetricAccumulator) report() semanticEvaluationReport {
	byKind := make(map[string]semanticMetricSet, len(a.kindCounts))
	for kind, total := range a.kindTotals {
		byKind[kind] = averageSemanticMetricSet(total, a.kindCounts[kind])
	}
	return semanticEvaluationReport{
		Overall:   averageSemanticMetricSet(a.totals[""], a.counts[""]),
		Structure: averageSemanticMetricSet(a.totals[semanticQueryClassStructure], a.counts[semanticQueryClassStructure]),
		Ordinary:  averageSemanticMetricSet(a.totals[semanticQueryClassOrdinary], a.counts[semanticQueryClassOrdinary]),
		ByKind:    byKind,
	}
}

func computeSemanticQueryMetrics(
	index indexedSemanticChunks,
	query semanticRetrievalQuery,
	results []*types.IndexWithScore,
) semanticMetricSet {
	if len(results) > semanticRetrievalTopK {
		results = results[:semanticRetrievalTopK]
	}
	retrievedIDs := make([]int, 0, len(results))
	retrievedTexts := make([]string, 0, len(results))
	retrievedSources := make([]string, 0, len(results))
	for _, result := range results {
		retrievedIDs = append(retrievedIDs, index.metricIDs[result.ChunkID])
		retrievedTexts = append(retrievedTexts, index.contextByChunkID[result.ChunkID])
		source := index.sourceByChunkID[result.ChunkID]
		if strings.TrimSpace(source) == "" {
			source = index.contextByChunkID[result.ChunkID]
		}
		retrievedSources = append(retrievedSources, source)
	}
	input := &types.MetricInput{
		RetrievalGT: [][]int{{index.relevantIDs[query.Marker]}}, RetrievalIDs: retrievedIDs,
	}
	gold := semanticEvalTokens(semanticQueryExcerpt(query))
	retrieved := semanticEvalTokens(strings.Join(retrievedSources, "\n"))
	return semanticMetricSet{
		RecallAtFive:            metric.NewRecallMetric().Compute(input),
		MRR:                     metric.NewMRRMetric().Compute(input),
		NDCGAtFive:              metric.NewNDCGMetric(semanticRetrievalTopK).Compute(input),
		ContextCompletenessRate: semanticContextCompleteness(index, query, results),
		IrrelevantContextRate:   semanticIrrelevantContextRate(index, query, results),
		TokenIoU:                semanticTokenIoU(gold, retrieved),
		ContextRecall:           semanticTokenRecall(gold, retrieved),
		ContextPrecision:        semanticChunkContextPrecision(query, retrievedSources),
	}
}

func semanticQueryExcerpt(query semanticRetrievalQuery) string {
	if strings.TrimSpace(query.Excerpt) != "" {
		return query.Excerpt
	}
	if query.RequiredContext != "" {
		return strings.TrimSpace(query.RequiredContext + "\n" + query.Marker)
	}
	return query.Marker
}

func semanticEvalTokens(value string) []string {
	tokens := make([]string, 0, len(value)/4)
	var buf []rune
	flush := func() {
		if len(buf) == 0 {
			return
		}
		tokens = append(tokens, string(buf))
		buf = buf[:0]
	}
	for _, current := range value {
		switch {
		case unicode.IsSpace(current) || unicode.IsPunct(current):
			flush()
		case unicode.Is(unicode.Han, current):
			flush()
			tokens = append(tokens, string(current))
		default:
			buf = append(buf, unicode.ToLower(current))
		}
	}
	flush()
	return tokens
}

func semanticTokenSet(tokens []string) map[string]struct{} {
	result := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if token != "" {
			result[token] = struct{}{}
		}
	}
	return result
}

func semanticTokenIoU(gold, retrieved []string) float64 {
	left, right := semanticTokenSet(gold), semanticTokenSet(retrieved)
	if len(left) == 0 && len(right) == 0 {
		return 1
	}
	union := len(left)
	intersection := 0
	for token := range right {
		if _, ok := left[token]; ok {
			intersection++
			continue
		}
		union++
	}
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func semanticTokenRecall(gold, retrieved []string) float64 {
	left, right := semanticTokenSet(gold), semanticTokenSet(retrieved)
	if len(left) == 0 {
		return 1
	}
	hit := 0
	for token := range left {
		if _, ok := right[token]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(left))
}

func semanticChunkContextPrecision(query semanticRetrievalQuery, retrieved []string) float64 {
	if len(retrieved) == 0 {
		return 0
	}
	excerpt := semanticQueryExcerpt(query)
	relevant := 0
	for _, content := range retrieved {
		if strings.Contains(content, query.Marker) ||
			excerpt != "" && strings.Contains(content, excerpt) {
			relevant++
		}
	}
	return float64(relevant) / float64(len(retrieved))
}

func semanticContextCompleteness(
	index indexedSemanticChunks,
	query semanticRetrievalQuery,
	results []*types.IndexWithScore,
) float64 {
	for _, result := range results {
		if index.metricIDs[result.ChunkID] != index.relevantIDs[query.Marker] {
			continue
		}
		context := index.contextByChunkID[result.ChunkID]
		if query.RequiredContext == "" || strings.Contains(context, query.RequiredContext) {
			return 1
		}
		return 0
	}
	return 0
}

func semanticIrrelevantContextRate(
	index indexedSemanticChunks,
	query semanticRetrievalQuery,
	results []*types.IndexWithScore,
) float64 {
	if len(results) == 0 {
		return 0
	}
	irrelevant := 0
	for _, result := range results {
		if index.metricIDs[result.ChunkID] != index.relevantIDs[query.Marker] {
			irrelevant++
		}
	}
	return float64(irrelevant) / float64(len(results))
}

func logSemanticEvaluation(t *testing.T, name string, report semanticEvaluationReport) {
	t.Helper()
	t.Logf("SEMANTIC_RETRIEVAL_EVAL mode=%s %s", name, formatSemanticMetricSet("overall", report.Overall))
	t.Logf("SEMANTIC_RETRIEVAL_EVAL mode=%s %s", name, formatSemanticMetricSet("structure", report.Structure))
	t.Logf("SEMANTIC_RETRIEVAL_EVAL mode=%s %s", name, formatSemanticMetricSet("ordinary", report.Ordinary))
	for _, kind := range []string{"table", "faq", "record", "code", "toc_body", "cross_page", "text"} {
		value, ok := report.ByKind[kind]
		if !ok {
			continue
		}
		t.Logf("SEMANTIC_RETRIEVAL_EVAL mode=%s %s", name, formatSemanticMetricSet("kind:"+kind, value))
	}
}

func formatSemanticMetricSet(class string, value semanticMetricSet) string {
	return fmt.Sprintf(
		"class=%s Recall@5=%.3f MRR=%.3f NDCG@5=%.3f context_completeness=%.3f irrelevant_context=%.3f token_iou=%.3f context_recall=%.3f context_precision=%.3f",
		class, value.RecallAtFive, value.MRR, value.NDCGAtFive,
		value.ContextCompletenessRate, value.IrrelevantContextRate,
		value.TokenIoU, value.ContextRecall, value.ContextPrecision,
	)
}

func requireSemanticMetricRange(t *testing.T, report semanticEvaluationReport) {
	t.Helper()
	sets := []semanticMetricSet{report.Overall, report.Structure, report.Ordinary}
	for _, current := range report.ByKind {
		sets = append(sets, current)
	}
	for _, current := range sets {
		for _, value := range []float64{
			current.RecallAtFive, current.MRR, current.NDCGAtFive,
			current.ContextCompletenessRate, current.IrrelevantContextRate,
			current.TokenIoU, current.ContextRecall, current.ContextPrecision,
		} {
			require.GreaterOrEqual(t, value, 0.0)
			require.LessOrEqual(t, value, 1.0)
		}
	}
}

func requireSemanticKindGates(t *testing.T, report semanticEvaluationReport, queries []semanticRetrievalQuery) {
	t.Helper()
	required := make(map[string]bool)
	for _, query := range queries {
		if query.Class == semanticQueryClassStructure {
			required[query.Kind] = true
		}
	}
	for kind := range required {
		metrics, ok := report.ByKind[kind]
		require.True(t, ok, "missing metrics for structure kind %q", kind)
		require.GreaterOrEqual(
			t, metrics.RecallAtFive, semanticStructureRecallGate,
			"kind %s Recall@5", kind,
		)
		require.GreaterOrEqual(
			t, metrics.ContextCompletenessRate, semanticStructureRecallGate,
			"kind %s context_completeness", kind,
		)
	}
}

func validateSemanticRolloutGate(
	v2 semanticEvaluationReport,
	baseline semanticEvaluationReport,
) error {
	if v2.Structure.RecallAtFive < semanticStructureRecallGate {
		return fmt.Errorf(
			"structure Recall@5 %.3f is below %.3f",
			v2.Structure.RecallAtFive, semanticStructureRecallGate,
		)
	}
	if v2.Ordinary.RecallAtFive < baseline.Ordinary.RecallAtFive {
		return fmt.Errorf(
			"ordinary Recall@5 %.3f is below baseline %.3f",
			v2.Ordinary.RecallAtFive, baseline.Ordinary.RecallAtFive,
		)
	}
	if v2.Ordinary.TokenIoU < baseline.Ordinary.TokenIoU {
		return fmt.Errorf(
			"ordinary token IoU %.3f is below baseline %.3f",
			v2.Ordinary.TokenIoU, baseline.Ordinary.TokenIoU,
		)
	}
	for kind, metrics := range v2.ByKind {
		if kind == "text" {
			continue
		}
		if metrics.RecallAtFive < semanticStructureRecallGate {
			return fmt.Errorf("kind %s Recall@5 %.3f is below %.3f", kind, metrics.RecallAtFive, semanticStructureRecallGate)
		}
		if metrics.ContextCompletenessRate < semanticStructureRecallGate {
			return fmt.Errorf(
				"kind %s context_completeness %.3f is below %.3f",
				kind, metrics.ContextCompletenessRate, semanticStructureRecallGate,
			)
		}
	}
	return nil
}

func TestSemanticRolloutGateRejectsStructureAndOrdinaryRegression(t *testing.T) {
	baseline := semanticEvaluationReport{
		Ordinary: semanticMetricSet{RecallAtFive: 0.80, TokenIoU: 0.40},
	}
	valid := semanticEvaluationReport{
		Structure: semanticMetricSet{RecallAtFive: semanticStructureRecallGate},
		Ordinary:  semanticMetricSet{RecallAtFive: 0.80, TokenIoU: 0.40},
		ByKind: map[string]semanticMetricSet{
			"table": {RecallAtFive: semanticStructureRecallGate, ContextCompletenessRate: semanticStructureRecallGate},
		},
	}
	require.NoError(t, validateSemanticRolloutGate(valid, baseline))

	structureRegression := valid
	structureRegression.Structure.RecallAtFive = semanticStructureRecallGate - 0.01
	require.ErrorContains(t, validateSemanticRolloutGate(structureRegression, baseline), "structure Recall@5")

	ordinaryRegression := valid
	ordinaryRegression.Ordinary.RecallAtFive = baseline.Ordinary.RecallAtFive - 0.01
	require.ErrorContains(t, validateSemanticRolloutGate(ordinaryRegression, baseline), "ordinary Recall@5")

	iouRegression := valid
	iouRegression.Ordinary.TokenIoU = baseline.Ordinary.TokenIoU - 0.01
	require.ErrorContains(t, validateSemanticRolloutGate(iouRegression, baseline), "ordinary token IoU")

	kindRegression := valid
	kindRegression.ByKind = map[string]semanticMetricSet{
		"table": {RecallAtFive: semanticStructureRecallGate - 0.01, ContextCompletenessRate: 1},
	}
	require.ErrorContains(t, validateSemanticRolloutGate(kindRegression, baseline), "kind table Recall@5")
}

func TestSemanticTokenIoUPrefersTightEvidence(t *testing.T) {
	gold := semanticEvalTokens("TC-LOGIN Authentication Pass")
	tight := semanticEvalTokens("| TC-LOGIN | Authentication | Pass |")
	bloated := semanticEvalTokens("| TC-LOGIN | Authentication | Pass |\n" + strings.Repeat("unrelated neighboring section words ", 20))

	require.Greater(t, semanticTokenIoU(gold, tight), semanticTokenIoU(gold, bloated))
	require.Equal(t, 1.0, semanticTokenRecall(gold, tight))
	require.Greater(t, semanticChunkContextPrecision(
		semanticRetrievalQuery{Marker: "TC-LOGIN", Excerpt: "| TC-LOGIN | Authentication | Pass |"},
		[]string{"| TC-LOGIN | Authentication | Pass |"},
	), semanticChunkContextPrecision(
		semanticRetrievalQuery{Marker: "TC-LOGIN", Excerpt: "| TC-LOGIN | Authentication | Pass |"},
		[]string{"| TC-LOGIN | Authentication | Pass |", "unrelated neighboring section"},
	))
}

func addSemanticMetricSets(left, right semanticMetricSet) semanticMetricSet {
	left.RecallAtFive += right.RecallAtFive
	left.MRR += right.MRR
	left.NDCGAtFive += right.NDCGAtFive
	left.ContextCompletenessRate += right.ContextCompletenessRate
	left.IrrelevantContextRate += right.IrrelevantContextRate
	left.TokenIoU += right.TokenIoU
	left.ContextRecall += right.ContextRecall
	left.ContextPrecision += right.ContextPrecision
	return left
}

func averageSemanticMetricSet(total semanticMetricSet, count int) semanticMetricSet {
	if count == 0 {
		return semanticMetricSet{}
	}
	divisor := float64(count)
	total.RecallAtFive /= divisor
	total.MRR /= divisor
	total.NDCGAtFive /= divisor
	total.ContextCompletenessRate /= divisor
	total.IrrelevantContextRate /= divisor
	total.TokenIoU /= divisor
	total.ContextRecall /= divisor
	total.ContextPrecision /= divisor
	return total
}
