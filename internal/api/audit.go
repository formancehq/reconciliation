package api

import (
	"encoding/base64"
	"net/http"

	"github.com/formancehq/go-libs/api"
	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"
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
