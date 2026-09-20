package service

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

func localIngestionEvidenceFromDocument(
	document chunker.SemanticDocument,
) (ingestionDocumentEvidence, bool) {
	if ingestionDocumentNeedsMapReduce(document) {
		return ingestionDocumentEvidence{}, false
	}
	counts := map[string]int{}
	for _, block := range document.Blocks {
		counts[block.Kind]++
	}
	dominant := make([]string, 0, 4)
	boundaries := make([]string, 0, 4)
	if counts[chunker.SemanticKindTableRow] > 0 || counts[chunker.SemanticKindTableHeader] > 0 {
		dominant = append(dominant, "table")
		boundaries = append(boundaries, "table_row")
	}
	if counts[chunker.SemanticKindFAQ] > 0 {
		dominant = append(dominant, "faq")
		boundaries = append(boundaries, "faq_pair")
	}
	if counts[chunker.SemanticKindCodeBlock] > 0 {
		dominant = append(dominant, "code")
		boundaries = append(boundaries, "code_block")
	}
	if counts[chunker.SemanticKindRecord] > 0 {
		dominant = append(dominant, "repeated_records")
		boundaries = append(boundaries, "record")
	}
	if len(dominant) == 0 {
		return ingestionDocumentEvidence{}, false
	}
	kind := types.IngestionDocumentKindTabularData
	switch {
	case containsString(dominant, "faq") && len(dominant) == 1:
		kind = types.IngestionDocumentKindFAQ
	case containsString(dominant, "table") && len(dominant) == 1:
		kind = types.IngestionDocumentKindTabularData
	default:
		kind = types.IngestionDocumentKindMixedDocument
	}
	return ingestionDocumentEvidence{
		Summary:                "high-confidence structure packed from the semantic tree",
		DocumentKindCandidates: []string{kind},
		ContentModeCandidates:  []string{types.IngestionContentModeDocument},
		DominantStructures:     dominant,
		BoundaryPriorities:     boundaries,
	}, true
}

func ingestionDocumentNeedsMapReduce(document chunker.SemanticDocument) bool {
	softHeading := false
	images := 0
	structural := 0
	narrative := 0
	for _, block := range document.Blocks {
		switch block.Kind {
		case chunker.SemanticKindHeading:
			if block.Confidence == chunker.SemanticConfidenceSoft {
				softHeading = true
			}
		case chunker.SemanticKindImage:
			images++
		case chunker.SemanticKindTableHeader, chunker.SemanticKindTableRow,
			chunker.SemanticKindFAQ, chunker.SemanticKindCodeBlock, chunker.SemanticKindRecord:
			structural++
		case chunker.SemanticKindParagraph, chunker.SemanticKindListItem, chunker.SemanticKindPreamble:
			narrative += block.End - block.Start
		}
	}
	if softHeading {
		return true
	}
	if images > 0 && structural == 0 {
		return true
	}
	if structural == 0 {
		return true
	}
	if narrative > 200 && structural > 0 {
		return true
	}
	return false
}

func firstIngestionDocumentKind(evidence ingestionDocumentEvidence) string {
	for _, kind := range evidence.DocumentKindCandidates {
		if strings.TrimSpace(kind) != "" {
			return kind
		}
	}
	return types.IngestionDocumentKindMixedDocument
}

func (s *ingestionAgentSession) hardRulesSeparated() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hardValid := 0
	for _, candidate := range s.candidates {
		if candidate.HardValid {
			hardValid++
		}
	}
	// Skip the agent only when exactly one packing variant survives hard
	// validation. Zero valid variants still need the fallback tool; a single
	// SelectionEligible winner among several valid variants is the normal
	// scoring outcome and still needs the agent on mixed/prose documents.
	return hardValid == 1
}

func (s *ingestionAgentSession) autoSubmitFromEvidence(evidence ingestionDocumentEvidence) error {
	if s.fallbackReady() {
		_, err := s.submitFallback(submitIngestionFallbackInput{
			DocumentKind:           firstIngestionDocumentKind(evidence),
			Confidence:             0.7,
			RecommendedContentMode: types.IngestionContentModeDocument,
			ReasonCodes:            []string{"hard_rules_separated"},
			Summary:                defaultIngestionEvidenceSummary(evidence),
		})
		return err
	}
	id := s.defaultCandidateID()
	if id == "" {
		return nil
	}
	_, err := s.submit(submitIngestionDecisionInput{
		CandidateID:            id,
		DocumentKind:           firstIngestionDocumentKind(evidence),
		Confidence:             0.85,
		RecommendedContentMode: types.IngestionContentModeDocument,
		ReasonCodes:            []string{"hard_rules_separated"},
		Summary:                defaultIngestionEvidenceSummary(evidence),
	})
	return err
}

func defaultIngestionEvidenceSummary(evidence ingestionDocumentEvidence) string {
	if strings.TrimSpace(evidence.Summary) != "" {
		return evidence.Summary
	}
	return "hard rules already separated packing variants"
}
