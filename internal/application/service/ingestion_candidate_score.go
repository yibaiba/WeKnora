package service

import (
	"math"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	structureIntegrityWeight = 40.0
	chunkSizeBalanceWeight   = 25.0
	boundaryQualityWeight    = 15.0
	overlapEfficiencyWeight  = 10.0
	parentChildWeight        = 10.0
)

type sourceSpan struct {
	kind       string
	start, end int
}

type ingestionCandidateMetrics struct {
	lengths   types.IngestionLengthDistribution
	structure types.IngestionStructureMetrics
	score     types.IngestionCandidateScore
}

func ingestionPreviewMetrics(
	content string,
	chunks, parents []chunker.Chunk,
	parentIndexes []int,
	config types.IngestionChunkingRecommendation,
	scoreConfig chunker.SplitterConfig,
) ingestionCandidateMetrics {
	spans := ingestionStructureSpans(content)
	structure, structureRatio := scoreStructureRetention(spans, chunks)
	score := types.IngestionCandidateScore{
		StructureIntegrity: roundScore(structureRatio * structureIntegrityWeight),
		ChunkSizeBalance:   roundScore(scoreChunkSizeBalance(chunks, scoreConfig.ChunkSize) * chunkSizeBalanceWeight),
		BoundaryQuality:    roundScore(scoreBoundaryQuality(content, chunks, spans, config.Separators) * boundaryQualityWeight),
		OverlapEfficiency:  roundScore(scoreOverlapEfficiency(chunks, scoreConfig.ChunkOverlap) * overlapEfficiencyWeight),
		ParentChild:        roundScore(scoreParentChild(chunks, parents, parentIndexes, config.EnableParentChild) * parentChildWeight),
	}
	score.Total = roundScore(score.StructureIntegrity + score.ChunkSizeBalance +
		score.BoundaryQuality + score.OverlapEfficiency + score.ParentChild)
	return ingestionCandidateMetrics{
		lengths: ingestionLengthDistribution(chunks), structure: structure, score: score,
	}
}

func scoreStructureRetention(
	spans []sourceSpan,
	chunks []chunker.Chunk,
) (types.IngestionStructureMetrics, float64) {
	metrics := types.IngestionStructureMetrics{
		HeadingRetention: 1, FAQRetention: 1, TableRetention: 1,
	}
	kinds := []string{"heading", "faq", "table"}
	rates := make([]float64, 0, len(kinds))
	for _, kind := range kinds {
		total, retained := 0, 0
		for _, span := range spans {
			if span.kind != kind {
				continue
			}
			total++
			if spanContainedByChunk(span, chunks) {
				retained++
			}
		}
		if total == 0 {
			continue
		}
		rate := float64(retained) / float64(total)
		metrics.PresentTypes = append(metrics.PresentTypes, kind)
		rates = append(rates, rate)
		switch kind {
		case "heading":
			metrics.HeadingRetention = roundScore(rate)
		case "faq":
			metrics.FAQRetention = roundScore(rate)
		case "table":
			metrics.TableRetention = roundScore(rate)
		}
	}
	if len(rates) == 0 {
		return metrics, 1
	}
	total := 0.0
	for _, rate := range rates {
		total += rate
	}
	return metrics, total / float64(len(rates))
}

func spanContainedByChunk(span sourceSpan, chunks []chunker.Chunk) bool {
	for _, current := range chunks {
		if span.start >= current.Start && span.end <= current.End {
			return true
		}
	}
	return false
}

func scoreChunkSizeBalance(chunks []chunker.Chunk, target int) float64 {
	if len(chunks) == 0 || target <= 0 {
		return 0
	}
	checked := chunks
	if len(chunks) > 1 {
		checked = chunks[:len(chunks)-1]
	}
	minimum := float64(target) * 0.5
	maximum := float64(target) * 1.25
	balanced := 0
	for _, current := range checked {
		length := float64(len([]rune(current.Content)))
		if length >= minimum && length <= maximum {
			balanced++
		}
	}
	return float64(balanced) / float64(len(checked))
}

