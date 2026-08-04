package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestSplitIngestionDocumentAnalysisUnitsCoversUnicodeExactly(t *testing.T) {
	content := strings.Repeat("甲🙂é", 5010) + "尾声"

	units, err := splitIngestionDocumentAnalysisUnits(content)

	require.NoError(t, err)
	require.Greater(t, len(units), 1)
	require.Equal(t, content, joinIngestionDocumentUnits(units))
	require.Equal(t, utf8.RuneCountInString(content), units[len(units)-1].End)
	for _, unit := range units {
		require.LessOrEqual(t, utf8.RuneCountInString(unit.Content), ingestionDocumentAnalysisUnitRunes)
	}
}

func TestSplitIngestionDocumentAnalysisUnitsUsesSourceSlicesForTables(t *testing.T) {
	table := "# 报表\n\n| 列一 | 列二 |\n| --- | --- |\n" +
		strings.Repeat("| 数据甲 | 数据乙 |\n", 900)
	content := table + "\n结论段落"

	units, err := splitIngestionDocumentAnalysisUnits(content)

	require.NoError(t, err)
	require.Greater(t, len(units), 1)
	require.Equal(t, content, joinIngestionDocumentUnits(units))
}

func TestSplitIngestionDocumentAnalysisUnitsHandlesLongUnstructuredText(t *testing.T) {
	content := strings.Repeat("无标题正文", 4000)

	units, err := splitIngestionDocumentAnalysisUnits(content)

	require.NoError(t, err)
	require.Greater(t, len(units), 1)
	require.Equal(t, content, joinIngestionDocumentUnits(units))
}

func TestSplitIngestionDocumentAnalysisUnitsRejectsEmptyText(t *testing.T) {
	for _, content := range []string{"", " \n\t"} {
		units, err := splitIngestionDocumentAnalysisUnits(content)
		require.ErrorContains(t, err, "正文为空")
		require.Nil(t, units)
	}
}

func TestValidateIngestionDocumentAnalysisCoverageRejectsInvalidUnits(t *testing.T) {
	valid := []ingestionDocumentAnalysisUnit{
		{Index: 0, Start: 0, End: 2, Content: "甲乙"},
		{Index: 1, Start: 2, End: 4, Content: "丙丁"},
	}
	tests := []struct {
		name  string
		units []ingestionDocumentAnalysisUnit
	}{
		{name: "missing units", units: nil},
		{name: "gap", units: []ingestionDocumentAnalysisUnit{{Index: 0, Start: 1, End: 4, Content: "乙丙丁"}}},
		{name: "overlap", units: []ingestionDocumentAnalysisUnit{
			{Index: 0, Start: 0, End: 3, Content: "甲乙丙"},
			{Index: 1, Start: 2, End: 4, Content: "丙丁"},
		}},
		{name: "wrong index", units: []ingestionDocumentAnalysisUnit{{Index: 2, Start: 0, End: 4, Content: "甲乙丙丁"}}},
		{name: "wrong content", units: []ingestionDocumentAnalysisUnit{{Index: 0, Start: 0, End: 4, Content: "甲乙丙戊"}}},
		{name: "missing tail", units: valid[:1]},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, validateIngestionDocumentAnalysisCoverage("甲乙丙丁", test.units))
		})
	}
	require.NoError(t, validateIngestionDocumentAnalysisCoverage("甲乙丙丁", valid))
}

func joinIngestionDocumentUnits(units []ingestionDocumentAnalysisUnit) string {
	var builder strings.Builder
	for _, unit := range units {
		builder.WriteString(unit.Content)
	}
	return builder.String()
}
