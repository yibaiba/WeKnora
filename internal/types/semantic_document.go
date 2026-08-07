package types

// SemanticBlock describes a source-positioned unit in the final Markdown.
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

// SemanticDiagnostics intentionally contains only aggregate reason codes.
type SemanticDiagnostics struct {
	HintsProvided    int            `json:"hints_provided"`
	HintsAccepted    int            `json:"hints_accepted"`
	HintsRejected    int            `json:"hints_rejected"`
	ReasonCodes      []string       `json:"reason_codes"`
	ReasonCodeCounts map[string]int `json:"reason_code_counts,omitempty"`
}

type SemanticDocument struct {
	ContentLength int
	Blocks        []SemanticBlock
	Diagnostics   SemanticDiagnostics
}
