package rerror

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
	return &RetryableError{err}
}

type NonRetryableError struct {
	error
}

func (e *NonRetryableError) Retryable() bool {
	return false
}

func AsNonRetryableError(err error) ReconcileError {
	return &NonRetryableError{err}
}
