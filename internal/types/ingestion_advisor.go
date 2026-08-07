package types

import "time"

const (
	IngestionAdvisorModeSmart = "smart"
	IngestionAdvisorModeOff   = "off"
)

const (
	IngestionDocumentKindPolicyManual  = "policy_manual"
	IngestionDocumentKindFAQ           = "faq"
	IngestionDocumentKindTabularData   = "tabular_data"
	IngestionDocumentKindReport        = "report"
	IngestionDocumentKindMeetingNotes  = "meeting_notes"
	IngestionDocumentKindPresentation  = "presentation"
	IngestionDocumentKindShortArticle  = "short_article"
	IngestionDocumentKindMixedDocument = "mixed_document"
)

const (
	IngestionContentModeDocument      = "document"
	IngestionContentModeFAQCandidate  = "faq_candidate"
	IngestionContentModeWikiCandidate = "wiki_candidate"
)

const (
	IngestionAppliedModeSmart    = "smart"
	IngestionAppliedModeFallback = "fallback"
	IngestionAppliedModeShadow   = "shadow"
)

// IngestionAdvisorConfig opts a file upload or reparse into document analysis.
// A nil config preserves the historical processing path.
type IngestionAdvisorConfig struct {
	Mode             string `json:"mode"`
	AllowWebAccess   bool   `json:"allow_web_access,omitempty"`
	AllowReadOnlyMCP bool   `json:"allow_read_only_mcp,omitempty"`
}

// IngestionAdvisorRequest is immutable input to the advisor boundary.
type IngestionAdvisorRequest struct {
	Content             string
	KnowledgeID         string
	KnowledgeBaseID     string
	KnowledgeBaseName   string
	KnowledgeBaseType   string
	TenantID            uint64
	VectorEnabled       bool
	KeywordEnabled      bool
	GraphEnabled        bool
	WikiEnabled         bool
	ModelID             string
	AllowWebAccess      bool
	AllowReadOnlyMCP    bool
	ChunkingConstraints IngestionChunkingConstraints
	FallbackChunking    IngestionChunkingRecommendation
	SemanticDocument    *SemanticDocument
	ProgressFn          func(IngestionAgentStep)
	AnalysisProgressFn  func(IngestionDocumentAnalysisProgress)
	Timeout             time.Duration
}

type IngestionDocumentAnalysisProgress struct {
	Phase                 string
	Status                string
	ContextWindowTokens   int
	CompletionTokenBudget int
	PromptSchemaTokens    int
	SafetyTokens          int
	ContentTokenBudget    int
	EstimatedSourceTokens int
	UnitCount             int
	Completed             int
	RetryCount            int
	FailedUnitAttempts    int
	Level                 int
	DurationMS            int64
	CoveredCharacters     int
	Failed                bool
	FailureKind           string
	FailedUnit            int
	ProviderFailureKind   string
	HTTPStatus            int
	FailureParameter      string
}

// IngestionChunkingConstraints are knowledge-base-owned splitter inputs. The
// advisor may observe them when previewing but never overwrite them.
type IngestionChunkingConstraints struct {
	TokenLimit      int
	Languages       []string
	TokenCounter    TokenCounter
	EmbeddingPrefix string `json:"-"`
}

type TokenCount struct {
	Count       int
	Mode        string
	TokenizerID string
}

type TokenCounter interface {
	Count(text string) (TokenCount, error)
}

type IngestionAgentWarning struct {
	Code    string `json:"code"`
	Tool    string `json:"tool,omitempty"`
	Message string `json:"message"`
}

type IngestionAgentStep struct {
	ToolCallID        string  `json:"-"`
	Round             int     `json:"round"`
	ToolName          string  `json:"tool_name"`
	Status            string  `json:"status"`
	DurationMS        int64   `json:"duration_ms,omitempty"`
	CandidateID       string  `json:"candidate_id,omitempty"`
	Score             float64 `json:"score,omitempty"`
	FailureCode       string  `json:"failure_code,omitempty"`
	FailureField      string  `json:"failure_field,omitempty"`
	FailureConstraint string  `json:"failure_constraint,omitempty"`
}

type IngestionAgentRun struct {
	MaxRounds      int                     `json:"max_rounds"`
	ActualRounds   int                     `json:"actual_rounds"`
	AvailableTools []string                `json:"available_tools"`
	Warnings       []IngestionAgentWarning `json:"warnings"`
	Steps          []IngestionAgentStep    `json:"steps"`
	StopReason     string                  `json:"stop_reason"`
}

type IngestionAdvisorResult struct {
	Analysis             *IngestionAnalysis
	Candidates           []IngestionChunkingCandidate
	SelectedCandidateID  string
	SelectionReasonCodes []string
	AgentRun             IngestionAgentRun
	SemanticDocument     *SemanticDocument
}

// IngestionChunkingRecommendation contains only fields the advisor may own.
type IngestionChunkingRecommendation struct {
	Strategy          string   `json:"strategy"`
	ChunkSize         int      `json:"chunk_size"`
	ChunkOverlap      int      `json:"chunk_overlap"`
	EnableParentChild bool     `json:"enable_parent_child"`
	ParentChunkSize   int      `json:"parent_chunk_size"`
	ChildChunkSize    int      `json:"child_chunk_size"`
	Separators        []string `json:"separators"`
}

type IngestionLengthDistribution struct {
	Minimum int     `json:"minimum"`
	Maximum int     `json:"maximum"`
	Average float64 `json:"average"`
	P50     int     `json:"p50"`
	P95     int     `json:"p95"`
}

