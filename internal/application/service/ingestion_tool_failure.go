package service

import (
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	ingestionFailureTool               = "INGESTION_TOOL_FAILED"
	ingestionFailureArgumentsInvalid   = "INGESTION_TOOL_ARGUMENTS_INVALID"
	ingestionFailureCandidatePreview   = "INGESTION_CANDIDATE_PREVIEW_FAILED"
	ingestionFailureCandidateLimit     = "INGESTION_CANDIDATE_LIMIT_REACHED"
	ingestionFailureDecisionInvalid    = "INGESTION_DECISION_INVALID"
	ingestionFailureStrategyInvalid    = ingestionFailureArgumentsInvalid
	ingestionFailureChunkSizeInvalid   = ingestionFailureArgumentsInvalid
	ingestionFailureOverlapInvalid     = ingestionFailureArgumentsInvalid
	ingestionFailureParentSizeInvalid  = ingestionFailureArgumentsInvalid
	ingestionFailureChildSizeInvalid   = ingestionFailureArgumentsInvalid
	ingestionFailureParentChildInvalid = ingestionFailureArgumentsInvalid
	ingestionFailureSeparatorsInvalid  = ingestionFailureArgumentsInvalid
	ingestionFailureChunkPosition      = ingestionFailureCandidatePreview
	ingestionFailureChunkOrder         = ingestionFailureCandidatePreview
	ingestionFailureParentChildMapping = ingestionFailureCandidatePreview
)

type ingestionToolError struct {
	code       string
	field      string
	constraint string
	message    string
	cause      error
}

func (e *ingestionToolError) Error() string {
	if e.cause == nil {
		return e.message
	}
	return fmt.Sprintf("%s: %v", e.message, e.cause)
}

func (e *ingestionToolError) Unwrap() error {
	return e.cause
}

func newIngestionToolError(code, field, constraint, message string) error {
	return &ingestionToolError{
		code: code, field: field, constraint: constraint, message: message,
	}
}

func wrapIngestionToolError(err error, code, field, constraint, message string) error {
	var classified *ingestionToolError
	if errors.As(err, &classified) {
		return err
	}
	return &ingestionToolError{
		code: code, field: field, constraint: constraint, message: message, cause: err,
	}
}

func safeIngestionToolFailure(err error) *types.ToolFailure {
	var classified *ingestionToolError
	if !errors.As(err, &classified) {
		return &types.ToolFailure{Code: ingestionFailureTool}
	}
	return &types.ToolFailure{
		Code: classified.code, Field: classified.field, Constraint: classified.constraint,
	}
}
