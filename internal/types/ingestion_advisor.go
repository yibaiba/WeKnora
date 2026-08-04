package types

import "time"

const (
	IngestionAdvisorModeSmart = "smart"
	IngestionAdvisorModeOff   = "off"
	IngestionPromptVersionV1  = "v1"
	IngestionPromptVersionV2  = "v2"
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

// IngestionAdvisorConfig opts a file upload or reparse into document analysis.
// A nil config preserves the historical processing path.
type IngestionAdvisorConfig struct {
	Mode             string `json:"mode"`
	PromptVersion    string `json:"prompt_version,omitempty"`
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
	PromptVersion       string
	AllowWebAccess      bool
	AllowReadOnlyMCP    bool
	ChunkingConstraints IngestionChunkingConstraints
	ProgressFn          func(IngestionAgentStep)
	AnalysisProgressFn  func(IngestionDocumentAnalysisProgress)
	Timeout             time.Duration
}

type IngestionDocumentAnalysisProgress struct {
	Phase             string
	UnitCount         int
	Completed         int
	Level             int
	DurationMS        int64
	CoveredCharacters int
	Failed            bool
}

// IngestionChunkingConstraints are knowledge-base-owned splitter inputs. The
// advisor may observe them when previewing but never overwrite them.
type IngestionChunkingConstraints struct {
	TokenLimit int
	Languages  []string
}

type IngestionAgentWarning struct {
	Code    string `json:"code"`
	Tool    string `json:"tool,omitempty"`
	Message string `json:"message"`
}

type IngestionAgentStep struct {
	Round       int     `json:"round"`
	ToolName    string  `json:"tool_name"`
	Status      string  `json:"status"`
	DurationMS  int64   `json:"duration_ms,omitempty"`
	CandidateID string  `json:"candidate_id,omitempty"`
	Score       float64 `json:"score,omitempty"`
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
	StructureIntegrity float64 `json:"structure_integrity"`
	ChunkSizeBalance   float64 `json:"chunk_size_balance"`
	BoundaryQuality    float64 `json:"boundary_quality"`
	OverlapEfficiency  float64 `json:"overlap_efficiency"`
	ParentChild        float64 `json:"parent_child"`
	Total              float64 `json:"total"`
}

type IngestionTierRejection struct {
	Tier   string `json:"tier"`
	Reason string `json:"reason"`
}

type IngestionChunkerDiagnostics struct {
	SelectedTier string                   `json:"selected_tier"`
	TierChain    []string                 `json:"tier_chain"`
	Rejected     []IngestionTierRejection `json:"rejected"`
}

type IngestionChunkingCandidate struct {
	ID               string                          `json:"id"`
	Config           IngestionChunkingRecommendation `json:"config"`
	ChunkCount       int                             `json:"chunk_count"`
	ParentChunkCount int                             `json:"parent_chunk_count"`
	Lengths          IngestionLengthDistribution     `json:"lengths"`
	Structure        IngestionStructureMetrics       `json:"structure"`
	Diagnostics      IngestionChunkerDiagnostics     `json:"diagnostics"`
	Score            IngestionCandidateScore         `json:"score"`
	HardValid        bool                            `json:"hard_valid"`
	Violations       []string                        `json:"violations"`
}

// IngestionAnalysis is persisted in knowledge.metadata.ingestion_analysis.
type IngestionAnalysis struct {
	DocumentKind           string                          `json:"document_kind"`
	Confidence             float64                         `json:"confidence"`
	RecommendedContentMode string                          `json:"recommended_content_mode"`
	ReasonCodes            []string                        `json:"reason_codes"`
	Summary                string                          `json:"summary"`
	RecommendedChunking    IngestionChunkingRecommendation `json:"recommended_chunking"`
	AppliedChunking        IngestionChunkingRecommendation `json:"applied_chunking"`
	ModelID                string                          `json:"model_id"`
	PromptVersion          string                          `json:"prompt_version"`
	Candidates             []IngestionChunkingCandidate    `json:"candidates"`
	SelectedCandidateID    string                          `json:"selected_candidate_id"`
	SelectionReasonCodes   []string                        `json:"selection_reason_codes"`
	AgentRun               IngestionAgentRun               `json:"agent_run"`
}

// DocumentStructureStats describes the complete extracted document, even when
// only sampled windows fit in the model prompt.
type DocumentStructureStats struct {
	CharacterCount        int                `json:"character_count"`
	LineCount             int                `json:"line_count"`
	NonEmptyLineCount     int                `json:"non_empty_line_count"`
	HeadingLevelCounts    HeadingLevelCounts `json:"heading_level_counts"`
	ParagraphCount        int                `json:"paragraph_count"`
	AverageParagraphChars int                `json:"average_paragraph_chars"`
	MaxParagraphChars     int                `json:"max_paragraph_chars"`
	ListLineCount         int                `json:"list_line_count"`
	ListDensity           float64            `json:"list_density"`
	TableLineCount        int                `json:"table_line_count"`
	TableDensity          float64            `json:"table_density"`
	QuestionAnswerPairs   int                `json:"question_answer_pairs"`
	Language              LanguageStats      `json:"language"`
}

type HeadingLevelCounts struct {
	H1 int `json:"h1"`
	H2 int `json:"h2"`
	H3 int `json:"h3"`
	H4 int `json:"h4"`
	H5 int `json:"h5"`
	H6 int `json:"h6"`
}

type LanguageStats struct {
	CJKCharacters   int     `json:"cjk_characters"`
	LatinCharacters int     `json:"latin_characters"`
	DigitCharacters int     `json:"digit_characters"`
	CJKRatio        float64 `json:"cjk_ratio"`
	LatinRatio      float64 `json:"latin_ratio"`
}

type DocumentContentSample struct {
	Head      string `json:"head"`
	Middle    string `json:"middle,omitempty"`
	Tail      string `json:"tail,omitempty"`
	Truncated bool   `json:"truncated"`
}

type IngestionDocumentProfile struct {
	Statistics DocumentStructureStats `json:"statistics"`
	Sample     DocumentContentSample  `json:"sample"`
}
