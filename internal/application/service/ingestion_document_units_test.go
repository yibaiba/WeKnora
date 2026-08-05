package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestCalculateIngestionDocumentAnalysisTokenBudgetUsesRequiredReserves(t *testing.T) {
	const contextWindow = 8192
	content := strings.Repeat("文", 15032)
	budget, err := calculateIngestionDocumentAnalysisTokenBudget(contextWindow, content)

	require.NoError(t, err)
	require.Equal(t, contextWindow, budget.ContextWindowTokens)
	require.Equal(t, ingestionDocumentAnalysisCompletionTokens, budget.CompletionTokens)
	require.Equal(t, (contextWindow+9)/10, budget.SafetyTokens)
	require.Positive(t, budget.PromptSchemaTokens)
	require.Equal(t, estimateIngestionDocumentTokens(content), budget.EstimatedSourceTokens)
	require.Equal(t, contextWindow-budget.CompletionTokens-budget.PromptSchemaTokens-budget.SafetyTokens, budget.ContentTokens)
}

func TestCalculateIngestionDocumentAnalysisTokenBudgetRejectsInsufficientWindow(t *testing.T) {
	budget, err := calculateIngestionDocumentAnalysisTokenBudget(1024, strings.Repeat("文", 15032))

	require.ErrorContains(t, err, "token 预算不足")
	require.LessOrEqual(t, budget.ContentTokens, 0)
}

func TestSplitIngestionDocumentAnalysisUnitsVariesWithModelWindow(t *testing.T) {
	content := strings.Repeat("章节内容。", 3000) + strings.Repeat("终", 32)
	require.Equal(t, 15032, utf8.RuneCountInString(content))

	counts := make([]int, 0, 3)
	for _, contextWindow := range []int{4096, 8192, 32768} {
		budget := requireAnalysisBudget(t, contextWindow, content)
		units, err := splitIngestionDocumentAnalysisUnits(content, budget.ContentTokens)

		require.NoError(t, err)
		require.Equal(t, content, joinIngestionDocumentUnits(units))
		requireUnitsWithinTokenBudget(t, units, budget.ContentTokens)
		counts = append(counts, len(units))
	}
	require.Greater(t, counts[0], counts[1])
	require.Greater(t, counts[1], counts[2])
}

func TestSplitIngestionDocumentAnalysisUnitsCoversUnicodeExactly(t *testing.T) {
	content := strings.Repeat("甲🙂é", 5010) + "尾声"
	budget := requireAnalysisBudget(t, 4096, content)

	units, err := splitIngestionDocumentAnalysisUnits(content, budget.ContentTokens)

	require.NoError(t, err)
	require.Greater(t, len(units), 1)
	require.Equal(t, content, joinIngestionDocumentUnits(units))
	require.Equal(t, utf8.RuneCountInString(content), units[len(units)-1].End)
	requireUnitsWithinTokenBudget(t, units, budget.ContentTokens)
}

func TestSplitIngestionDocumentAnalysisUnitsUsesSemanticSourceSlices(t *testing.T) {
	table := "# 报表\n\n| 列一 | 列二 |\n| --- | --- |\n" +
		strings.Repeat("| 数据甲 | 数据乙 |\n", 900)
	content := table + "\n## 结论\n\n" + strings.Repeat("混合 English 结论。\n", 500)
	budget := requireAnalysisBudget(t, 4096, content)

	units, err := splitIngestionDocumentAnalysisUnits(content, budget.ContentTokens)

	require.NoError(t, err)
	require.Greater(t, len(units), 1)
	require.Equal(t, content, joinIngestionDocumentUnits(units))
	requireUnitsWithinTokenBudget(t, units, budget.ContentTokens)
}

func TestSplitIngestionDocumentAnalysisUnitsHandlesLongParagraph(t *testing.T) {
	content := strings.Repeat("unstructured", 5000)
	budget := requireAnalysisBudget(t, 4096, content)

	units, err := splitIngestionDocumentAnalysisUnits(content, budget.ContentTokens)

	require.NoError(t, err)
	require.Greater(t, len(units), 1)
	require.Equal(t, content, joinIngestionDocumentUnits(units))
	requireUnitsWithinTokenBudget(t, units, budget.ContentTokens)
}

