package types

// IngestionSemanticDiagnostics stores only aggregate measurements and reason
// codes. It deliberately excludes source text, structure IDs, and positions.
type IngestionSemanticDiagnostics struct {
	SourceFormat                 string         `json:"source_format"`
	HintsProvided                int            `json:"hints_provided"`
	HintsAccepted                int            `json:"hints_accepted"`
	HintsRejected                int            `json:"hints_rejected"`
	HintAcceptanceRate           float64        `json:"hint_acceptance_rate"`
	HintRejectionReasonCounts    map[string]int `json:"hint_rejection_reason_counts"`
	StructureViolationCounts     map[string]int `json:"structure_violation_counts"`
	TokenCountModeCounts         map[string]int `json:"token_count_mode_counts"`
	CandidateCount               int            `json:"candidate_count"`
	ValidCandidateCount          int            `json:"valid_candidate_count"`
	NonHighestScoreCandidateUsed bool           `json:"non_highest_score_candidate_used"`
	FallbackApplied              bool           `json:"fallback_applied"`
	SelectedAverageChunkTokens   float64        `json:"selected_average_chunk_tokens"`
	SelectedContextTokenRatio    float64        `json:"selected_context_token_ratio"`
}

// IngestionShadowComparison records the sanitized difference between the
// existing production configuration and the V2 recommendation.
type IngestionShadowComparison struct {
	BaselineChunking      IngestionChunkingRecommendation `json:"baseline_chunking"`
	V2RecommendedChunking IngestionChunkingRecommendation `json:"v2_recommended_chunking"`
	V2AppliedMode         string                          `json:"v2_applied_mode"`
	V2SelectedCandidateID string                          `json:"v2_selected_candidate_id"`
	ChunkingChanged       bool                            `json:"chunking_changed"`
	ReasonCodes           []string                        `json:"reason_codes"`
}
