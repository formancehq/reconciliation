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

	// Bare ledger (nothing declared yet) → the reconcile applies the full chart.
	m.EXPECT().
		GetLedgerInfo(gomock.Any(), testLedger).
		Return(nil, nil)

	// Reconcile pass (F8): every account type is added so a stale ledger picks up
	// additive chart changes. Capture the names actually reconciled.
	var addedTypes []string

	m.EXPECT().
		AddAccountType(gomock.Any(), testLedger, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, at *commonpb.AccountType) error {
			addedTypes = append(addedTypes, at.GetName())

			return nil
		}).
		Times(len(schema.AccountTypes()))

	// Reconcile pass (F8): every typed metadata field is (re-)declared. Capture
	// the keys actually reconciled.
	var setFields []string

	m.EXPECT().
		SetMetadataFieldType(gomock.Any(), testLedger, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, cmd *commonpb.SetMetadataFieldTypeCommand) error {
			setFields = append(setFields, cmd.GetKey())

			return nil
		}).
		Times(len(schema.MetadataSchema()))

	// The queryable metadata indexes are created (id at minimum, for id→address
	// resolution).
	m.EXPECT().
		CreateIndex(gomock.Any(), testLedger, gomock.Any()).
		Return(nil).
		Times(len(schema.MetadataIndexes()) + len(schema.TransactionIndexes()))

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

	// Every account type and metadata field in the chart is reconciled (F8).
	for wantType := range schema.AccountTypes() {
		if !slices.Contains(addedTypes, wantType) {
			t.Errorf("account type %q not reconciled (got %v)", wantType, addedTypes)
		}
	}

	for _, cmd := range schema.MetadataSchema() {
		if !slices.Contains(setFields, cmd.GetKey()) {
			t.Errorf("metadata field %q not reconciled (got %v)", cmd.GetKey(), setFields)
		}
	}
}

// TestProvisioner_UpToDateLedgerSkipsReconcile is the churn guard: on a ledger
// that already carries the full chart, the reconcile must issue ZERO
// AddAccountType / SetMetadataFieldType calls — re-declaring an indexed metadata
// field would re-trigger a forward-index rewrite on every boot.
func TestProvisioner_UpToDateLedgerSkipsReconcile(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockprovisionAPI(ctrl)

	m.EXPECT().CreateLedger(gomock.Any(), testLedger, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	// The ledger already has every account type and every metadata field with the
	// declared type — a fully-provisioned ledger.
	fields := map[string]*commonpb.MetadataFieldSchema{}
	for _, cmd := range schema.MetadataSchema() {
		fields[cmd.GetKey()] = &commonpb.MetadataFieldSchema{Type: cmd.GetType()}
	}

	m.EXPECT().
		GetLedgerInfo(gomock.Any(), testLedger).
		Return(&commonpb.LedgerInfo{
			AccountTypes:   schema.AccountTypes(),
			MetadataSchema: &commonpb.MetadataSchema{AccountFields: fields},
		}, nil)

	// The delta is empty → no schema-mutating reconcile calls. gomock fails the
	// test if either is called (no EXPECT registered).

	// The remaining passes still run (idempotent no-ops at the client layer).
	m.EXPECT().CreateIndex(gomock.Any(), testLedger, gomock.Any()).Return(nil).Times(len(schema.MetadataIndexes()) + len(schema.TransactionIndexes()))
	m.EXPECT().CreatePreparedQuery(gomock.Any(), testLedger, gomock.Any()).Return(nil).Times(len(schema.PreparedQueries()))
	m.EXPECT().SaveNumscript(gomock.Any(), testLedger, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(len(schema.Numscripts()))

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