type IngestionStructureMetrics struct {
	PresentTypes     []string `json:"present_types"`
	HeadingRetention float64  `json:"heading_retention"`
	FAQRetention     float64  `json:"faq_retention"`
	TableRetention   float64  `json:"table_retention"`
}

type IngestionCandidateScore struct {
	SemanticIntegrity float64 `json:"semantic_integrity"`
	BoundaryQuality   float64 `json:"boundary_quality"`
	SizeFit           float64 `json:"size_fit"`
	ContextEfficiency float64 `json:"context_efficiency"`
	ParentChild       float64 `json:"parent_child"`
	Total             float64 `json:"total"`
}

type IngestionStructureQuality struct {
	OrphanTableRows         int `json:"orphan_table_rows"`
	HeaderlessContinuations int `json:"headerless_continuations"`
	SplitAtomicBlocks       int `json:"split_atomic_blocks"`
	MixedSections           int `json:"mixed_sections"`
	OversizeAtomicBlocks    int `json:"oversize_atomic_blocks"`
}

// IngestionChunkStructureDescription deliberately excludes source text,
// structure identifiers, and positions so previews cannot disclose content.
type IngestionChunkStructureDescription struct {
	Index             int      `json:"index"`
	Kinds             []string `json:"kinds"`
	SectionDepth      int      `json:"section_depth"`
	HasContext        bool     `json:"has_context"`
	TableContinuation bool     `json:"table_continuation"`
	ParentMapped      bool     `json:"parent_mapped"`
}

type IngestionTierRejection struct {
	Tier   string `json:"tier"`
	Reason string `json:"reason"`
}

type IngestionChunkerDiagnostics struct {
	SelectedTier       string                   `json:"selected_tier"`
	TierChain          []string                 `json:"tier_chain"`
	Rejected           []IngestionTierRejection `json:"rejected"`
	ContextReasonCodes []string                 `json:"context_reason_codes,omitempty"`
}

type IngestionChunkingCandidate struct {
	ID                   string                               `json:"id"`
	Archetype            string                               `json:"archetype"`
	TokenCountMode       string                               `json:"token_count_mode"`
	TokenizerID          string                               `json:"tokenizer_id"`
	PackingPolicyVersion string                               `json:"packing_policy_version"`
	Config               IngestionChunkingRecommendation      `json:"config"`
	ChunkCount           int                                  `json:"chunk_count"`
	ParentChunkCount     int                                  `json:"parent_chunk_count"`
	Lengths              IngestionLengthDistribution          `json:"lengths"`
	Structure            IngestionStructureMetrics            `json:"structure"`
	StructureQuality     IngestionStructureQuality            `json:"structure_quality"`
	BlockDescriptions    []IngestionChunkStructureDescription `json:"block_descriptions"`
	Diagnostics          IngestionChunkerDiagnostics          `json:"diagnostics"`
	Score                IngestionCandidateScore              `json:"score"`
	HardValid            bool                                 `json:"hard_valid"`
	Violations           []string                             `json:"violations"`
	ContextTokenRatio    float64                              `json:"context_token_ratio"`
	ComparisonFacts      IngestionCandidateComparisonFacts    `json:"comparison_facts"`
}

type IngestionCandidateComparisonFacts struct {
	ReferenceCandidateID string   `json:"reference_candidate_id"`
	TotalScoreGap        float64  `json:"total_score_gap"`
	EvidenceAdvantages   []string `json:"evidence_advantages"`
	SelectionEligible    bool     `json:"selection_eligible"`
	ReasonCodes          []string `json:"reason_codes"`
}

// SemanticPackingPolicy is derived from validated full-document evidence.
// Callers receive value copies so an Agent cannot alter packing behavior.
type SemanticPackingPolicy struct {
	Version                     string   `json:"version"`
	TrustSoftHeadings           bool     `json:"trust_soft_headings"`
	StrongBoundaryOrder         []string `json:"strong_boundary_order"`
	SeparateRecords             bool     `json:"separate_records"`
	PreserveRepeatedPageRegions bool     `json:"preserve_repeated_page_regions"`
	ContextTokenPercent         int      `json:"context_token_percent"`
	ContextTokenLimit           int      `json:"context_token_limit"`
}

// IngestionAnalysis is persisted in knowledge.metadata.ingestion_analysis.
type IngestionAnalysis struct {
	AppliedMode               string                          `json:"applied_mode"`
	FallbackReasonCodes       []string                        `json:"fallback_reason_codes"`
	DocumentKind              string                          `json:"document_kind"`
	Confidence                float64                         `json:"confidence"`
	RecommendedContentMode    string                          `json:"recommended_content_mode"`
	ReasonCodes               []string                        `json:"reason_codes"`
	Summary                   string                          `json:"summary"`
	RecommendedChunking       IngestionChunkingRecommendation `json:"recommended_chunking"`
	AppliedChunking           IngestionChunkingRecommendation `json:"applied_chunking"`
	ModelID                   string                          `json:"model_id"`
	Candidates                []IngestionChunkingCandidate    `json:"candidates"`
	SelectedCandidateID       string                          `json:"selected_candidate_id"`
	SelectionReasonCodes      []string                        `json:"selection_reason_codes"`
	AgentRun                  IngestionAgentRun               `json:"agent_run"`
	PackingPolicy             SemanticPackingPolicy           `json:"packing_policy"`
	SemanticDiagnostics       IngestionSemanticDiagnostics    `json:"semantic_diagnostics"`
	CandidateGeneratorVersion string                          `json:"candidate_generator_version"`
	ShadowComparison          *IngestionShadowComparison      `json:"shadow_comparison,omitempty"`
}
