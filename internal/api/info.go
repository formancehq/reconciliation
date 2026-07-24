package api

import (
	"encoding/json"
	"net/http"
)

// ModuleInfo is the discovery payload served at /_info. It extends the standard
// go-libs ServiceInfo ({version, debug}) with the UI-federation fields the
// console shell reads to DISCOVER and EMBED this module's own business UI:
// a stable name, a human label, an icon hint, and the base URL where the UI is
// served. See mortgage-ledger-v3-poc/docs/drafts/rfc-console-ui-federation.md.
type ModuleInfo struct {
	Version string `json:"version"`
	Debug   bool   `json:"debug"`
	Name    string `json:"name"`
	Label   string `json:"label"`
	Icon    string `json:"icon"`
	UIURL   string `json:"uiUrl"`
}

// infoHandler serves the extended /_info discovery payload.
func infoHandler(info ModuleInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(info); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
