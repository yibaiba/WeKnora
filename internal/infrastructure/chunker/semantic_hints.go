package chunker

import (
	"sort"
	"strings"
	"unicode"
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
	request := semanticHintRelocationRequest{source: []rune(source), final: []rune(final)}
	result := make([]SemanticBlock, 0, len(ordered))
	for _, hint := range ordered {
		request.hint = hint
		block, next, code := relocateSemanticHint(request)
		if code != "" {
			rejectSemanticHint(diagnostics, code)
			continue
		}
		request.cursor = next
		result = append(result, block)
	}
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
	fragment := request.source[hint.Start:hint.End]
	if strings.TrimSpace(string(fragment)) == "" {
		return SemanticBlock{}, request.cursor, "hint_source_empty"
	}
	match, code := findSemanticFragment(request.final, semanticFragmentRequest{
		fragment: fragment, cursor: request.cursor, preferred: hint.Start,
	})
	if code != "" {
		return SemanticBlock{}, request.cursor, code
	}
	confidence := hint.Confidence
	if confidence != SemanticConfidenceSoft {
		confidence = SemanticConfidenceHigh
	}
	return SemanticBlock{
		ID: hint.ID, Kind: hint.Kind, Start: match.start, End: match.end,
		ParentID: hint.ParentID, SectionDepth: hint.SectionDepth,
		TableID: hint.TableID, RecordID: hint.RecordID,
		Atomic: hint.Atomic, Confidence: confidence,
		ContextKinds: append([]string(nil), hint.ContextKinds...),
	}, match.end, ""
}

type semanticFragmentRequest struct {
	fragment  []rune
	cursor    int
	preferred int
}

type semanticFragmentMatch struct {
	start int
	end   int
}

func findSemanticFragment(final []rune, request semanticFragmentRequest) (semanticFragmentMatch, string) {
	if semanticRunesEqualAt(final, request.fragment, request.preferred, request.cursor) {
		return semanticFragmentMatch{start: request.preferred, end: request.preferred + len(request.fragment)}, ""
	}
	exact := findExactSemanticMatches(final, request.fragment, request.cursor)
	if len(exact) == 1 {
		return semanticFragmentMatch{start: exact[0], end: exact[0] + len(request.fragment)}, ""
	}
	if len(exact) > 1 {
		return semanticFragmentMatch{}, "hint_source_ambiguous"
	}
	return findNormalizedSemanticFragment(final, request.fragment, request.cursor)
}

func semanticRunesEqualAt(final, fragment []rune, start, cursor int) bool {
	if start < cursor || start < 0 || start+len(fragment) > len(final) {
		return false
	}
	return string(final[start:start+len(fragment)]) == string(fragment)
}

func findExactSemanticMatches(final, fragment []rune, cursor int) []int {
	if cursor < 0 || cursor > len(final) || len(fragment) > len(final)-cursor {
		return nil
	}
	return findSemanticRuneMatches(final, fragment, cursor)
}

type normalizedSemanticText struct {
	value  []rune
	starts []int
	ends   []int
}

func findNormalizedSemanticFragment(
	final []rune,
	fragment []rune,
	cursor int,
) (semanticFragmentMatch, string) {
	normalizedFinal := normalizeSemanticText(final, 0)
	normalizedFragment := normalizeSemanticText(fragment, 0)
	positions := findSemanticRuneMatches(normalizedFinal.value, normalizedFragment.value, 0)
	matches := make([]semanticFragmentMatch, 0, len(positions))
	for _, start := range positions {
		if normalizedFinal.starts[start] < cursor {
			continue
		}
		last := start + len(normalizedFragment.value) - 1
		matches = append(matches, semanticFragmentMatch{
			start: normalizedFinal.starts[start], end: normalizedFinal.ends[last],
		})
	}
	if len(matches) == 1 {
		return matches[0], ""
	}
	if len(matches) > 1 {
		return semanticFragmentMatch{}, "hint_normalized_ambiguous"
	}
	return semanticFragmentMatch{}, "hint_source_unmatched"
}

