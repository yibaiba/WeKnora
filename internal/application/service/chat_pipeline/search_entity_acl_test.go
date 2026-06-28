package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type entitySearchACLGuard struct {
	allowed map[string]struct{}
}

func (g entitySearchACLGuard) CanRead(
	context.Context,
	interfaces.SourceACLGuardRequest,
) (*types.SourceACLDecision, error) {
	return &types.SourceACLDecision{Allowed: true}, nil
}

func (g entitySearchACLGuard) RequireRead(
	context.Context,
	interfaces.SourceACLGuardRequest,
) error {
	return nil
}

func (g entitySearchACLGuard) FilterKnowledges(
	_ context.Context,
	_ string,
	knowledges []*types.Knowledge,
) ([]*types.Knowledge, error) {
	filtered := make([]*types.Knowledge, 0, len(knowledges))
	for _, knowledge := range knowledges {
		if knowledge == nil {
			continue
		}
		if _, ok := g.allowed[knowledge.ID]; ok {
			filtered = append(filtered, knowledge)
		}
	}
	return filtered, nil
}

func (g entitySearchACLGuard) FilterIndexCandidates(
	context.Context,
	string,
	[]*types.IndexWithScore,
) ([]*types.IndexWithScore, error) {
	return nil, nil
}

func TestPluginSearchEntityFiltersBySourceACL(t *testing.T) {
	plugin := &PluginSearchEntity{
		sourceACLGuard: entitySearchACLGuard{allowed: map[string]struct{}{
			"allowed": {},
		}},
	}
	ids, err := plugin.allowedEntityKnowledgeIDs(context.Background(), []*types.Knowledge{
		{ID: "allowed"},
		{ID: "denied"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := ids["allowed"]; !ok {
		t.Fatal("expected allowed knowledge id to remain")
	}
	if _, ok := ids["denied"]; ok {
		t.Fatal("expected denied knowledge id to be filtered")
	}
}

func TestPluginSearchEntitySkipsMissingKnowledgeAfterACL(t *testing.T) {
	knowledgeMap := map[string]*types.Knowledge{
		"allowed": {ID: "allowed", Title: "Allowed"},
	}
	allowedKnowledgeIDs := map[string]struct{}{
		"allowed": {},
		"missing": {},
	}
	chunks := []*types.Chunk{
		{ID: "chunk-1", KnowledgeID: "allowed"},
		{ID: "chunk-2", KnowledgeID: "missing"},
	}
	entityResults := buildEntitySearchResults(context.Background(), chunks, knowledgeMap, allowedKnowledgeIDs)
	if len(entityResults) != 1 {
		t.Fatalf("expected only chunks with loaded knowledge, got %d", len(entityResults))
	}
	if entityResults[0].KnowledgeID != "allowed" {
		t.Fatalf("unexpected knowledge id: %s", entityResults[0].KnowledgeID)
	}
}
