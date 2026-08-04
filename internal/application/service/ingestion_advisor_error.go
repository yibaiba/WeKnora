package service

import (
	"errors"
	"fmt"
)

const (
	ingestionAdvisorErrorModelUnavailable = "INGESTION_MODEL_UNAVAILABLE"
	ingestionAdvisorErrorToolCalling      = "INGESTION_MODEL_TOOL_CALLING_UNSUPPORTED"
	ingestionAdvisorErrorCoreTool         = "INGESTION_CORE_TOOL_FAILED"
	ingestionAdvisorErrorCandidate        = "INGESTION_CANDIDATE_INVALID"
	ingestionAdvisorErrorMaxRounds        = "INGESTION_AGENT_MAX_ROUNDS"
	ingestionAdvisorErrorNotSubmitted     = "INGESTION_DECISION_NOT_SUBMITTED"
	ingestionAdvisorErrorExecution        = "INGESTION_AGENT_EXECUTION_FAILED"
)

type ingestionAdvisorRunError struct {
	code  string
	cause error
}

func (e *ingestionAdvisorRunError) Error() string { return e.cause.Error() }
func (e *ingestionAdvisorRunError) Unwrap() error { return e.cause }

func newIngestionAdvisorRunError(code, message string, args ...any) error {
	return &ingestionAdvisorRunError{code: code, cause: fmt.Errorf(message, args...)}
}

func wrapIngestionAdvisorRunError(code, message string, cause error) error {
	return &ingestionAdvisorRunError{code: code, cause: fmt.Errorf("%s: %w", message, cause)}
}

func ingestionAdvisorRunErrorCode(err error) string {
	var runErr *ingestionAdvisorRunError
	if errors.As(err, &runErr) && runErr.code != "" {
		return runErr.code
	}
	return ingestionAdvisorErrorExecution
}
