package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/formancehq/reconciliation/internal/api/service"
)

// subjectMiddleware binds the caller's verified access-token subject onto the
// request context, so the write path can attribute a lifecycle action to the
// authenticated human (EN-1930, P1.2) rather than trusting the self-declared
// `by` in the request body.
//
// It MUST be mounted AFTER auth.Middleware. That middleware has already verified
// the token's signature and rejected an invalid one, so by the time we run the
// bearer token in the Authorization header is trusted and we only need to READ
// its subject — we decode the JWT payload without re-verifying it. Re-verifying
// would duplicate the upstream work and require the signing keyset here, which
// reconciliation does not wire today. No token (auth disabled in local dev, or a
// public call) → no subject → the write path falls back to the self-declared
// `by`, tagged as unverified.
func subjectMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subject := subjectFromAuthHeader(r.Header.Get("Authorization")); subject != "" {
			r = r.WithContext(service.WithSubject(r.Context(), subject))
		}

		next.ServeHTTP(w, r)
	})
}

// subjectFromAuthHeader extracts the `sub` claim from a Bearer JWT WITHOUT
// verifying its signature — safe only because auth.Middleware has already
// verified it upstream (see subjectMiddleware). Returns "" for a missing or
// malformed token.
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
