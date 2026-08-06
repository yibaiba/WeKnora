package chunker

import (
	"sort"
	"strings"
	"unicode/utf8"
)

func relocateSemanticHints(
	source string,
	final string,
	hints []SemanticBlockHint,
	diagnostics *SemanticDiagnostics,
) []SemanticBlock {
	ordered := append([]SemanticBlockHint(nil), hints...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Start != ordered[j].Start {
			return ordered[i].Start < ordered[j].Start
		}
		return ordered[i].End < ordered[j].End
	})
	sourceRunes := []rune(source)
	finalRunes := []rune(final)
	cursor := 0
	result := make([]SemanticBlock, 0, len(ordered))
	for _, hint := range ordered {
		block, next, code := relocateSemanticHint(semanticHintRelocationRequest{
			source: sourceRunes, final: finalRunes, cursor: cursor, hint: hint,
		})
		if code != "" {
			rejectSemanticHint(diagnostics, code)
			continue
		}
		result = append(result, block)
		cursor = next
	}
	clearMissingSemanticParents(result, diagnostics)
	return result
}

type semanticHintRelocationRequest struct {
	source []rune
	final  []rune
	cursor int
	hint   SemanticBlockHint
}

func relocateSemanticHint(request semanticHintRelocationRequest) (SemanticBlock, int, string) {
	hint := request.hint
	if !semanticKindAllowed(hint.Kind) {
		return SemanticBlock{}, request.cursor, "hint_kind_invalid"
	}
	if hint.Start < 0 || hint.End <= hint.Start || hint.End > len(request.source) {
		return SemanticBlock{}, request.cursor, "hint_source_range_invalid"
	}
	fragment := string(request.source[hint.Start:hint.End])
	if strings.TrimSpace(fragment) == "" {
		return SemanticBlock{}, request.cursor, "hint_source_empty"
	}
	start := findSemanticFragment(request.final, semanticFragmentRequest{
		fragment: fragment, cursor: request.cursor, preferred: hint.Start,
	})
	if start < 0 {
		return SemanticBlock{}, request.cursor, "hint_source_unmatched"
	}
	end := start + utf8.RuneCountInString(fragment)
	confidence := hint.Confidence
	if confidence != SemanticConfidenceSoft {
		confidence = SemanticConfidenceHigh
	}
	return SemanticBlock{
		ID: hint.ID, Kind: hint.Kind, Start: start, End: end,
		ParentID: hint.ParentID, SectionDepth: hint.SectionDepth,
		TableID: hint.TableID, RecordID: hint.RecordID,
		Atomic: hint.Atomic, Confidence: confidence,
		ContextKinds: append([]string(nil), hint.ContextKinds...),
	}, end, ""
}

type semanticFragmentRequest struct {
	fragment  string
	cursor    int
	preferred int
}

func findSemanticFragment(final []rune, request semanticFragmentRequest) int {
	fragmentRunes := []rune(request.fragment)
	if request.preferred >= request.cursor && request.preferred+len(fragmentRunes) <= len(final) &&
		string(final[request.preferred:request.preferred+len(fragmentRunes)]) == request.fragment {
		return request.preferred
	}
	if request.cursor > len(final) {
		return -1
	}
	remainder := string(final[request.cursor:])
	byteOffset := strings.Index(remainder, request.fragment)
	if byteOffset < 0 {
		return -1
	}
	return request.cursor + utf8.RuneCountInString(remainder[:byteOffset])
}

func clearMissingSemanticParents(blocks []SemanticBlock, diagnostics *SemanticDiagnostics) {
	ids := make(map[string]struct{}, len(blocks))
	for _, block := range blocks {
		if block.ID != "" {
			ids[block.ID] = struct{}{}
		}
	}
	for index := range blocks {
		if blocks[index].ParentID == "" {
			continue
		}
		if _, ok := ids[blocks[index].ParentID]; ok {
			continue
		}
		blocks[index].ParentID = ""
		appendSemanticReason(diagnostics, "hint_parent_missing")
	}
}

func rejectSemanticHint(diagnostics *SemanticDiagnostics, code string) {
	diagnostics.HintsRejected++
	appendSemanticReason(diagnostics, code)
}

func appendSemanticReason(diagnostics *SemanticDiagnostics, code string) {
	for _, existing := range diagnostics.ReasonCodes {
		if existing == code {
			return
		}
	}
	diagnostics.ReasonCodes = append(diagnostics.ReasonCodes, code)
}
