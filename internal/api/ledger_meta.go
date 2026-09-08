package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/formancehq/go-libs/api"
	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/go-chi/chi/v5"
)

// ledgerIntrospector is the read-only slice of the ledger client the
// introspection handlers depend on. Declared consumer-side (not in the ledger
// package) so the handlers can be unit-tested with a fake; *ledger.Client
// satisfies it, so newRouter still passes the real client unchanged.
type ledgerIntrospector interface {
	ListLedgers(ctx context.Context) ([]string, error)
	GetLedgerInfo(ctx context.Context, name string) (*commonpb.LedgerInfo, error)
	QueryAccountsFunc(ctx context.Context, ledgerName string, filter *commonpb.QueryFilter, fn func(*commonpb.Account) error) error
	ListSigningKeys(ctx context.Context) ([]ledger.SigningKeyInfo, error)
	ListAuditEntries(ctx context.Context, ledgerName string, limit int) ([]ledger.AuditEntryInfo, error)
	GetAuditEntry(ctx context.Context, sequence uint64) (ledger.AuditEntryInfo, error)
	ResolveAuditEntryByTransaction(ctx context.Context, ledgerName string, transactionID uint64) (ledger.AuditEntryInfo, bool, error)
}

// This file surfaces read-only ledger introspection so the standalone
// reconciliation UI's rule builder can offer a *live* typed metadata-key /
// account picker — sourced through THIS module's existing ledger gRPC
// connection rather than a browser→ledger connection. That keeps the UI a plain
// HTTP client of the module (mirroring the mortgage pattern) while preserving
// the builder's autosuggest. See docs / rfc-console-ui-federation.

// metaFieldOption mirrors the UI rule builder's field option: an indexed
// account-metadata key plus the value kind that drives its operators.
type metaFieldOption struct {
	Key  string `json:"key"`
	Kind string `json:"kind"` // string | int | uint | bool | datetime
	// Ready reflects index build state; only 'ready' keys are actually
	// queryable. Declared fields are indexed on declaration, so this is true.
	Ready bool `json:"ready"`
}

type accountTypeOption struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern,omitempty"`
}

// ledgerMetaFieldsResponse is the {data} payload of GET /ledgers/{ledger}/meta-fields.
type ledgerMetaFieldsResponse struct {
	Fields         []metaFieldOption   `json:"fields"`
	AccountTypes   []accountTypeOption `json:"accountTypes"`
	AddressIndexed bool                `json:"addressIndexed"`
}

// metadataKind maps a ledger metadata field type to the UI's value-kind vocabulary
// ('string' | 'int' | 'uint' | 'bool' | 'datetime'), matching the ledger-client
// helper the explorer/rule builder uses.
func metadataKind(t commonpb.MetadataType) string {
	switch t {
	case commonpb.MetadataType_METADATA_TYPE_BOOL:
		return "bool"
	case commonpb.MetadataType_METADATA_TYPE_DATETIME:
		return "datetime"
	case commonpb.MetadataType_METADATA_TYPE_UINT64,
		commonpb.MetadataType_METADATA_TYPE_UINT8,
		commonpb.MetadataType_METADATA_TYPE_UINT16,
		commonpb.MetadataType_METADATA_TYPE_UINT32:
		return "uint"
	case commonpb.MetadataType_METADATA_TYPE_INT64,
		commonpb.MetadataType_METADATA_TYPE_INT8,
		commonpb.MetadataType_METADATA_TYPE_INT16,
		commonpb.MetadataType_METADATA_TYPE_INT32:
		return "int"
	default:
		return "string"
	}
}

// listLedgerMetaFieldsHandler returns a data ledger's indexed account-metadata
// schema + account types. Declaring a metadata field type is what builds its
// forward index (F8), so the declared account fields ARE the queryable key
// universe for the rule builder. The recon resolver still validates key / type /
// index at rule create, so over-listing here is safe. An unknown ledger yields
// an empty set and the UI falls back to a free-text key.
func listLedgerMetaFieldsHandler(client ledgerIntrospector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "ledger")

		resp := ledgerMetaFieldsResponse{
			Fields:         []metaFieldOption{},
			AccountTypes:   []accountTypeOption{},
			AddressIndexed: true, // the account address is always queryable in v3
		}

		info, err := client.GetLedgerInfo(r.Context(), name)
		if err != nil {
			api.InternalServerError(w, r, err)
			return
		}
		if info == nil {
			api.Ok(w, resp)
			return
		}

		if schema := info.GetMetadataSchema(); schema != nil {
			for key, field := range schema.GetAccountFields() {
				resp.Fields = append(resp.Fields, metaFieldOption{
					Key:   key,
					Kind:  metadataKind(field.GetType()),
					Ready: true,
				})
			}
		}
		for typeName, at := range info.GetAccountTypes() {
			resp.AccountTypes = append(resp.AccountTypes, accountTypeOption{
				Name:    typeName,
				Pattern: at.GetPattern(),
			})
		}

		// Maps iterate in random order — sort for a stable response.
		sort.Slice(resp.Fields, func(i, j int) bool { return resp.Fields[i].Key < resp.Fields[j].Key })
		sort.Slice(resp.AccountTypes, func(i, j int) bool { return resp.AccountTypes[i].Name < resp.AccountTypes[j].Name })

		api.Ok(w, resp)
	}
}

