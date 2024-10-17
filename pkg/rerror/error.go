package rerror

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type ReconcileError interface {
	error
	Retryable() bool
}

type RetryableError struct {
	error
}

func (e *RetryableError) Retryable() bool {
	return true
}

func AsRetryableError(err error) ReconcileError {
	if err == nil {
		return nil
	}

	return &RetryableError{err}
}

type NonRetryableError struct {
	error
}

func (e *NonRetryableError) Retryable() bool {
	return false
}

func AsNonRetryableError(err error) ReconcileError {
	if err == nil {
		return nil
	}

	return &NonRetryableError{err}
}

func EvaluateReconcileError(result ctrl.Result, err ReconcileError) (ctrl.Result, error) {
	if err == nil {
		return result, nil
	}
	if err.Retryable() {
		return result, err
	}

	return result, reconcile.TerminalError(err)
}