func TestSplitIngestionDocumentAnalysisUnitsRejectsInvalidInput(t *testing.T) {
	for _, content := range []string{"", " \n\t"} {
		units, err := splitIngestionDocumentAnalysisUnits(content, 100)
		require.ErrorContains(t, err, "正文为空")
		require.Nil(t, units)
	}
	units, err := splitIngestionDocumentAnalysisUnits("正文", 0)
	require.ErrorContains(t, err, "预算必须大于 0")
	require.Nil(t, units)
}

func TestValidateIngestionDocumentAnalysisCoverageRejectsInvalidUnits(t *testing.T) {
	valid := []ingestionDocumentAnalysisUnit{
		newTestAnalysisUnit(0, 0, 2, "甲乙"),
		newTestAnalysisUnit(1, 2, 4, "丙丁"),
	}
	tests := []struct {
		name  string
		units []ingestionDocumentAnalysisUnit
	}{
		{name: "missing units", units: nil},
		{name: "gap", units: []ingestionDocumentAnalysisUnit{newTestAnalysisUnit(0, 1, 4, "乙丙丁")}},
		{name: "overlap", units: []ingestionDocumentAnalysisUnit{
			newTestAnalysisUnit(0, 0, 3, "甲乙丙"),
			newTestAnalysisUnit(1, 2, 4, "丙丁"),
		}},
		{name: "wrong index", units: []ingestionDocumentAnalysisUnit{newTestAnalysisUnit(2, 0, 4, "甲乙丙丁")}},
		{name: "wrong content", units: []ingestionDocumentAnalysisUnit{newTestAnalysisUnit(0, 0, 4, "甲乙丙戊")}},
		{name: "missing tail", units: valid[:1]},
		{name: "wrong estimate", units: []ingestionDocumentAnalysisUnit{{
			Index: 0, Start: 0, End: 4, EstimatedTokens: 99, Content: "甲乙丙丁",
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, validateIngestionDocumentAnalysisCoverage("甲乙丙丁", test.units, 10))
		})
	}
	require.NoError(t, validateIngestionDocumentAnalysisCoverage("甲乙丙丁", valid, 10))
	require.ErrorContains(t, validateIngestionDocumentAnalysisCoverage(
		"甲乙丙丁", []ingestionDocumentAnalysisUnit{newTestAnalysisUnit(0, 0, 4, "甲乙丙丁")}, 1,
	), "超过正文预算")
}

func requireAnalysisBudget(
	t *testing.T,
	contextWindow int,
	content string,
) ingestionDocumentAnalysisTokenBudget {
	t.Helper()
	budget, err := calculateIngestionDocumentAnalysisTokenBudget(contextWindow, content)
	require.NoError(t, err)
	return budget
}

func requireUnitsWithinTokenBudget(
	t *testing.T,
	units []ingestionDocumentAnalysisUnit,
	tokenBudget int,
) {
	t.Helper()
	cursor := 0
	for index, unit := range units {
		require.Equal(t, index, unit.Index)
		require.Equal(t, cursor, unit.Start)
		require.Equal(t, estimateIngestionDocumentTokens(unit.Content), unit.EstimatedTokens)
		require.LessOrEqual(t, unit.EstimatedTokens, tokenBudget)
		cursor = unit.End
	}
}

func newTestAnalysisUnit(index, start, end int, content string) ingestionDocumentAnalysisUnit {
	return ingestionDocumentAnalysisUnit{
		Index: index, Start: start, End: end,
		EstimatedTokens: estimateIngestionDocumentTokens(content), Content: content,
	}
}

func joinIngestionDocumentUnits(units []ingestionDocumentAnalysisUnit) string {
	var builder strings.Builder
	for _, unit := range units {
		builder.WriteString(unit.Content)
	}
	return builder.String()
}
