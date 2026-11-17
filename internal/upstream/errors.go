// Package upstream limits dependency error text exposed to callers and storage.
package upstream

// Wrap renders only a fixed operation message. The cause remains available via
// errors.Is/As; upstream bodies, error codes and transport URLs are untrusted.
// Callers must supply an operation constant, never request or response data.
func Wrap(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	return &operationError{operation: operation, cause: cause}
}

type operationError struct {
	operation string
	cause     error
}

func (e *operationError) Error() string { return e.operation + ": upstream request failed" }
func (e *operationError) Unwrap() error { return e.cause }
