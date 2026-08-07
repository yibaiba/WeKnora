//go:build sqlite_fts5

package sqlite

import (
	"fmt"
	"strings"
	"testing"

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
}

type semanticEvaluationReport struct {
	Overall   semanticMetricSet
	Structure semanticMetricSet
	Ordinary  semanticMetricSet
}

type semanticMetricAccumulator struct {
	totals map[string]semanticMetricSet
	counts map[string]int
}

func newSemanticMetricAccumulator() *semanticMetricAccumulator {
	return &semanticMetricAccumulator{
		totals: make(map[string]semanticMetricSet), counts: make(map[string]int),
	}
}

func (a *semanticMetricAccumulator) add(class string, value semanticMetricSet) {
	a.totals[""] = addSemanticMetricSets(a.totals[""], value)
	a.counts[""]++
	a.totals[class] = addSemanticMetricSets(a.totals[class], value)
	a.counts[class]++
}

func (a *semanticMetricAccumulator) report() semanticEvaluationReport {
	return semanticEvaluationReport{
		Overall:   averageSemanticMetricSet(a.totals[""], a.counts[""]),
		Structure: averageSemanticMetricSet(a.totals[semanticQueryClassStructure], a.counts[semanticQueryClassStructure]),
		Ordinary:  averageSemanticMetricSet(a.totals[semanticQueryClassOrdinary], a.counts[semanticQueryClassOrdinary]),
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
	for _, result := range results {
		retrievedIDs = append(retrievedIDs, index.metricIDs[result.ChunkID])
	}
	input := &types.MetricInput{
		RetrievalGT: [][]int{{index.relevantIDs[query.Marker]}}, RetrievalIDs: retrievedIDs,
	}
	return semanticMetricSet{
		RecallAtFive:            metric.NewRecallMetric().Compute(input),
		MRR:                     metric.NewMRRMetric().Compute(input),
		NDCGAtFive:              metric.NewNDCGMetric(semanticRetrievalTopK).Compute(input),
		ContextCompletenessRate: semanticContextCompleteness(index, query, results),
		IrrelevantContextRate:   semanticIrrelevantContextRate(index, query, results),
	}
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
}

func formatSemanticMetricSet(class string, value semanticMetricSet) string {
	return fmt.Sprintf(
		"class=%s Recall@5=%.3f MRR=%.3f NDCG@5=%.3f context_completeness=%.3f irrelevant_context=%.3f",
		class, value.RecallAtFive, value.MRR, value.NDCGAtFive,
		value.ContextCompletenessRate, value.IrrelevantContextRate,
	)
}

func requireSemanticMetricRange(t *testing.T, report semanticEvaluationReport) {
	t.Helper()
	for _, current := range []semanticMetricSet{report.Overall, report.Structure, report.Ordinary} {
		for _, value := range []float64{
			current.RecallAtFive, current.MRR, current.NDCGAtFive,
			current.ContextCompletenessRate, current.IrrelevantContextRate,
		} {
			require.GreaterOrEqual(t, value, 0.0)
			require.LessOrEqual(t, value, 1.0)
		}
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
	return nil
}

func TestSemanticRolloutGateRejectsStructureAndOrdinaryRegression(t *testing.T) {
	baseline := semanticEvaluationReport{
		Ordinary: semanticMetricSet{RecallAtFive: 0.80},
	}
	valid := semanticEvaluationReport{
		Structure: semanticMetricSet{RecallAtFive: semanticStructureRecallGate},
		Ordinary:  semanticMetricSet{RecallAtFive: 0.80},
	}
	require.NoError(t, validateSemanticRolloutGate(valid, baseline))

	structureRegression := valid
	structureRegression.Structure.RecallAtFive = semanticStructureRecallGate - 0.01
	require.ErrorContains(t, validateSemanticRolloutGate(structureRegression, baseline), "structure Recall@5")

	ordinaryRegression := valid
	ordinaryRegression.Ordinary.RecallAtFive = baseline.Ordinary.RecallAtFive - 0.01
	require.ErrorContains(t, validateSemanticRolloutGate(ordinaryRegression, baseline), "ordinary Recall@5")
}

func addSemanticMetricSets(left, right semanticMetricSet) semanticMetricSet {
	left.RecallAtFive += right.RecallAtFive
	left.MRR += right.MRR
	left.NDCGAtFive += right.NDCGAtFive
	left.ContextCompletenessRate += right.ContextCompletenessRate
	left.IrrelevantContextRate += right.IrrelevantContextRate
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
	return total
}
