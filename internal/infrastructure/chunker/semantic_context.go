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
	Content       string
	Document      SemanticDocument
	Block         SemanticBlock
	Continuation  bool
	TokenLimit    int
	TokenCounter  TokenCounter
	PackingPolicy types.SemanticPackingPolicy
}

type SemanticContextResult struct {
	Header      string
	ReasonCodes []string
}

func BuildSemanticContext(request SemanticContextRequest) (SemanticContextResult, error) {
	context, err := newSemanticContextIndex(request.Content, request.Document, request.PackingPolicy).contextFor(
		request.Block, request.Continuation, request.TokenLimit, request.TokenCounter,
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

func (index semanticContextIndex) contextFor(
	block SemanticBlock,
	continuation bool,
	tokenLimit int,
	counter TokenCounter,
) (semanticContext, error) {
	items := index.contextItems(block, continuation)
	if tokenLimit <= 0 {
		return semanticContext{header: joinSemanticContextItems(items)}, nil
	}
	percent, cap := semanticContextTokenPercent, semanticContextTokenCap
	if index.policy.ContextTokenPercent > 0 {
		percent = index.policy.ContextTokenPercent
	}
	if index.policy.ContextTokenLimit > 0 {
		cap = index.policy.ContextTokenLimit
	}
	budget := min(cap, tokenLimit*percent/100)
	return fitSemanticContext(items, budget, tokenCounterOrConservative(counter))
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
	budget int,
	counter TokenCounter,
) (semanticContext, error) {
	selected := make([]semanticContextItem, 0, len(items))
	reasons := make([]string, 0, 2)
	for _, item := range items {
		candidate := append(append([]semanticContextItem(nil), selected...), item)
		count, err := counter.Count(joinSemanticContextItems(candidate))
		if err != nil {
			return semanticContext{}, err
		}
		if count.Count <= budget {
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
