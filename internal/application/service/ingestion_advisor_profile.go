package service

import (
	"math"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	ingestionSampleBudget     = 24000
	ingestionSampleWindowSize = 8000
)

// BuildIngestionDocumentProfile derives full-document statistics and a
// deterministic, rune-safe prompt sample without mutating caller data.
func BuildIngestionDocumentProfile(content string) types.IngestionDocumentProfile {
	return types.IngestionDocumentProfile{
		Statistics: BuildIngestionDocumentStatistics(content),
		Sample:     sampleDocumentContent([]rune(content)),
	}
}

// BuildIngestionDocumentStatistics profiles the complete extracted text
// without retaining any body sample.
func BuildIngestionDocumentStatistics(content string) types.DocumentStructureStats {
	lines := strings.Split(content, "\n")
	stats := profileDocumentLines(lines)
	profileParagraphs(content, &stats)
	profileLanguage(content, &stats)
	return stats
}

func profileDocumentLines(lines []string) types.DocumentStructureStats {
	stats := types.DocumentStructureStats{LineCount: len(lines)}
	previousWasQuestion := false
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		stats.NonEmptyLineCount++
		incrementHeadingCount(line, &stats.HeadingLevelCounts)
		if isListLine(line) {
			stats.ListLineCount++
		}
		if isTableLine(line) {
			stats.TableLineCount++
		}
		if previousWasQuestion && isAnswerLine(line) {
			stats.QuestionAnswerPairs++
		}
		previousWasQuestion = isQuestionLine(line)
	}
	stats.ListDensity = ratio(stats.ListLineCount, stats.NonEmptyLineCount)
	stats.TableDensity = ratio(stats.TableLineCount, stats.NonEmptyLineCount)
	return stats
}

func profileParagraphs(content string, stats *types.DocumentStructureStats) {
	paragraphs := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n\n")
	total := 0
	for _, paragraph := range paragraphs {
		length := len([]rune(strings.TrimSpace(paragraph)))
		if length == 0 {
			continue
		}
		stats.ParagraphCount++
		total += length
		if length > stats.MaxParagraphChars {
			stats.MaxParagraphChars = length
		}
	}
	if stats.ParagraphCount > 0 {
		stats.AverageParagraphChars = int(math.Round(float64(total) / float64(stats.ParagraphCount)))
	}
}

func profileLanguage(content string, stats *types.DocumentStructureStats) {
	runes := []rune(content)
	stats.CharacterCount = len(runes)
	for _, r := range runes {
		switch {
		case unicode.Is(unicode.Han, r):
			stats.Language.CJKCharacters++
		case unicode.Is(unicode.Latin, r):
			stats.Language.LatinCharacters++
		case unicode.IsDigit(r):
			stats.Language.DigitCharacters++
		}
	}
	stats.Language.CJKRatio = ratio(stats.Language.CJKCharacters, stats.CharacterCount)
	stats.Language.LatinRatio = ratio(stats.Language.LatinCharacters, stats.CharacterCount)
}

func sampleDocumentContent(runes []rune) types.DocumentContentSample {
	if len(runes) <= ingestionSampleBudget {
		return types.DocumentContentSample{Head: string(runes)}
	}
	middleStart := len(runes)/2 - ingestionSampleWindowSize/2
	return types.DocumentContentSample{
		Head:      string(runes[:ingestionSampleWindowSize]),
		Middle:    string(runes[middleStart : middleStart+ingestionSampleWindowSize]),
		Tail:      string(runes[len(runes)-ingestionSampleWindowSize:]),
		Truncated: true,
	}
}

func incrementHeadingCount(line string, counts *types.HeadingLevelCounts) {
	level := 0
	for level < len(line) && level < 6 && line[level] == '#' {
		level++
	}
	if level == 0 || len(line) <= level || line[level] != ' ' {
		return
	}
	switch level {
	case 1:
		counts.H1++
	case 2:
		counts.H2++
	case 3:
		counts.H3++
	case 4:
		counts.H4++
	case 5:
		counts.H5++
	case 6:
		counts.H6++
	}
}

func isListLine(line string) bool {
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") {
		return true
	}
	trimmed := strings.TrimLeft(line, "0123456789")
	return len(trimmed) < len(line) && (strings.HasPrefix(trimmed, ". ") || strings.HasPrefix(trimmed, ") "))
}

func isTableLine(line string) bool {
	return strings.Count(line, "|") >= 2 || strings.Count(line, "\t") >= 2
}

func isQuestionLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.HasPrefix(lower, "q:") || strings.HasPrefix(lower, "q：") ||
		strings.HasPrefix(line, "问：") || strings.HasPrefix(line, "问题：")
}

func isAnswerLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.HasPrefix(lower, "a:") || strings.HasPrefix(lower, "a：") ||
		strings.HasPrefix(line, "答：") || strings.HasPrefix(line, "回答：")
}

func ratio(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(part)/float64(total)*10000) / 10000
}
