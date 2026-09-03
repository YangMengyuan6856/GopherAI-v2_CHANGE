package contract

import (
	"context"
	"errors"
	"fmt"
)

type ErrorCategory string

const (
	ErrorValidation            ErrorCategory = "validation"
	ErrorAuth                  ErrorCategory = "auth"
	ErrorNotFound              ErrorCategory = "not_found"
	ErrorConflict              ErrorCategory = "conflict"
	ErrorDependencyTimeout     ErrorCategory = "dependency_timeout"
	ErrorDependencyUnavailable ErrorCategory = "dependency_unavailable"
	ErrorBudgetExceeded        ErrorCategory = "budget_exceeded"
	ErrorEvidenceInsufficient  ErrorCategory = "evidence_insufficient"
	ErrorModel                 ErrorCategory = "model_error"
	ErrorInternal              ErrorCategory = "internal"
)

type DomainError struct {
	Code      string         `json:"code"`
	Category  ErrorCategory  `json:"category"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	TraceID   string         `json:"trace_id,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	Cause     error          `json:"-"`
}

func (domainError *DomainError) Error() string {
	if domainError == nil {
		return ""
	}
	if domainError.Cause == nil {
		return domainError.Message
	}
	return fmt.Sprintf("%s: %v", domainError.Message, domainError.Cause)
}

func (domainError *DomainError) Unwrap() error {
	if domainError == nil {
		return nil
	}
	return domainError.Cause
}

func NewDomainError(code string, category ErrorCategory, message string, retryable bool, cause error) *DomainError {
	return &DomainError{Code: code, Category: category, Message: message, Retryable: retryable, Cause: cause}
}

func WithTrace(err error, traceID string) *DomainError {
	if err == nil {
		return nil
	}
	var domainError *DomainError
	if errors.As(err, &domainError) {
		copy := *domainError
		copy.TraceID = traceID
		return &copy
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &DomainError{Code: "REQUEST_TIMEOUT", Category: ErrorDependencyTimeout, Message: "请求处理超时，请稍后重试", Retryable: true, TraceID: traceID, Cause: err}
	}
	if errors.Is(err, context.Canceled) {
		return &DomainError{Code: "REQUEST_CANCELED", Category: ErrorDependencyUnavailable, Message: "请求已取消", Retryable: true, TraceID: traceID, Cause: err}
	}
	return &DomainError{
		Code:      "INTERNAL_ERROR",
		Category:  ErrorInternal,
		Message:   "服务暂时不可用",
		Retryable: true,
		TraceID:   traceID,
		Cause:     err,
	}
}
