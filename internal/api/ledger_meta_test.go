package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

// fakeIntrospector is an in-memory ledgerIntrospector for the ledger-meta
// handler tests — the real *ledger.Client wraps a live gRPC connection, so the
// interface seam is what makes these handlers unit-testable.
type fakeIntrospector struct {
	ledgers     []string
	ledgersErr  error
	info        *commonpb.LedgerInfo
	infoErr     error
	accounts    []*commonpb.Account
	accountsErr error
	signingKeys []ledger.SigningKeyInfo
	signingErr  error
	auditItems  []ledger.AuditEntryInfo
	auditErr    error
	auditLimit  int
}

func (f *fakeIntrospector) ListLedgers(context.Context) ([]string, error) {
	return f.ledgers, f.ledgersErr
}

func (f *fakeIntrospector) GetLedgerInfo(context.Context, string) (*commonpb.LedgerInfo, error) {
	return f.info, f.infoErr
}

func (f *fakeIntrospector) ListSigningKeys(context.Context) ([]ledger.SigningKeyInfo, error) {
	return f.signingKeys, f.signingErr
}

func (f *fakeIntrospector) ListAuditEntries(_ context.Context, _ string, limit int) ([]ledger.AuditEntryInfo, error) {
	f.auditLimit = limit
	return f.auditItems, f.auditErr
}

// QueryAccountsFunc mirrors the real streaming contract: it invokes fn per
// account and surfaces fn's error verbatim (the handler stops early by returning
// errStopScan), so the limit/cap path is exercised faithfully.
func (f *fakeIntrospector) QueryAccountsFunc(_ context.Context, _ string, _ *commonpb.QueryFilter, fn func(*commonpb.Account) error) error {
	if f.accountsErr != nil {
		return f.accountsErr
	}
	for _, acct := range f.accounts {
		if err := fn(acct); err != nil {
			return err
		}
	}
	return nil
}

func TestListLedgersHandler(t *testing.T) {
	t.Parallel()

	t.Run("lists live ledger names", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		listLedgersHandler(&fakeIntrospector{ledgers: []string{"treasury", "bank"}}).
			ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ledgers", nil))

		require.Equal(t, http.StatusOK, rec.Code)
		var got sharedapi.BaseResponse[ledgersResponse]
		sharedapi.Decode(t, rec.Body, &got)
		names := make([]string, 0, len(got.Data.Ledgers))
		for _, l := range got.Data.Ledgers {
			names = append(names, l.Name)
		}
		require.Equal(t, []string{"treasury", "bank"}, names)
	})

	t.Run("best-effort empty (not null) on read error", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		listLedgersHandler(&fakeIntrospector{ledgersErr: errors.New("ledger unreachable")}).
			ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ledgers", nil))

		require.Equal(t, http.StatusOK, rec.Code, "a broken ledger link must not 500 the rule dialog")
		// Emit [] not null so the UI maps cleanly and falls back to free-text.
		require.Contains(t, rec.Body.String(), `"ledgers":[]`)
	})
}

