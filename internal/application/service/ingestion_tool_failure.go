package service

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

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

func sanitizeIngestionToolFailure(failure *types.ToolFailure) *types.ToolFailure {
	if failure == nil {
		return nil
	}
	return &types.ToolFailure{
		Code:       safeIngestionFailureCode(failure.Code),
		Field:      safeIngestionFailureField(failure.Field),
		Constraint: safeIngestionFailureConstraint(failure.Constraint),
	}
}

func safeIngestionFailureCode(code string) string {
	if code == "TOOL_ARGUMENTS_INVALID" {
		return ingestionFailureArgumentsInvalid
	}
	switch code {
	case ingestionFailureTool, ingestionFailureArgumentsInvalid, ingestionFailureCandidatePreview,
		ingestionFailureCandidateLimit, ingestionFailureDecisionInvalid:
		return code
	default:
		return ingestionFailureTool
	}
}

func safeIngestionFailureField(field string) string {
	switch field {
	case "strategy", "chunk_size", "chunk_overlap", "enable_parent_child",
		"parent_chunk_size", "child_chunk_size", "separators", "candidate_id",
		"document_kind", "confidence", "recommended_content_mode", "reason_codes", "summary":
		return field
	}
	for _, arrayField := range []string{"separators", "reason_codes"} {
		if isSafeArrayItemField(field, arrayField) {
			return field
		}
	}
	return ""
}

func isSafeArrayItemField(field, root string) bool {
	prefix := root + "["
	if !strings.HasPrefix(field, prefix) || !strings.HasSuffix(field, "]") {
		return false
	}
	index := strings.TrimSuffix(strings.TrimPrefix(field, prefix), "]")
	_, err := strconv.ParseUint(index, 10, 32)
	return err == nil
}

func safeIngestionFailureConstraint(constraint string) string {
	switch constraint {
	case "valid_json", "json_schema", "supported_strategy", "effective_chunk_size_range",
		"at_most_half_chunk_size", "parent_chunk_size_range", "child_chunk_size_range",
		"not_greater_than_parent_chunk_size", "non_empty_supported_separators",
		"source_rune_positions", "strictly_increasing_end_positions", "valid_parent_child_mapping",
		"serializable_candidate", "candidate_limit", "previewed_hard_valid_candidate", "persisted_candidate",
		"all_candidates_structurally_invalid":
		return constraint
	default:
		return ""
	}
}
