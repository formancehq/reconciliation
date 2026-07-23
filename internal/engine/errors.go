package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common"
)

// ErrCompile signals a rule expression failed to parse or type-check. Wrapped
// once by Compile with a human-readable message; callers can errors.Is on it
// to distinguish create-time validation failures from runtime errors.
var ErrCompile = errors.New("rule expression failed to compile")

// ErrEvaluate signals a runtime failure during evaluation (resolver error,
// CEL runtime error, budget exceeded). Triggers an engine.error meta-incident
// rather than a data incident — see spec §6.4 and Resolution kinds.
var ErrEvaluate = errors.New("rule evaluation failed")

// translateCompileError converts the cel-go Issues object into a single error
// that mentions every diagnostic, with line/column information. cel-go's raw
// formatting is engineer-friendly; we pass it through largely unchanged but
// wrap it with ErrCompile so callers know to surface this at HTTP 400.
func translateCompileError(issues *cel.Issues, source string) error {
	if issues == nil || issues.Err() == nil {
		return nil
	}
	src := common.NewTextSource(source)
	var msgs []string
	for _, e := range issues.Errors() {
		msgs = append(msgs, e.ToDisplayString(src))
	}
	return fmt.Errorf("%w: %s", ErrCompile, strings.Join(msgs, "; "))
}

// translateRuntimeError wraps a cel-go runtime error (or a resolver error)
// with ErrEvaluate so the service layer can route it into an engine.error
// meta-incident rather than treating it as a data incident.
func translateRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrEvaluate) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrEvaluate, err)
}