func findSemanticRuneMatches(haystack, needle []rune, start int) []int {
	if len(needle) == 0 || start < 0 || start > len(haystack) {
		return nil
	}
	prefix := make([]int, len(needle))
	for index, matched := 1, 0; index < len(needle); index++ {
		for matched > 0 && needle[index] != needle[matched] {
			matched = prefix[matched-1]
		}
		if needle[index] == needle[matched] {
			matched++
		}
		prefix[index] = matched
	}
	matches := make([]int, 0, 2)
	for index, matched := start, 0; index < len(haystack); index++ {
		for matched > 0 && haystack[index] != needle[matched] {
			matched = prefix[matched-1]
		}
		if haystack[index] == needle[matched] {
			matched++
		}
		if matched == len(needle) {
			matches = append(matches, index-len(needle)+1)
			matched = prefix[matched-1]
		}
	}
	return matches
}

func normalizeSemanticText(value []rune, base int) normalizedSemanticText {
	result := normalizedSemanticText{
		value: make([]rune, 0, len(value)), starts: make([]int, 0, len(value)), ends: make([]int, 0, len(value)),
	}
	for index := 0; index < len(value); {
		start := index
		if unicode.IsSpace(value[index]) {
			for index < len(value) && unicode.IsSpace(value[index]) {
				index++
			}
			result.value = append(result.value, ' ')
		} else {
			result.value = append(result.value, value[index])
			index++
		}
		result.starts = append(result.starts, base+start)
		result.ends = append(result.ends, base+index)
	}
	return result
}

type semanticHintRelations struct {
	seenIDs   map[string]string
	recordIDs map[string]struct{}
}

func newSemanticHintRelations() semanticHintRelations {
	return semanticHintRelations{
		seenIDs: make(map[string]string), recordIDs: make(map[string]struct{}),
	}
}

func (relations *semanticHintRelations) validate(block SemanticBlock) (SemanticBlock, string) {
	if block.ID != "" {
		if _, duplicate := relations.seenIDs[block.ID]; duplicate {
			return SemanticBlock{}, "hint_id_duplicate"
		}
	}
	if block.ParentID != "" {
		parentKind, ok := relations.seenIDs[block.ParentID]
		if !ok {
			return SemanticBlock{}, "hint_parent_missing_or_forward"
		}
		if parentKind != SemanticKindHeading {
			return SemanticBlock{}, "hint_parent_not_heading"
		}
	}
	if code := validateSemanticHintFields(block); code != "" {
		return SemanticBlock{}, code
	}
	if block.RecordID != "" {
		if _, duplicate := relations.recordIDs[block.RecordID]; duplicate {
			return SemanticBlock{}, "hint_record_id_duplicate"
		}
	}
	if len(block.ContextKinds) == 0 {
		block.ContextKinds = canonicalSemanticContextKinds(block.Kind)
	}
	return block, ""
}

func (relations *semanticHintRelations) commit(block SemanticBlock) {
	if block.ID != "" {
		relations.seenIDs[block.ID] = block.Kind
	}
	if block.RecordID != "" {
		relations.recordIDs[block.RecordID] = struct{}{}
	}
}

func validateSemanticHintFields(block SemanticBlock) string {
	if block.Kind == SemanticKindHeading {
		if block.SectionDepth < 1 || block.SectionDepth > 6 {
			return "hint_section_depth_invalid"
		}
	} else if block.SectionDepth != 0 {
		return "hint_section_depth_invalid"
	}
	isTable := block.Kind == SemanticKindTableHeader || block.Kind == SemanticKindTableRow
	if isTable != (block.TableID != "") {
		return "hint_table_relation_invalid"
	}
	isRecord := block.Kind == SemanticKindRecord
	if isRecord != (block.RecordID != "") {
		return "hint_record_relation_invalid"
	}
	if len(block.ContextKinds) > 0 && !semanticContextKindsMatch(block.Kind, block.ContextKinds) {
		return "hint_context_kinds_invalid"
	}
	return ""
}

func rejectSemanticHint(diagnostics *SemanticDiagnostics, code string) {
	diagnostics.HintsRejected++
	if diagnostics.ReasonCodeCounts == nil {
		diagnostics.ReasonCodeCounts = make(map[string]int)
	}
	diagnostics.ReasonCodeCounts[code]++
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
