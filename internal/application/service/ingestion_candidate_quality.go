package service

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
)

func (validator *ingestionStructureValidator) validateCodeContinuations() {
	for _, block := range validator.request.document.Blocks {
		if block.Kind != chunker.SemanticKindCodeBlock ||
			block.Confidence != chunker.SemanticConfidenceHigh {
			continue
		}
		context := firstIngestionContextLine(validator.index.blockText(block))
		for _, current := range validator.request.chunks {
			if current.Start <= block.Start || !rangesOverlap(chunkRange(current), blockRange(block)) {
				continue
			}
			if !strings.Contains(current.ContextHeader, context) {
				validator.violations.add(ingestionViolationCodeContextMissing)
			}
		}
	}
}

func countMixedSectionChunks(
	chunks []chunker.Chunk,
	index ingestionSemanticIndex,
) int {
	mixed := 0
	for _, current := range chunks {
		sections := make(map[string]struct{})
		for _, block := range index.byID {
			if !rangesOverlap(chunkRange(current), blockRange(block)) {
				continue
			}
			if rootID := index.rootSectionID(block); rootID != "" {
				sections[rootID] = struct{}{}
			}
		}
		if len(sections) > 1 {
			mixed++
		}
	}
	return mixed
}

func (index ingestionSemanticIndex) rootSectionID(block chunker.SemanticBlock) string {
	rootID := ""
	if block.Kind == chunker.SemanticKindHeading {
		rootID = block.ID
	}
	parentID := block.ParentID
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
		if parent.Kind == chunker.SemanticKindHeading {
			rootID = parent.ID
		}
		parentID = parent.ParentID
	}
	return rootID
}
