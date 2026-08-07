package chunker

import "fmt"

type semanticTableValidation struct {
	headerSeen bool
	rowSeen    bool
}

func validateSemanticRelationships(blocks []SemanticBlock) error {
	seen := make(map[string]SemanticBlock, len(blocks))
	tables := make(map[string]semanticTableValidation)
	records := make(map[string]struct{})
	for index, block := range blocks {
		if err := validateSemanticBlockIdentity(index, block, seen); err != nil {
			return err
		}
		if err := validateSemanticBlockType(index, block); err != nil {
			return err
		}
		if err := validateSemanticTable(index, block, tables); err != nil {
			return err
		}
		if block.Kind == SemanticKindRecord {
			if _, duplicate := records[block.RecordID]; duplicate {
				return fmt.Errorf("semantic block %d duplicates record id %q", index, block.RecordID)
			}
			records[block.RecordID] = struct{}{}
		}
		seen[block.ID] = block
	}
	for tableID, state := range tables {
		if !state.headerSeen {
			return fmt.Errorf("semantic table %q has no header", tableID)
		}
	}
	return nil
}

func validateSemanticBlockIdentity(index int, block SemanticBlock, seen map[string]SemanticBlock) error {
	if block.ID == "" {
		return fmt.Errorf("semantic block %d has empty id", index)
	}
	if _, duplicate := seen[block.ID]; duplicate {
		return fmt.Errorf("semantic block %d duplicates id %q", index, block.ID)
	}
	if block.ParentID == "" {
		return nil
	}
	parent, ok := seen[block.ParentID]
	if !ok {
		return fmt.Errorf("semantic block %d has missing or forward parent %q", index, block.ParentID)
	}
	if parent.Kind != SemanticKindHeading {
		return fmt.Errorf("semantic block %d parent %q is not a heading", index, block.ParentID)
	}
	return nil
}

func validateSemanticBlockType(index int, block SemanticBlock) error {
	if !semanticKindAllowed(block.Kind) {
		return fmt.Errorf("semantic block %d has invalid kind %q", index, block.Kind)
	}
	if block.Kind == SemanticKindHeading {
		if block.SectionDepth < 1 || block.SectionDepth > 6 {
			return fmt.Errorf("semantic heading %d has invalid depth %d", index, block.SectionDepth)
		}
	} else if block.SectionDepth != 0 {
		return fmt.Errorf("semantic block %d has unexpected section depth", index)
	}
	if !semanticContextKindsMatch(block.Kind, block.ContextKinds) {
		return fmt.Errorf("semantic block %d has invalid context kinds", index)
	}
	if err := validateSemanticRelationFields(index, block); err != nil {
		return err
	}
	return nil
}

func validateSemanticRelationFields(index int, block SemanticBlock) error {
	isTable := block.Kind == SemanticKindTableHeader || block.Kind == SemanticKindTableRow
	if isTable != (block.TableID != "") {
		return fmt.Errorf("semantic block %d has inconsistent table id", index)
	}
	isRecord := block.Kind == SemanticKindRecord
	if isRecord != (block.RecordID != "") {
		return fmt.Errorf("semantic block %d has inconsistent record id", index)
	}
	return nil
}

func validateSemanticTable(
	index int,
	block SemanticBlock,
	tables map[string]semanticTableValidation,
) error {
	if block.TableID == "" {
		return nil
	}
	state := tables[block.TableID]
	if block.Kind == SemanticKindTableHeader {
		if state.headerSeen || state.rowSeen {
			return fmt.Errorf("semantic table %q has invalid header at block %d", block.TableID, index)
		}
		state.headerSeen = true
	} else {
		state.rowSeen = true
	}
	tables[block.TableID] = state
	return nil
}

func semanticContextKindsMatch(kind string, values []string) bool {
	allowed, required := semanticContextContract(kind)
	if len(allowed) == 0 {
		return len(values) == 0
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := allowed[value]; !ok {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	for _, value := range required {
		if _, ok := seen[value]; !ok {
			return false
		}
	}
	return true
}

func semanticContextContract(kind string) (map[string]struct{}, []string) {
	switch kind {
	case SemanticKindFAQ:
		return map[string]struct{}{"question": {}, "answer": {}}, []string{"question", "answer"}
	case SemanticKindImage:
		return map[string]struct{}{"image": {}, "caption": {}}, []string{"image"}
	case SemanticKindRecord:
		return map[string]struct{}{"record": {}}, []string{"record"}
	default:
		return nil, nil
	}
}

func canonicalSemanticContextKinds(kind string) []string {
	switch kind {
	case SemanticKindFAQ:
		return []string{"question", "answer"}
	case SemanticKindImage:
		return []string{"image"}
	case SemanticKindRecord:
		return []string{"record"}
	default:
		return nil
	}
}
