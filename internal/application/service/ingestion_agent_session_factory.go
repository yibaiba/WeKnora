package service

import (
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

func newIngestionAgentSession(
	content string,
	constraints types.IngestionChunkingConstraints,
) *ingestionAgentSession {
	return newIngestionAgentSessionWithFallback(
		content, constraints, types.IngestionChunkingRecommendation{},
	)
}

func newIngestionAgentSessionWithFallback(
	content string,
	constraints types.IngestionChunkingConstraints,
	fallback types.IngestionChunkingRecommendation,
) *ingestionAgentSession {
	document, err := chunker.AnalyzeSemanticDocument(content, chunker.SemanticAnalysisOptions{})
	return newIngestionAgentSessionWithDocument(content, constraints, ingestionSessionDocument{
		document: document, err: err, fallback: fallback,
	})
}

func newIngestionAgentSessionFromRequest(
	request types.IngestionAdvisorRequest,
) *ingestionAgentSession {
	if request.SemanticDocument == nil {
		return newIngestionAgentSessionWithFallback(
			request.Content, request.ChunkingConstraints, request.FallbackChunking,
		)
	}
	document := chunker.CloneSemanticDocument(*request.SemanticDocument)
	return newIngestionAgentSessionWithDocument(
		request.Content,
		request.ChunkingConstraints,
		ingestionSessionDocument{
			document: document, err: chunker.ValidateSemanticDocument(document),
			fallback: request.FallbackChunking,
		},
	)
}

type ingestionSessionDocument struct {
	document chunker.SemanticDocument
	err      error
	fallback types.IngestionChunkingRecommendation
}

func newIngestionAgentSessionWithDocument(
	content string,
	constraints types.IngestionChunkingConstraints,
	analysis ingestionSessionDocument,
) *ingestionAgentSession {
	return &ingestionAgentSession{
		content:    content,
		statistics: BuildIngestionDocumentStatistics(content),
		constraints: types.IngestionChunkingConstraints{
			TokenLimit: constraints.TokenLimit,
			Languages:  append([]string(nil), constraints.Languages...),
		},
		document:       analysis.document,
		documentErr:    analysis.err,
		fallback:       cloneChunkingRecommendation(analysis.fallback),
		candidates:     make(map[string]types.IngestionChunkingCandidate),
		inFlight:       make(map[string]*ingestionCandidateFlight),
		buildCandidate: buildIngestionCandidate,
	}
}
