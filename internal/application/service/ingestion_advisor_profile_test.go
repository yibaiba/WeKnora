package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestBuildIngestionDocumentProfileUsesThreeRuneSafeWindows(t *testing.T) {
	content := strings.Repeat("头", 8000) + strings.Repeat("前", 7000) +
		strings.Repeat("中", 8000) + strings.Repeat("后", 7000) + strings.Repeat("尾", 8000)

	profile := BuildIngestionDocumentProfile(content)

	require.True(t, profile.Sample.Truncated)
	require.Equal(t, 8000, utf8.RuneCountInString(profile.Sample.Head))
	require.Equal(t, 8000, utf8.RuneCountInString(profile.Sample.Middle))
	require.Equal(t, 8000, utf8.RuneCountInString(profile.Sample.Tail))
	require.Equal(t, strings.Repeat("头", 8000), profile.Sample.Head)
	require.Equal(t, strings.Repeat("中", 8000), profile.Sample.Middle)
	require.Equal(t, strings.Repeat("尾", 8000), profile.Sample.Tail)
	require.Equal(t, 38000, profile.Statistics.CharacterCount)
}

func TestBuildIngestionDocumentProfileCapturesFullStructure(t *testing.T) {
	content := `# 制度
## Scope

- 第一项
2. Second item

| 姓名 | 数量 |
| --- | --- |
| Alice | 2 |

问：如何申请？
答：提交表单 1。`

	profile := BuildIngestionDocumentProfile(content)

	require.False(t, profile.Sample.Truncated)
	require.Equal(t, content, profile.Sample.Head)
	require.Equal(t, 1, profile.Statistics.HeadingLevelCounts.H1)
	require.Equal(t, 1, profile.Statistics.HeadingLevelCounts.H2)
	require.Equal(t, 2, profile.Statistics.ListLineCount)
	require.Equal(t, 3, profile.Statistics.TableLineCount)
	require.Equal(t, 1, profile.Statistics.QuestionAnswerPairs)
	require.Positive(t, profile.Statistics.Language.CJKCharacters)
	require.Positive(t, profile.Statistics.Language.LatinCharacters)
	require.Positive(t, profile.Statistics.Language.DigitCharacters)
}

func TestBuildIngestionDocumentProfileDoesNotMutateInput(t *testing.T) {
	content := "# 标题\n\n正文"
	original := content

	_ = BuildIngestionDocumentProfile(content)

	require.Equal(t, original, content)
}
