package types

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
