package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"time"

	"github.com/formancehq/go-libs/api"
	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"
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
}

type auditEntriesResponse struct {
	Entries []auditEntry `json:"entries"`
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
			row := auditEntry{
				Sequence:   e.Sequence,
				KeyID:      e.KeyID,
				Signed:     e.Signed,
				Outcome:    e.Outcome,
				OrderCount: e.OrderCount,
				Ledgers:    e.Ledgers,
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
			resp.Entries = append(resp.Entries, row)
		}

		api.Ok(w, resp)
	}
}
