package ledger

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"go.uber.org/mock/gomock"
)

const testLedger = "reconciliation"

func TestProvisioner_Provision(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockprovisionAPI(ctrl)

	// Assert the ledger is created with AUDIT enforcement AND the real chart:
	// a non-empty metadata schema and the alert-state family marked EPHEMERAL.
	m.EXPECT().
		CreateLedger(gomock.Any(), testLedger, gomock.Any(), gomock.Any(), commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT).
		DoAndReturn(func(_ context.Context, _ string, md []*commonpb.SetMetadataFieldTypeCommand, ats map[string]*commonpb.AccountType, _ commonpb.ChartEnforcementMode) error {
			if len(md) == 0 {
				t.Error("CreateLedger got an empty metadata schema")
			}

			st, ok := ats[schema.AccountTypeAlertState]
			if !ok {
				t.Fatalf("CreateLedger missing account type %q", schema.AccountTypeAlertState)
			}

			if st.GetPersistence() != commonpb.AccountTypePersistence_ACCOUNT_TYPE_EPHEMERAL {
				t.Errorf("alert-state persistence = %v, want EPHEMERAL", st.GetPersistence())
			}

			return nil
		})

	// The queryable metadata indexes are created (id at minimum, for id→address
	// resolution).
	m.EXPECT().
		CreateIndex(gomock.Any(), testLedger, gomock.Any()).
		Return(nil).
		Times(len(schema.MetadataIndexes()))

	// Capture the prepared queries actually registered.
	var registered []string

	m.EXPECT().
		CreatePreparedQuery(gomock.Any(), testLedger, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, q *commonpb.PreparedQuery) error {
			registered = append(registered, q.GetName())

			return nil
		}).
		Times(2)

	// Capture the numscripts actually registered (assert non-empty content +
	// pinned version).
	var scripts []string

	m.EXPECT().
		SaveNumscript(gomock.Any(), testLedger, gomock.Any(), gomock.Any(), schema.NumscriptVersion).
		DoAndReturn(func(_ context.Context, _, name, content, _ string) error {
			if content == "" {
				t.Errorf("numscript %q has empty content", name)
			}

			scripts = append(scripts, name)

			return nil
		}).
		Times(len(schema.Numscripts()))

	p := NewProvisioner(m, testLedger, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	if err := p.Provision(context.Background()); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	for _, want := range []string{schema.PQOpenCount, schema.PQRulesEnabled} {
		if !slices.Contains(registered, want) {
			t.Errorf("prepared query %q not registered (got %v)", want, registered)
		}
	}

	for _, want := range []string{schema.NumscriptAlertOpen, schema.NumscriptAlertBump, schema.NumscriptAlertReopen, schema.NumscriptAlertMove} {
		if !slices.Contains(scripts, want) {
			t.Errorf("numscript %q not registered (got %v)", want, scripts)
		}
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