func scoreBoundaryQuality(
	content string,
	chunks []chunker.Chunk,
	spans []sourceSpan,
	separators []string,
) float64 {
	if len(chunks) <= 1 {
		return 1
	}
	boundaries := make(map[int]struct{}, len(spans)*2)
	for _, span := range spans {
		boundaries[span.start] = struct{}{}
		boundaries[span.end] = struct{}{}
	}
	runes := []rune(content)
	hits := 0
	for _, current := range chunks[:len(chunks)-1] {
		if _, ok := boundaries[current.End]; ok || separatorEndsAt(runes, current.End, separators) {
			hits++
		}
	}
	return float64(hits) / float64(len(chunks)-1)
}

func separatorEndsAt(content []rune, boundary int, separators []string) bool {
	for _, separator := range separators {
		value := []rune(separator)
		if len(value) == 0 || boundary < len(value) {
			continue
		}
		if string(content[boundary-len(value):boundary]) == separator {
			return true
		}
	}
	return false
}

func scoreOverlapEfficiency(chunks []chunker.Chunk, target int) float64 {
	if len(chunks) <= 1 {
		return 1
	}
	total := 0.0
	for index := 1; index < len(chunks); index++ {
		actual := max(0, chunks[index-1].End-chunks[index].Start)
		if target == 0 {
			if actual == 0 {
				total++
			}
			continue
		}
		difference := math.Abs(float64(actual-target)) / float64(target)
		total += math.Max(0, 1-difference)
	}
	return total / float64(len(chunks)-1)
}

func scoreParentChild(
	children, parents []chunker.Chunk,
	parentIndexes []int,
	enabled bool,
) float64 {
	if !enabled {
		return 1
	}
	if len(children) == 0 || len(children) != len(parentIndexes) {
		return 0
	}
	consistent := 0
	for index, parentIndex := range parentIndexes {
		// The production chunker uses -1 for a self-contained child whose
		// parent would be byte-for-byte identical and therefore is not stored.
		if parentIndex == -1 {
			consistent++
			continue
		}
		if parentIndex < 0 || parentIndex >= len(parents) {
			continue
		}
		child, parent := children[index], parents[parentIndex]
		if child.Start >= parent.Start && child.End <= parent.End {
			consistent++
		}
	}
	return float64(consistent) / float64(len(children))
}

func ingestionStructureSpans(content string) []sourceSpan {
	lines := ingestionLineSpans(content)
	runes := []rune(content)
	spans := make([]sourceSpan, 0)
	var pendingQuestion *sourceSpan
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		trimmed := strings.TrimSpace(string(runes[line.start:line.end]))
		if isMarkdownHeading(trimmed) {
			spans = append(spans, sourceSpan{kind: "heading", start: line.start, end: line.end})
		}
		if isQuestionLine(trimmed) {
			question := line
			pendingQuestion = &question
		} else if pendingQuestion != nil && isAnswerLine(trimmed) {
			spans = append(spans, sourceSpan{kind: "faq", start: pendingQuestion.start, end: line.end})
			pendingQuestion = nil
		} else if trimmed != "" {
			pendingQuestion = nil
		}
		if !isTableLine(trimmed) {
			continue
		}
		start := line.start
		end := line.end
		for index+1 < len(lines) {
			next := lines[index+1]
			nextText := strings.TrimSpace(string(runes[next.start:next.end]))
			if !isTableLine(nextText) {
				break
			}
			index++
			end = next.end
		}
		spans = append(spans, sourceSpan{kind: "table", start: start, end: end})
	}
	return spans
}

func ingestionLineSpans(content string) []sourceSpan {
	runes := []rune(content)
	result := make([]sourceSpan, 0, strings.Count(content, "\n")+1)
	start := 0
	for index, current := range runes {
		if current != '\n' {
			continue
		}
		result = append(result, sourceSpan{start: start, end: index + 1})
		start = index + 1
	}
	if start < len(runes) || len(runes) == 0 {
		result = append(result, sourceSpan{start: start, end: len(runes)})
	}
	return result
}

func isMarkdownHeading(line string) bool {
	level := 0
	for level < len(line) && level < 6 && line[level] == '#' {
		level++
	}
	return level > 0 && len(line) > level && line[level] == ' '
}

func roundScore(value float64) float64 {
	return math.Round(value*100) / 100
}