func TestListLedgerAccountsHandler(t *testing.T) {
	t.Parallel()

	acct := func(addr, asset, balance string) *commonpb.Account {
		return &commonpb.Account{
			Address: addr,
			Volumes: []*commonpb.AccountVolume{
				{Asset: asset, Volumes: &commonpb.VolumesWithBalance{Balance: balance}},
			},
		}
	}
	serve := func(fake *fakeIntrospector, target string) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		r.Get("/ledgers/{ledger}/accounts", listLedgerAccountsHandler(fake))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	t.Run("returns address + per-asset balance, sorted by address", func(t *testing.T) {
		t.Parallel()
		rec := serve(&fakeIntrospector{accounts: []*commonpb.Account{
			acct("bank:usd", "USD", "100"),
			acct("assets:usd", "USD", "42"),
		}}, "/ledgers/demo/accounts")

		require.Equal(t, http.StatusOK, rec.Code)
		var got sharedapi.BaseResponse[ledgerAccountsResponse]
		sharedapi.Decode(t, rec.Body, &got)
		require.Len(t, got.Data.Accounts, 2)
		require.Equal(t, "assets:usd", got.Data.Accounts[0].Address, "accounts sorted by address")
		require.Equal(t, "bank:usd", got.Data.Accounts[1].Address)
		require.Equal(t, "42", got.Data.Accounts[0].Balances["USD"])
		require.False(t, got.Data.Capped)
	})

	t.Run("filters by address prefix", func(t *testing.T) {
		t.Parallel()
		rec := serve(&fakeIntrospector{accounts: []*commonpb.Account{
			acct("bank:usd", "USD", "100"),
			acct("assets:usd", "USD", "42"),
		}}, "/ledgers/demo/accounts?prefix=bank:")

		var got sharedapi.BaseResponse[ledgerAccountsResponse]
		sharedapi.Decode(t, rec.Body, &got)
		require.Len(t, got.Data.Accounts, 1)
		require.Equal(t, "bank:usd", got.Data.Accounts[0].Address)
	})

	t.Run("caps at limit and flags capped", func(t *testing.T) {
		t.Parallel()
		rec := serve(&fakeIntrospector{accounts: []*commonpb.Account{
			acct("a", "USD", "1"), acct("b", "USD", "2"), acct("c", "USD", "3"),
		}}, "/ledgers/demo/accounts?limit=2")

		var got sharedapi.BaseResponse[ledgerAccountsResponse]
		sharedapi.Decode(t, rec.Body, &got)
		require.Len(t, got.Data.Accounts, 2)
		require.True(t, got.Data.Capped)
	})

	t.Run("best-effort empty on read error (unknown ledger)", func(t *testing.T) {
		t.Parallel()
		rec := serve(&fakeIntrospector{accountsErr: errors.New("unknown ledger")}, "/ledgers/nope/accounts")

		require.Equal(t, http.StatusOK, rec.Code)
		var got sharedapi.BaseResponse[ledgerAccountsResponse]
		sharedapi.Decode(t, rec.Body, &got)
		require.Empty(t, got.Data.Accounts)
	})
}

func TestListLedgerMetaFieldsHandler(t *testing.T) {
	t.Parallel()

	serve := func(fake *fakeIntrospector) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		r.Get("/ledgers/{ledger}/meta-fields", listLedgerMetaFieldsHandler(fake))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ledgers/demo/meta-fields", nil))
		return rec
	}

	t.Run("unknown ledger yields empty set (address always indexed)", func(t *testing.T) {
		t.Parallel()
		rec := serve(&fakeIntrospector{info: nil}) // GetLedgerInfo → (nil, nil)

		require.Equal(t, http.StatusOK, rec.Code)
		var got sharedapi.BaseResponse[ledgerMetaFieldsResponse]
		sharedapi.Decode(t, rec.Body, &got)
		require.Empty(t, got.Data.Fields)
		require.Empty(t, got.Data.AccountTypes)
		require.True(t, got.Data.AddressIndexed)
	})

	t.Run("maps declared metadata fields to UI kinds, sorted", func(t *testing.T) {
		t.Parallel()
		rec := serve(&fakeIntrospector{info: &commonpb.LedgerInfo{
			MetadataSchema: &commonpb.MetadataSchema{
				AccountFields: map[string]*commonpb.MetadataFieldSchema{
					"enabled":    {Type: commonpb.MetadataType_METADATA_TYPE_BOOL},
					"created_at": {Type: commonpb.MetadataType_METADATA_TYPE_DATETIME},
				},
			},
			AccountTypes: map[string]*commonpb.AccountType{
				"rule": {Name: "rule", Pattern: "rule:{id}"},
			},
		}})

		require.Equal(t, http.StatusOK, rec.Code)
		var got sharedapi.BaseResponse[ledgerMetaFieldsResponse]
		sharedapi.Decode(t, rec.Body, &got)
		require.Len(t, got.Data.Fields, 2)
		require.Equal(t, "created_at", got.Data.Fields[0].Key, "fields sorted by key")
		require.Equal(t, "datetime", got.Data.Fields[0].Kind)
		require.True(t, got.Data.Fields[0].Ready)
		require.Equal(t, "enabled", got.Data.Fields[1].Key)
		require.Equal(t, "bool", got.Data.Fields[1].Kind)
		require.Len(t, got.Data.AccountTypes, 1)
		require.Equal(t, "rule", got.Data.AccountTypes[0].Name)
		require.Equal(t, "rule:{id}", got.Data.AccountTypes[0].Pattern)
	})

	t.Run("surfaces a read error as 500", func(t *testing.T) {
		t.Parallel()
		rec := serve(&fakeIntrospector{infoErr: errors.New("boom")})
		require.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}
