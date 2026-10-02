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

// fullChart is the LedgerInfo of a ledger that already carries the whole chart.
func fullChart() *commonpb.LedgerInfo {
	fields := map[string]*commonpb.MetadataFieldSchema{}
	for _, cmd := range schema.MetadataSchema() {
		fields[cmd.GetKey()] = &commonpb.MetadataFieldSchema{Type: cmd.GetType()}
	}

	return &commonpb.LedgerInfo{
		AccountTypes:   schema.AccountTypes(),
		MetadataSchema: &commonpb.MetadataSchema{AccountFields: fields},
	}
}

// expectIndexesAndNumscriptsCreated registers the index and numscript passes of
// a ledger that has none yet, and returns the numscript names registered.
func expectIndexesAndNumscriptsCreated(t *testing.T, m *MockprovisionAPI) *[]string {
	t.Helper()

	m.EXPECT().ListIndexIDs(gomock.Any(), testLedger).Return(nil, nil)

	// The queryable metadata indexes are created (id at minimum, for id→address
	// resolution).
	m.EXPECT().
		CreateIndex(gomock.Any(), testLedger, gomock.Any()).
		Return(nil).
		Times(len(schema.MetadataIndexes()) + len(schema.TransactionIndexes()))

	m.EXPECT().NumscriptVersions(gomock.Any(), testLedger, gomock.Any()).Return(nil, nil).Times(len(schema.Numscripts()))

	// Capture the numscripts actually registered (assert non-empty content +
	// pinned version).
	scripts := &[]string{}

	m.EXPECT().
		SaveNumscript(gomock.Any(), testLedger, gomock.Any(), gomock.Any(), schema.NumscriptVersion).
		DoAndReturn(func(_ context.Context, _, name, content, _ string) error {
			if content == "" {
				t.Errorf("numscript %q has empty content", name)
			}

			*scripts = append(*scripts, name)

			return nil
		}).
		Times(len(schema.Numscripts()))

	return scripts
}

func TestProvisioner_Provision(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockprovisionAPI(ctrl)

	// No ledger yet: the first read finds nothing, the second reads the chart
	// CreateLedger applied.
	m.EXPECT().GetLedgerInfo(gomock.Any(), testLedger).Return(nil, nil)
	m.EXPECT().GetLedgerInfo(gomock.Any(), testLedger).Return(fullChart(), nil)

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

	// CreateLedger applied the whole chart, so no AddAccountType or
	// SetMetadataFieldType follows: gomock fails the test on either.
	scripts := expectIndexesAndNumscriptsCreated(t, m)

	p := NewProvisioner(m, testLedger, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	if err := p.Provision(context.Background()); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	for _, want := range []string{schema.NumscriptAlertOpen, schema.NumscriptAlertBump, schema.NumscriptAlertReopen, schema.NumscriptAlertMove} {
		if !slices.Contains(*scripts, want) {
			t.Errorf("numscript %q not registered (got %v)", want, *scripts)
		}
	}
}

// TestProvisioner_ReconcilesAStaleLedger is F8: an existing ledger whose chart
// predates the current one gets every missing account type and metadata field,
// without being re-created.
func TestProvisioner_ReconcilesAStaleLedger(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockprovisionAPI(ctrl)

	// The ledger exists with nothing declared. No CreateLedger is expected.
	m.EXPECT().GetLedgerInfo(gomock.Any(), testLedger).Return(&commonpb.LedgerInfo{}, nil)

	var addedTypes []string

	m.EXPECT().
		AddAccountType(gomock.Any(), testLedger, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, at *commonpb.AccountType) error {
			addedTypes = append(addedTypes, at.GetName())

			return nil
		}).
		Times(len(schema.AccountTypes()))

	var setFields []string

	m.EXPECT().
		SetMetadataFieldType(gomock.Any(), testLedger, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, cmd *commonpb.SetMetadataFieldTypeCommand) error {
			setFields = append(setFields, cmd.GetKey())

			return nil
		}).
		Times(len(schema.MetadataSchema()))

	expectIndexesAndNumscriptsCreated(t, m)

	p := NewProvisioner(m, testLedger, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	if err := p.Provision(context.Background()); err != nil {
		t.Fatalf("Provision: %v", err)
	}

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

// TestProvisioner_UpToDateLedgerWritesNothing is the boot guard: on a ledger
// that already carries the chart, every index and every numscript version, a
// boot issues no write at all. Re-declaring an indexed metadata field would
// rewrite its index, and every refused re-create is still a signed, rejected
// audit entry.
func TestProvisioner_UpToDateLedgerWritesNothing(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockprovisionAPI(ctrl)

	m.EXPECT().GetLedgerInfo(gomock.Any(), testLedger).Return(fullChart(), nil)
	m.EXPECT().
		ListIndexIDs(gomock.Any(), testLedger).
		Return(slices.Concat(schema.MetadataIndexes(), schema.TransactionIndexes()), nil)
	m.EXPECT().
		NumscriptVersions(gomock.Any(), testLedger, gomock.Any()).
		Return([]string{"1.0.0", schema.NumscriptVersion}, nil).
		Times(len(schema.Numscripts()))

	// No EXPECT for CreateLedger, AddAccountType, SetMetadataFieldType,
	// CreateIndex or SaveNumscript: gomock fails the test if one is called.

	p := NewProvisioner(m, testLedger, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	if err := p.Provision(context.Background()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
}

func TestProvisioner_CreateLedgerErrorShortCircuits(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockprovisionAPI(ctrl)

	m.EXPECT().GetLedgerInfo(gomock.Any(), testLedger).Return(nil, nil)
	m.EXPECT().
		CreateLedger(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("boom"))
	// No further pass may run when CreateLedger fails: no EXPECT is set for any of
	// them, so gomock fails the test if one is called.

	p := NewProvisioner(m, testLedger, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	if err := p.Provision(context.Background()); err == nil {
		t.Fatal("expected an error when CreateLedger fails")
	}
}
