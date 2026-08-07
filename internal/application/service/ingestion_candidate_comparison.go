package service

import "github.com/Tencent/WeKnora/internal/types"

func ingestionEvidenceScoreDimensions(evidence ingestionDocumentEvidence) []string {
	dimensions := []string{"semantic_integrity", "boundary_quality"}
	if containsString(evidence.RiskSignals, "oversize_atomic") ||
		containsString(evidence.RiskSignals, "ocr_noise") {
		dimensions = append(dimensions, "size_fit")
	}
	if hasAnyString(evidence.DominantStructures,
		"section_body", "table", "repeated_records", "list", "code", "image_text") {
		dimensions = append(dimensions, "context_efficiency")
	}
	if hasAnyString(evidence.DominantStructures, "section_body", "mixed") {
		dimensions = append(dimensions, "parent_child")
	}
	return dimensions
}

func attachIngestionComparisonFacts(
	candidates []types.IngestionChunkingCandidate,
	dimensions []string,
) {
	reference := highestValidIngestionCandidate(candidates)
	for index := range candidates {
		candidates[index].ComparisonFacts = compareIngestionCandidate(
			candidates[index], reference, dimensions,
		)
	}
}

func highestValidIngestionCandidate(
	candidates []types.IngestionChunkingCandidate,
) *types.IngestionChunkingCandidate {
	var highest *types.IngestionChunkingCandidate
	for index := range candidates {
		candidate := &candidates[index]
		if !candidate.HardValid || highest != nil && candidate.Score.Total <= highest.Score.Total {
			continue
		}
		highest = candidate
	}
	return highest
}

func compareIngestionCandidate(
	candidate types.IngestionChunkingCandidate,
	reference *types.IngestionChunkingCandidate,
	dimensions []string,
) types.IngestionCandidateComparisonFacts {
	facts := types.IngestionCandidateComparisonFacts{}
	if reference == nil {
		facts.ReasonCodes = []string{"no_hard_valid_candidate"}
		return facts
	}
	facts.ReferenceCandidateID = reference.ID
	facts.TotalScoreGap = roundScore(max(0, reference.Score.Total-candidate.Score.Total))
	if !candidate.HardValid {
		facts.ReasonCodes = []string{"hard_validation_failed"}
		return facts
	}
	if candidate.ID == reference.ID {
		facts.SelectionEligible = true
		facts.ReasonCodes = []string{"highest_total_score"}
		return facts
	}
	for _, dimension := range dimensions {
		advantage := candidateScoreDimension(candidate.Score, dimension) -
			candidateScoreDimension(reference.Score, dimension)
		if advantage >= 5 {
			facts.EvidenceAdvantages = append(facts.EvidenceAdvantages, dimension)
		}
	}
	return finalizeCandidateComparison(facts)
}

func finalizeCandidateComparison(
	facts types.IngestionCandidateComparisonFacts,
) types.IngestionCandidateComparisonFacts {
	if facts.TotalScoreGap > 5 {
		facts.ReasonCodes = []string{"total_score_gap_exceeded"}
		return facts
	}
	if len(facts.EvidenceAdvantages) == 0 {
		facts.ReasonCodes = []string{"no_evidence_dimension_advantage"}
		return facts
	}
	facts.SelectionEligible = true
	for _, dimension := range facts.EvidenceAdvantages {
		facts.ReasonCodes = append(facts.ReasonCodes, "evidence_"+dimension+"_advantage")
	}
	return facts
}

func candidateScoreDimension(score types.IngestionCandidateScore, dimension string) float64 {
	switch dimension {
	case "semantic_integrity":
		return score.SemanticIntegrity
	case "boundary_quality":
		return score.BoundaryQuality
	case "size_fit":
		return score.SizeFit
	case "context_efficiency":
		return score.ContextEfficiency
	case "parent_child":
		return score.ParentChild
	default:
		return 0
	}
}

func hasAnyString(values []string, expected ...string) bool {
	for _, value := range values {
		if containsString(expected, value) {
			return true
		}
	}
	return false
}
