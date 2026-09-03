package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/formancehq/go-libs/api"
	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/go-chi/chi/v5"
)

// ControlLedger is the name of reconciliation's control ledger, injected from
// the --ledger-control-name flag so the audit handlers can scope the ledger's
// bucket-wide audit trail to recon's own entries.
type ControlLedger string

// defaultAuditEntriesLimit / maxAuditEntriesLimit bound GET /audit/entries.
const (
	defaultAuditEntriesLimit = 50
	maxAuditEntriesLimit     = 500
)

// signingKeyOption is one registered signing key as the audit UI consumes it.
// PublicKey is standard base64 — the exact value an external auditor feeds to
// ed25519.Verify to check a `_recon` entry, with nobody from Formance involved.
type signingKeyOption struct {
	KeyID       string `json:"keyId"`
	PublicKey   string `json:"publicKey"`
	ParentKeyID string `json:"parentKeyId,omitempty"`
}

type signingKeysResponse struct {
	Keys []signingKeyOption `json:"keys"`
}

// listSigningKeysHandler serves the public signing keys the reconciliation
// control-ledger writes are signed with (EN-1930). It backs the audit tab's
// "verify it yourself" panel: the UI shows the public key and a verification
// recipe so an auditor never has to trust our own verification endpoint.
//
// Best-effort, matching the other ledger-introspection handlers: on a read error
// it returns an empty set (debug-logged) rather than failing the page.
func listSigningKeysHandler(client ledgerIntrospector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := signingKeysResponse{Keys: []signingKeyOption{}}

		keys, err := client.ListSigningKeys(r.Context())
		if err != nil {
			v5log.FromContext(r.Context()).Debugf("list signing keys for the audit panel failed; returning empty: %v", err)
			api.Ok(w, resp)
			return
		}

		for _, k := range keys {
			resp.Keys = append(resp.Keys, signingKeyOption{
				KeyID:       k.KeyID,
				PublicKey:   base64.StdEncoding.EncodeToString(k.PublicKey),
				ParentKeyID: k.ParentKeyID,
			})
		}

		api.Ok(w, resp)
	}
}

// auditEntry is one signed control-ledger entry as the audit tab / an external
// auditor consumes it. `payload` and `signature` are standard base64; the check
// is ed25519.Verify(publicKey, payload, signature) using a key from
// GET /audit/signing-keys — nobody from Formance in the loop. `signed` is false
// for the rare bootstrap write (e.g. the signing-key registration itself).
type auditEntry struct {
	Sequence   uint64   `json:"sequence"`
	Timestamp  string   `json:"timestamp"`
	KeyID      string   `json:"keyId,omitempty"`
	Payload    string   `json:"payload,omitempty"`
	Signature  string   `json:"signature,omitempty"`
	Signed     bool     `json:"signed"`
	Outcome    string   `json:"outcome"`
	OrderCount uint32   `json:"orderCount"`
	Ledgers    []string `json:"ledgers,omitempty"`
	// Populated only for outcome == "failure": why the write was rejected.
	FailureReason  string `json:"failureReason,omitempty"`
	FailureMessage string `json:"failureMessage,omitempty"`
}

type auditEntriesResponse struct {
	Entries []auditEntry `json:"entries"`
}

// prettyFailureReason turns the ledger's ErrorReason enum name
// (e.g. "ERROR_REASON_INSUFFICIENT_FUNDS") into a readable label
// ("insufficient funds"). Empty for the unspecified/zero reason.
func prettyFailureReason(reason string) string {
	if reason == "" || reason == "ERROR_REASON_UNSPECIFIED" {
		return ""
	}
	trimmed := strings.TrimPrefix(reason, "ERROR_REASON_")
	return strings.ToLower(strings.ReplaceAll(trimmed, "_", " "))
}

// listAuditEntriesHandler serves reconciliation's own control-ledger audit
// entries — the ledger's native AuditEntry stream scoped to the control ledger —
// so an auditor gets the {payload, signature, sequence} of every write without
// ledger credentials, and verifies each one against the published public key.
//
// Best-effort, matching the other ledger-introspection handlers: on a read error
// it returns an empty set (debug-logged) rather than failing the page.
func listAuditEntriesHandler(client ledgerIntrospector, control ControlLedger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := auditEntriesResponse{Entries: []auditEntry{}}

		limit := defaultAuditEntriesLimit
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > maxAuditEntriesLimit {
			limit = maxAuditEntriesLimit
		}

		entries, err := client.ListAuditEntries(r.Context(), string(control), limit)
		if err != nil {
			v5log.FromContext(r.Context()).Debugf("list audit entries for the audit tab failed; returning empty: %v", err)
			api.Ok(w, resp)
			return
		}

		for _, e := range entries {
			resp.Entries = append(resp.Entries, toAuditEntryRow(e))
		}

		api.Ok(w, resp)
	}
}

// getAuditEntryHandler serves one audit entry by sequence, with the detail the
// list omits — the failure reason/message on a rejected write. Backs the audit
// tab's expand-a-row interaction. Best-effort: a read error is a 404-ish empty
// (debug-logged) rather than a page failure.
func getAuditEntryHandler(client ledgerIntrospector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		seq, err := strconv.ParseUint(chi.URLParam(r, "sequence"), 10, 64)
		if err != nil {
			api.BadRequest(w, ErrValidation, fmt.Errorf("invalid sequence %q", chi.URLParam(r, "sequence")))
			return
		}

		entry, err := client.GetAuditEntry(r.Context(), seq)
		if err != nil {
			v5log.FromContext(r.Context()).Debugf("get audit entry %d failed: %v", seq, err)
			api.NotFound(w, fmt.Errorf("audit entry %d not found", seq))
			return
		}

		api.Ok(w, toAuditEntryRow(entry))
	}
}

// toAuditEntryRow renders one entry for the API (base64 payload/signature,
// prettified failure reason). Shared by the list and single-entry handlers.
func toAuditEntryRow(e ledger.AuditEntryInfo) auditEntry {
	row := auditEntry{
		Sequence:       e.Sequence,
		KeyID:          e.KeyID,
		Signed:         e.Signed,
		Outcome:        e.Outcome,
		OrderCount:     e.OrderCount,
		Ledgers:        e.Ledgers,
		FailureReason:  prettyFailureReason(e.FailureReason),
		FailureMessage: e.FailureMessage,
	}
	if !e.Timestamp.IsZero() {
		row.Timestamp = e.Timestamp.UTC().Format(time.RFC3339Nano)
	}
	if len(e.Payload) > 0 {
		row.Payload = base64.StdEncoding.EncodeToString(e.Payload)
	}
	if len(e.Signature) > 0 {
		row.Signature = base64.StdEncoding.EncodeToString(e.Signature)
	}
	return row
}
