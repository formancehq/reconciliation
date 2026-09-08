package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/formancehq/reconciliation/internal/api/service"
)

// AuthConfig tells the router whether token authentication is actually enforced
// on this process. It exists because one middleware's safety depends on it:
// subjectMiddleware reads a JWT it does not verify, which is only sound when
// something upstream already did. Supplied from the --auth-enabled flag in
// cmd/serve.go, mirroring audit.Config.
type AuthConfig struct {
	Enabled bool
}

// subjectMiddleware binds the caller's verified access-token subject onto the
// request context, so the write path can attribute a lifecycle action to the
// authenticated human (EN-1930, P1.2) rather than trusting the self-declared
// `by` in the request body.
//
// It MUST be mounted AFTER auth.Middleware, and it decodes the bearer token's
// payload WITHOUT re-verifying the signature: that middleware has already
// verified the token and rejected an invalid one, so the subject is trusted by
// the time we run. Re-verifying would duplicate the upstream work and require
// the signing keyset here, which reconciliation does not wire today.
//
// That reasoning holds only while auth is enforced, hence authEnabled. With
// --auth-enabled=false (the go-libs default) auth.Middleware is backed by
// noAuth, which admits every request and never looks at the Authorization
// header at all — so nothing upstream has verified anything, and any caller
// could present a self-signed (or unsigned) token to have an arbitrary `sub`
// recorded as the authoritative actor in signed control-ledger metadata. That
// is worse than no attribution: the signature over the record would be valid,
// so an auditor could not tell the forgery from a real one. When auth is off
// this middleware therefore binds nothing.
//
// No subject — auth disabled, a public call, or no token — means the write path
// falls back to the self-declared `by`, tagged as unverified.
func subjectMiddleware(authEnabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if !authEnabled {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if subject := subjectFromAuthHeader(r.Header.Get("Authorization")); subject != "" {
				r = r.WithContext(service.WithSubject(r.Context(), subject))
			}

			next.ServeHTTP(w, r)
		})
	}
}

// subjectFromAuthHeader extracts the `sub` claim from a Bearer JWT WITHOUT
// verifying its signature — safe only because auth.Middleware has already
// verified it upstream, which subjectMiddleware is responsible for ensuring
// (see there). Returns "" for a missing or malformed token.
func subjectFromAuthHeader(header string) string {
	const bearer = "bearer "
	if len(header) < len(bearer) || !strings.EqualFold(header[:len(bearer)], bearer) {
		return ""
	}

	token := strings.TrimSpace(header[len(bearer):])
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}

	return claims.Sub
}