// accountView is one account's address + per-asset balance (minor units, as a
// decimal string to preserve full precision).
type accountView struct {
	Address  string            `json:"address"`
	Balances map[string]string `json:"balances"`
}

type ledgerAccountsResponse struct {
	Accounts []accountView `json:"accounts"`
	// Capped is true when more accounts matched than the returned limit.
	Capped bool `json:"capped"`
}

// errStopScan is the sentinel a bounded account scan returns to stop early.
var errStopScan = errors.New("stop scan")

// listLedgerAccountsHandler lists a data ledger's accounts (address + per-asset
// balance), bounded by ?limit (default 50, max 500) and optionally filtered by
// an address ?prefix. Backs the standalone UI's account-selector autosuggest,
// sourced through this module's ledger gRPC connection. An unknown ledger yields
// an empty list.
func listLedgerAccountsHandler(client ledgerIntrospector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "ledger")
		prefix := r.URL.Query().Get("prefix")

		// `filter` carries this module's own account-query DSL — the shape a
		// rule's source.query takes, and the shape an alert records as
		// effectiveQuery. Passing it here pushes the predicate down to the
		// ledger, so an operator can list the accounts behind an alert instead
		// of reading a list the alert would otherwise have to embed.
		//
		// Its presence also changes the failure contract. Without it this is the
		// rule builder's autosuggest, where an unreachable ledger should degrade
		// to an empty picker; with it the caller asked a specific question and a
		// silent empty answer would read as "nothing matched".
		var (
			filter   *commonpb.QueryFilter
			explicit bool
		)
		if raw := r.URL.Query().Get("filter"); raw != "" {
			if !json.Valid([]byte(raw)) {
				api.BadRequest(w, ErrValidation, fmt.Errorf("'filter' is not valid JSON"))

				return
			}
			translated, err := ledger.TranslateDataQuery(json.RawMessage(raw))
			if err != nil {
				api.BadRequest(w, ErrValidation, fmt.Errorf("'filter' is not a supported account query: %w", err))

				return
			}
			filter, explicit = translated, true
		}

		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > 500 {
			limit = 500
		}

		resp := ledgerAccountsResponse{Accounts: []accountView{}}
		err := client.QueryAccountsFunc(r.Context(), name, filter, func(acct *commonpb.Account) error {
			addr := acct.GetAddress()
			if prefix != "" && !strings.HasPrefix(addr, prefix) {
				return nil
			}
			if len(resp.Accounts) >= limit {
				resp.Capped = true
				return errStopScan
			}
			balances := map[string]string{}
			for asset, bal := range commonpb.BalancesByAsset(acct) {
				balances[asset] = bal.String()
			}
			resp.Accounts = append(resp.Accounts, accountView{Address: addr, Balances: balances})
			return nil
		})
		if err != nil && !errors.Is(err, errStopScan) {
			if explicit {
				// The caller ran a specific query. Surface why it failed — an
				// unindexed metadata key is the common case, and the ledger's own
				// message names the offending field.
				api.BadRequest(w, ErrValidation, fmt.Errorf("query accounts on %q: %w", name, err))

				return
			}
			// Unknown/unconnected ledger or read error: empty (the UI falls back).
			v5log.FromContext(r.Context()).Debugf("list accounts on %q for account picker failed; returning empty: %v", name, err)
			api.Ok(w, ledgerAccountsResponse{Accounts: []accountView{}})

			return
		}

		sort.Slice(resp.Accounts, func(i, j int) bool { return resp.Accounts[i].Address < resp.Accounts[j].Address })
		api.Ok(w, resp)
	}
}

// ledgerNameOption is one ledger's name, surfaced for the rule-builder's ledger
// picker.
type ledgerNameOption struct {
	Name string `json:"name"`
}

type ledgersResponse struct {
	Ledgers []ledgerNameOption `json:"ledgers"`
}

// listLedgersHandler lists the cluster's live ledger names so the standalone
// UI's rule builder can suggest real ledgers instead of pure free-text, sourced
// through this module's ledger gRPC connection. Best-effort: an unreachable
// ledger or read error yields an empty list (the UI keeps the field free-text),
// matching the sibling accounts handler — a broken ledger link never 500s the
// rule dialog.
func listLedgersHandler(client ledgerIntrospector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := ledgersResponse{Ledgers: []ledgerNameOption{}}

		names, err := client.ListLedgers(r.Context())
		if err != nil {
			v5log.FromContext(r.Context()).Debugf("list ledgers for the rule-builder picker failed; returning empty: %v", err)
			api.Ok(w, resp)
			return
		}

		for _, name := range names {
			resp.Ledgers = append(resp.Ledgers, ledgerNameOption{Name: name})
		}
		api.Ok(w, resp)
	}
}
