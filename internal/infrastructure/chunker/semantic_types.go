package chunker

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

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

type SemanticBlock = types.SemanticBlock
type SemanticBlockHint = types.SemanticBlockHint
type SemanticDiagnostics = types.SemanticDiagnostics
type SemanticDocument = types.SemanticDocument

type SemanticAnalysisOptions struct {
	HintSource string
	Hints      []SemanticBlockHint
}

func ValidateSemanticDocument(document SemanticDocument) error {
	if err := validateSemanticCoverage(document); err != nil {
		return err
	}
	return validateSemanticRelationships(document.Blocks)
}

func validateSemanticCoverage(document SemanticDocument) error {
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

func CloneSemanticDocument(document SemanticDocument) SemanticDocument {
	cloned := document
	cloned.Blocks = append([]SemanticBlock(nil), document.Blocks...)
	for index := range cloned.Blocks {
		cloned.Blocks[index].ContextKinds = append(
			[]string(nil), document.Blocks[index].ContextKinds...,
		)
	}
	cloned.Diagnostics.ReasonCodes = append(
		[]string(nil), document.Diagnostics.ReasonCodes...,
	)
	return cloned
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
