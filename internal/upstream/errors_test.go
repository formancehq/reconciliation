package upstream

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWrapWithholdsDetailsAndPreservesCause(t *testing.T) {
	cause := fmt.Errorf("secret upstream body and URL: %w", context.DeadlineExceeded)
	err := Wrap("pool balances", cause)
	require.EqualError(t, err, "pool balances: upstream request failed")
	require.True(t, errors.Is(err, context.DeadlineExceeded))
	require.Same(t, cause, errors.Unwrap(err))
	require.Nil(t, Wrap("pool balances", nil))
}
