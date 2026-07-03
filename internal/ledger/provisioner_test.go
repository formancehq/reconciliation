package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"go.uber.org/mock/gomock"
)

const testLedger = "reconciliation"

func TestProvisioner_Provision(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockprovisionAPI(ctrl)

	// Ledger created once, with AUDIT enforcement (explicit — zero value is STRICT).
	m.EXPECT().
		CreateLedger(gomock.Any(), testLedger, gomock.Any(), gomock.Any(), commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT).
		Return(nil)
	// Both fixed prepared queries registered.
	m.EXPECT().
		CreatePreparedQuery(gomock.Any(), testLedger, gomock.Any()).
		Return(nil).
		Times(2)

	p := NewProvisioner(m, testLedger, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	if err := p.Provision(context.Background()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
}

func TestProvisioner_CreateLedgerErrorShortCircuits(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockprovisionAPI(ctrl)

	m.EXPECT().
		CreateLedger(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("boom"))
	// CreatePreparedQuery must not be called when CreateLedger fails: no EXPECT set,
	// so gomock fails the test if it is called.

	p := NewProvisioner(m, testLedger, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	if err := p.Provision(context.Background()); err == nil {
		t.Fatal("expected an error when CreateLedger fails")
	}
}
