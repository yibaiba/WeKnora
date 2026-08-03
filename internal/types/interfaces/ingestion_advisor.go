package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// IngestionAdvisor analyzes extracted document text without owning the file,
// knowledge record, model selection, or chunking lifecycle.
type IngestionAdvisor interface {
	Analyze(ctx context.Context, request types.IngestionAdvisorRequest) (*types.IngestionAnalysis, error)
}
