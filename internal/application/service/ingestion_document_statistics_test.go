package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildIngestionDocumentStatisticsCapturesFullStructure(t *testing.T) {
	content := `# 制度
## Scope

- 第一项
2. Second item

| 姓名 | 数量 |
| --- | --- |
| Alice | 2 |

问：如何申请？
答：提交表单 1。`

	statistics := BuildIngestionDocumentStatistics(content)

	require.Equal(t, 1, statistics.HeadingLevelCounts.H1)
	require.Equal(t, 1, statistics.HeadingLevelCounts.H2)
	require.Equal(t, 2, statistics.ListLineCount)
	require.Equal(t, 3, statistics.TableLineCount)
	require.Equal(t, 1, statistics.QuestionAnswerPairs)
	require.Positive(t, statistics.Language.CJKCharacters)
	require.Positive(t, statistics.Language.LatinCharacters)
	require.Positive(t, statistics.Language.DigitCharacters)
}

func TestBuildIngestionDocumentStatisticsDoesNotMutateInput(t *testing.T) {
	content := "# 标题\n\n正文"
	original := content

	_ = BuildIngestionDocumentStatistics(content)

	require.Equal(t, original, content)
}
