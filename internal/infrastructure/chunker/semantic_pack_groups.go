package chunker

import "strings"

type semanticPackGroup struct {
	units        []semanticPackingUnit
	start        int
	end          int
	context      string
	pendingStart int
}

func (packer semanticPacker) pack(units []semanticPackingUnit) ([]Chunk, error) {
	chunks := make([]Chunk, 0, len(units))
	group := semanticPackGroup{pendingStart: -1}
	for _, unit := range units {
		if strings.TrimSpace(string(packer.runes[unit.Start:unit.End])) == "" {
			var err error
			group, err = packer.addWhitespace(group, unit, &chunks)
			if err != nil {
				return nil, err
			}
			continue
		}
		if len(group.units) == 0 {
			group = startSemanticGroup(group.pendingStart, unit)
			continue
		}
		canMerge, err := packer.canMerge(group, unit)
		if err != nil {
			return nil, err
		}
		if canMerge {
			group.units = append(group.units, unit)
			group.end = unit.End
			continue
		}
		chunks = appendSemanticGroup(chunks, group, packer.runes)
		group = startSemanticGroup(group.pendingStart, unit)
	}
	if len(group.units) > 0 {
		chunks = appendSemanticGroup(chunks, group, packer.runes)
	} else if group.pendingStart >= 0 && group.pendingStart < len(packer.runes) {
		chunks = append(chunks, Chunk{Start: group.pendingStart, End: len(packer.runes), Content: string(packer.runes[group.pendingStart:])})
	}
	for index := range chunks {
		chunks[index].Seq = index
	}
	return chunks, nil
}

func (packer semanticPacker) addWhitespace(
	group semanticPackGroup,
	unit semanticPackingUnit,
	chunks *[]Chunk,
) (semanticPackGroup, error) {
	if len(group.units) == 0 {
		if group.pendingStart < 0 {
			group.pendingStart = unit.Start
		}
		return group, nil
	}
	fits, err := packer.fits(group.start, unit.End, group.context)
	if err != nil {
		return group, err
	}
	if fits {
		group.units = append(group.units, unit)
		group.end = unit.End
		return group, nil
	}
	*chunks = appendSemanticGroup(*chunks, group, packer.runes)
	return semanticPackGroup{pendingStart: unit.Start}, nil
}

func startSemanticGroup(pendingStart int, unit semanticPackingUnit) semanticPackGroup {
	start := unit.Start
	if pendingStart >= 0 {
		start = pendingStart
	}
	return semanticPackGroup{
		units: []semanticPackingUnit{unit}, start: start, end: unit.End,
		context: unit.ContextHeader, pendingStart: -1,
	}
}

func (packer semanticPacker) canMerge(group semanticPackGroup, next semanticPackingUnit) (bool, error) {
	if group.end != next.Start {
		return false, nil
	}
	fits, err := packer.fits(group.start, next.End, group.context)
	if err != nil || !fits {
		return false, err
	}
	anchor := lastSemanticMeaningfulUnit(group.units, packer.runes)
	if next.Kind == SemanticKindHeading {
		return false, nil
	}
	if anchor.Kind == SemanticKindHeading {
		return next.ParentID == anchor.ID, nil
	}
	if semanticStandaloneKind(anchor.Kind) || semanticStandaloneKind(next.Kind) {
		return false, nil
	}
	if anchor.TableID != "" || next.TableID != "" {
		return anchor.TableID != "" && anchor.TableID == next.TableID, nil
	}
	if anchor.RecordID != "" || next.RecordID != "" {
		return anchor.RecordID != "" && anchor.RecordID == next.RecordID, nil
	}
	return anchor.Kind == next.Kind && anchor.ParentID == next.ParentID, nil
}

func lastSemanticMeaningfulUnit(units []semanticPackingUnit, content []rune) semanticPackingUnit {
	for index := len(units) - 1; index >= 0; index-- {
		unit := units[index]
		if strings.TrimSpace(string(content[unit.Start:unit.End])) != "" {
			return unit
		}
	}
	return units[len(units)-1]
}

func semanticStandaloneKind(kind string) bool {
	return kind == SemanticKindFAQ || kind == SemanticKindCodeBlock || kind == SemanticKindImage || kind == SemanticKindPageRegion
}

func appendSemanticGroup(chunks []Chunk, group semanticPackGroup, content []rune) []Chunk {
	if group.end <= group.start {
		return chunks
	}
	return append(chunks, Chunk{
		Content: string(content[group.start:group.end]), ContextHeader: group.context,
		ContextReasonCodes: append([]string(nil), group.units[0].ContextReasonCodes...),
		Start:              group.start, End: group.end,
	})
}
