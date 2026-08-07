package chunker

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	semanticContextTokenPercent = 20
	semanticContextTokenCap     = 128

	SemanticReasonAncestorOmitted        = "context_ancestor_omitted"
	SemanticReasonRequiredContextExceeds = "required_context_budget_exceeded"
)

type semanticContextIndex struct {
	content      []rune
	byID         map[string]SemanticBlock
	tableHeaders map[string]SemanticBlock
	records      map[string]SemanticBlock
	policy       types.SemanticPackingPolicy
}

type semanticContextItem struct {
	text     string
	required bool
}

type semanticContext struct {
	header      string
	reasonCodes []string
}

type SemanticContextRequest struct {
	Content         string
	Document        SemanticDocument
	Block           SemanticBlock
	Continuation    bool
	TokenLimit      int
	TokenCounter    TokenCounter
	EmbeddingPrefix string
	PackingPolicy   types.SemanticPackingPolicy
}

type SemanticContextResult struct {
	Header      string
	ReasonCodes []string
}

func BuildSemanticContext(request SemanticContextRequest) (SemanticContextResult, error) {
	context, err := newSemanticContextIndex(request.Content, request.Document, request.PackingPolicy).contextFor(
		request.Block, semanticContextOptions{
			continuation: request.Continuation, tokenLimit: request.TokenLimit,
			counter: request.TokenCounter, embeddingPrefix: request.EmbeddingPrefix,
		},
	)
	if err != nil {
		return SemanticContextResult{}, err
	}
	return SemanticContextResult{
		Header: context.header, ReasonCodes: append([]string(nil), context.reasonCodes...),
	}, nil
}

func newSemanticContextIndex(
	content string,
	document SemanticDocument,
	policy types.SemanticPackingPolicy,
) semanticContextIndex {
	index := semanticContextIndex{
		content: []rune(content), byID: make(map[string]SemanticBlock, len(document.Blocks)),
		tableHeaders: make(map[string]SemanticBlock), records: make(map[string]SemanticBlock),
		policy: policy,
	}
	for _, block := range document.Blocks {
		index.byID[block.ID] = block
		if block.Kind == SemanticKindTableHeader && block.TableID != "" {
			index.tableHeaders[block.TableID] = block
		}
		if block.Kind == SemanticKindRecord && block.RecordID != "" {
			index.records[block.RecordID] = block
		}
	}
	return index
}

type semanticContextOptions struct {
	continuation    bool
	tokenLimit      int
	counter         TokenCounter
	embeddingPrefix string
}

type semanticContextBudget struct {
	headerTokenLimit int
	totalTokenLimit  int
	embeddingPrefix  string
}

func (index semanticContextIndex) contextFor(
	block SemanticBlock,
	options semanticContextOptions,
) (semanticContext, error) {
	items := index.contextItems(block, options.continuation)
	if options.tokenLimit <= 0 {
		return semanticContext{header: joinSemanticContextItems(items)}, nil
	}
	percent, cap := semanticContextTokenPercent, semanticContextTokenCap
	if index.policy.ContextTokenPercent > 0 {
		percent = index.policy.ContextTokenPercent
	}
	if index.policy.ContextTokenLimit > 0 {
		cap = index.policy.ContextTokenLimit
	}
	budget := semanticContextBudget{
		headerTokenLimit: min(cap, options.tokenLimit*percent/100),
		totalTokenLimit:  options.tokenLimit, embeddingPrefix: options.embeddingPrefix,
	}
	return fitSemanticContext(items, budget, tokenCounterOrConservative(options.counter))
}

func (index semanticContextIndex) contextItems(
	block SemanticBlock,
	continuation bool,
) []semanticContextItem {
	items := make([]semanticContextItem, 0, 6)
	if block.Kind == SemanticKindTableRow {
		items = appendContextItem(items, index.blockText(index.tableHeaders[block.TableID]), true)
	}
	if continuation && block.Kind == SemanticKindRecord {
		items = appendContextItem(items, firstSemanticSourceLine(index.blockText(index.records[block.RecordID])), true)
	}
	if continuation && block.Kind == SemanticKindCodeBlock {
		items = appendContextItem(items, firstSemanticSourceLine(index.blockText(block)), true)
	}
	for _, heading := range index.headingAncestorsNearestFirst(block.ParentID) {
		items = appendContextItem(items, heading, false)
	}
	return items
}

func fitSemanticContext(
	items []semanticContextItem,
	budget semanticContextBudget,
	counter TokenCounter,
) (semanticContext, error) {
	selected := make([]semanticContextItem, 0, len(items))
	reasons := make([]string, 0, 2)
	for _, item := range items {
		candidate := append(append([]semanticContextItem(nil), selected...), item)
		fits, err := semanticContextCandidateFits(candidate, budget, counter)
		if err != nil {
			return semanticContext{}, err
		}
		if fits {
			selected = candidate
			continue
		}
		if item.required {
			selected = candidate
			reasons = appendUniqueReason(reasons, SemanticReasonRequiredContextExceeds)
			continue
		}
		reasons = appendUniqueReason(reasons, SemanticReasonAncestorOmitted)
	}
	return semanticContext{header: joinSemanticContextItems(selected), reasonCodes: reasons}, nil
}

func semanticContextCandidateFits(
	items []semanticContextItem,
	budget semanticContextBudget,
	counter TokenCounter,
) (bool, error) {
	header := joinSemanticContextItems(items)
	count, err := counter.Count(header)
	if err != nil || count.Count > budget.headerTokenLimit {
		return false, err
	}
	if strings.TrimSpace(budget.embeddingPrefix) == "" {
		return true, nil
	}
	overhead, err := counter.Count(embeddingContentOverhead(budget.embeddingPrefix, header))
	if err != nil {
		return false, err
	}
	return overhead.Count < budget.totalTokenLimit, nil
}

func (index semanticContextIndex) headingAncestorsNearestFirst(parentID string) []string {
	result := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for parentID != "" {
		if _, duplicate := seen[parentID]; duplicate {
			break
		}
		seen[parentID] = struct{}{}
		parent, ok := index.byID[parentID]
		if !ok {
			break
		}
		trustedHeading := parent.Confidence == SemanticConfidenceHigh || index.policy.TrustSoftHeadings
		if parent.Kind == SemanticKindHeading && trustedHeading {
			result = appendUniqueContext(result, index.blockText(parent))
		}
		parentID = parent.ParentID
	}
	return result
}

func (index semanticContextIndex) blockText(block SemanticBlock) string {
	if block.End <= block.Start || block.Start < 0 || block.End > len(index.content) {
		return ""
	}
	return strings.TrimSpace(string(index.content[block.Start:block.End]))
}

func appendContextItem(items []semanticContextItem, value string, required bool) []semanticContextItem {
	value = strings.TrimSpace(value)
	if value == "" {
		return items
	}
	for _, existing := range items {
		if existing.text == value {
			return items
		}
	}
	return append(items, semanticContextItem{text: value, required: required})
}

func appendUniqueContext(parts []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return parts
	}
	for _, existing := range parts {
		if existing == value {
			return parts
		}
	}
	return append(parts, value)
}

func joinSemanticContextItems(items []semanticContextItem) string {
	parts := make([]string, len(items))
	for index, item := range items {
		parts[index] = item.text
	}
	return strings.Join(parts, "\n")
}

func appendUniqueReason(values []string, reason string) []string {
	for _, value := range values {
		if value == reason {
			return values
		}
	}
	return append(values, reason)
}

func firstSemanticSourceLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return strings.TrimSpace(value[:index])
	}
	return strings.TrimSpace(value)
}
