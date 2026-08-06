package chunker

import "strings"

type semanticPackGroup struct {
	units        []semanticPackingUnit
	start        int
	end          int
	context      string
	pendingStart int
}

func (packer semanticPacker) pack(units []semanticPackingUnit) []Chunk {
	chunks := make([]Chunk, 0, len(units))
	group := semanticPackGroup{pendingStart: -1}
	for _, unit := range units {
		if strings.TrimSpace(string(packer.runes[unit.Start:unit.End])) == "" {
			group = packer.addWhitespace(group, unit, &chunks)
			continue
		}
		if len(group.units) == 0 {
			group = startSemanticGroup(group.pendingStart, unit)
			continue
		}
		if packer.canMerge(group, unit) {
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
	return chunks
}

func (packer semanticPacker) addWhitespace(
	group semanticPackGroup,
	unit semanticPackingUnit,
	chunks *[]Chunk,
) semanticPackGroup {
	if len(group.units) == 0 {
		if group.pendingStart < 0 {
			group.pendingStart = unit.Start
		}
		return group
	}
	if packer.fits(group.start, unit.End, group.context) {
		group.units = append(group.units, unit)
		group.end = unit.End
		return group
	}
	*chunks = appendSemanticGroup(*chunks, group, packer.runes)
	return semanticPackGroup{pendingStart: unit.Start}
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

func (packer semanticPacker) canMerge(group semanticPackGroup, next semanticPackingUnit) bool {
	if group.end != next.Start || !packer.fits(group.start, next.End, group.context) {
		return false
	}
	anchor := lastSemanticMeaningfulUnit(group.units, packer.runes)
	if next.Kind == SemanticKindHeading {
		return false
	}
	if anchor.Kind == SemanticKindHeading {
		return next.ParentID == anchor.ID
	}
	if semanticStandaloneKind(anchor.Kind) || semanticStandaloneKind(next.Kind) {
		return false
	}
	if anchor.TableID != "" || next.TableID != "" {
		return anchor.TableID != "" && anchor.TableID == next.TableID
	}
	if anchor.RecordID != "" || next.RecordID != "" {
		return anchor.RecordID != "" && anchor.RecordID == next.RecordID
	}
	return anchor.Kind == next.Kind && anchor.ParentID == next.ParentID
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
		Start: group.start, End: group.end,
	})
}
