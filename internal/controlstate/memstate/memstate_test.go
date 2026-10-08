package memstate_test

import (
	"testing"

	"github.com/formancehq/reconciliation/internal/controlstate/controlstatetest"
	"github.com/formancehq/reconciliation/internal/controlstate/memstate"
)

func TestContract(t *testing.T) {
	t.Parallel()
	controlstatetest.Run(t, func(*testing.T) controlstatetest.Store { return memstate.New() })
}
