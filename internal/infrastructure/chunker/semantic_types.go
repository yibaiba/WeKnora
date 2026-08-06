package chunker

import "fmt"

const (
	SemanticKindPreamble    = "preamble"
	SemanticKindHeading     = "heading"
	SemanticKindParagraph   = "paragraph"
	SemanticKindListItem    = "list_item"
	SemanticKindTableHeader = "table_header"
	SemanticKindTableRow    = "table_row"
	SemanticKindRecord      = "record"
	SemanticKindFAQ         = "faq"
	SemanticKindCodeBlock   = "code_block"
	SemanticKindImage       = "image_caption"
	SemanticKindPageRegion  = "page_region"
)

const (
	SemanticConfidenceHigh = "high"
	SemanticConfidenceSoft = "soft"
)

// SemanticBlock is a source-positioned structural unit. Blocks in a
// SemanticDocument are non-overlapping and continuously cover the source.
type SemanticBlock struct {
	ID           string
	Kind         string
	Start        int
	End          int
	ParentID     string
	SectionDepth int
	TableID      string
	RecordID     string
	Atomic       bool
	Confidence   string
	ContextKinds []string
}

type SemanticBlockHint struct {
	ID           string
	Kind         string
	Start        int
	End          int
	ParentID     string
	SectionDepth int
	TableID      string
	RecordID     string
	Atomic       bool
	Confidence   string
	ContextKinds []string
}

type SemanticAnalysisOptions struct {
	HintSource string
	Hints      []SemanticBlockHint
}

// SemanticDiagnostics is intentionally content-free.
type SemanticDiagnostics struct {
	HintsProvided int      `json:"hints_provided"`
	HintsAccepted int      `json:"hints_accepted"`
	HintsRejected int      `json:"hints_rejected"`
	ReasonCodes   []string `json:"reason_codes"`
}

type SemanticDocument struct {
	ContentLength int
	Blocks        []SemanticBlock
	Diagnostics   SemanticDiagnostics
}

func ValidateSemanticDocument(document SemanticDocument) error {
	if document.ContentLength == 0 && len(document.Blocks) == 0 {
		return nil
	}
	if len(document.Blocks) == 0 {
		return fmt.Errorf("semantic document has no blocks")
	}
	expected := 0
	for index, block := range document.Blocks {
		if block.Start != expected || block.End <= block.Start || block.End > document.ContentLength {
			return fmt.Errorf("semantic block %d has invalid coverage", index)
		}
		expected = block.End
	}
	if expected != document.ContentLength {
		return fmt.Errorf("semantic document coverage ends at %d, want %d", expected, document.ContentLength)
	}
	return nil
}

func semanticKindAllowed(kind string) bool {
	switch kind {
	case SemanticKindPreamble, SemanticKindHeading, SemanticKindParagraph,
		SemanticKindListItem, SemanticKindTableHeader, SemanticKindTableRow,
		SemanticKindRecord, SemanticKindFAQ, SemanticKindCodeBlock,
		SemanticKindImage, SemanticKindPageRegion:
		return true
	default:
		return false
	}
}
