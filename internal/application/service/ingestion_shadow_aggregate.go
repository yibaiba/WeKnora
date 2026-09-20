package service

import (
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

// ShadowKindAggregate is the source_format × kind rollup used by the V2
// shadow dashboard. It contains only counts and rates, never source text.
type ShadowKindAggregate struct {
	SourceFormat             string
	Kind                     string
	Documents                int
	HintAcceptanceRateSum    float64
	HintAcceptanceSamples    int
	StructureViolationCounts map[string]int
	KindViolationCounts      map[string]int
	FallbackCount            int
	ValidCandidateCount      int
	CandidateCount           int
}

func AggregateShadowDiagnostics(
	items []types.IngestionSemanticDiagnostics,
) []ShadowKindAggregate {
	byKey := make(map[string]*ShadowKindAggregate)
	for _, item := range items {
		format := item.SourceFormat
		if format == "" {
			format = "unknown"
		}
		kinds := shadowDiagnosticKinds(item)
		for _, kind := range kinds {
			key := format + "\x00" + kind
			row := byKey[key]
			if row == nil {
				row = &ShadowKindAggregate{
					SourceFormat:             format,
					Kind:                     kind,
					StructureViolationCounts: make(map[string]int),
					KindViolationCounts:      make(map[string]int),
				}
				byKey[key] = row
			}
			row.Documents++
			if item.HintsProvided > 0 {
				row.HintAcceptanceRateSum += item.HintAcceptanceRate
				row.HintAcceptanceSamples++
			}
			if item.FallbackApplied {
				row.FallbackCount++
			}
			row.ValidCandidateCount += item.ValidCandidateCount
			row.CandidateCount += item.CandidateCount
			for code, count := range item.StructureViolationCounts {
				row.StructureViolationCounts[code] += count
			}
			for code, count := range item.KindViolationCounts {
				if shadowKindOwnsViolation(kind, code) {
					row.KindViolationCounts[code] += count
				}
			}
		}
	}
	result := make([]ShadowKindAggregate, 0, len(byKey))
	for _, row := range byKey {
		result = append(result, *row)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SourceFormat != result[j].SourceFormat {
			return result[i].SourceFormat < result[j].SourceFormat
		}
		return result[i].Kind < result[j].Kind
	})
	return result
}

func (row ShadowKindAggregate) HintAcceptanceRate() float64 {
	if row.HintAcceptanceSamples == 0 {
		return 0
	}
	return row.HintAcceptanceRateSum / float64(row.HintAcceptanceSamples)
}

func shadowKindOwnsViolation(kind, code string) bool {
	if kind == "all" || strings.HasPrefix(code, kind+":") {
		return true
	}
	switch kind {
	case chunker.SemanticKindTableRow, chunker.SemanticKindTableHeader:
		return strings.HasPrefix(code, "table:")
	case chunker.SemanticKindCodeBlock:
		return strings.HasPrefix(code, "code:")
	case chunker.SemanticKindFAQ:
		return strings.HasPrefix(code, "faq:")
	case chunker.SemanticKindRecord:
		return strings.HasPrefix(code, "record:")
	default:
		return false
	}
}

func shadowDiagnosticKinds(item types.IngestionSemanticDiagnostics) []string {
	seen := map[string]struct{}{"all": {}}
	for kind := range item.KindBlockCounts {
		if kind != "" {
			seen[kind] = struct{}{}
		}
	}
	for code := range item.KindViolationCounts {
		if index := strings.IndexByte(code, ':'); index > 0 {
			seen[code[:index]] = struct{}{}
		}
	}
	kinds := make([]string, 0, len(seen))
	for kind := range seen {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}
