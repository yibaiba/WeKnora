package types

const (
	IngestionAdvisorModeSmart = "smart"
	IngestionAdvisorModeOff   = "off"
	IngestionPromptVersionV1  = "v1"
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
	Mode          string `json:"mode"`
	PromptVersion string `json:"prompt_version,omitempty"`
}

// IngestionAdvisorRequest is immutable input to the advisor boundary.
type IngestionAdvisorRequest struct {
	Content       string
	ModelID       string
	PromptVersion string
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
