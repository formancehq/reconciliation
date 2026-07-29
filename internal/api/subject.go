package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/formancehq/reconciliation/internal/audit"
	"github.com/formancehq/reconciliation/internal/models"
)

// subjectMiddleware attaches the caller's identity to the request context so
// every journalled write is attributed to whoever actually made it.
//
// This replaces the self-declared `by` field that acknowledgements and
// resolutions used to carry. A client-supplied name is worth nothing to an
// auditor — anyone could write anyone's name in it — so the API keeps accepting
// the field as a human-readable note but the attribution that enters the hash
// comes from the token.
//
// MUST be mounted inside the authenticated group, after auth.Middleware. The
// claims are decoded here without re-verifying the signature, which is sound
// only because a request cannot reach this point unless auth.Middleware already
// verified the token against the issuer's keys. Moving this middleware outside
// that group would turn attribution into something a caller can forge, which is
// precisely the flaw it exists to fix.
func subjectMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(audit.WithSubject(r.Context(), subjectFromRequest(r))))
	})
}

// tokenClaims is the subset of the access token the journal records. Scope is
// left as json.RawMessage because authorization servers emit it both as a
// space-delimited string and as an array.
type tokenClaims struct {
	Subject  string          `json:"sub"`
	Issuer   string          `json:"iss"`
	ClientID string          `json:"client_id"`
	Scope    json.RawMessage `json:"scope"`
}

// subjectFromRequest builds the attribution snapshot.
//
// An unreadable or absent token yields an unknown subject rather than a
// fabricated one. In a deployment with authentication disabled — local
// development — every entry is honestly marked unknown instead of being
// attributed to a plausible-looking principal that never existed.
func subjectFromRequest(r *http.Request) models.Subject {
	raw := bearerToken(r)
	if raw == "" {
		return models.Subject{}
	}

	claims, ok := decodeClaims(raw)
	if !ok {
		return models.Subject{}
	}

	subject := models.Subject{
		Subject: claims.Subject,
		Scopes:  parseScopes(claims.Scope),
	}
	switch {
	case claims.Issuer != "":
		subject.Source = models.SubjectSourceIssuer
		subject.SourceValue = claims.Issuer
	case claims.ClientID != "":
		subject.Source = models.SubjectSourceClientID
		subject.SourceValue = claims.ClientID
	}
	// A machine-to-machine token often carries only a client id and no subject.
	// Recording the client id as the subject keeps "who did this" answerable
	// instead of leaving a blank where an integration acted.
	if subject.Subject == "" && claims.ClientID != "" {
		subject.Subject = claims.ClientID
	}
	return subject
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// decodeClaims reads a JWT's payload segment. Signature verification is
// auth.Middleware's job — see the note on subjectMiddleware.
func decodeClaims(token string) (tokenClaims, bool) {
	segments := strings.Split(token, ".")
	if len(segments) < 2 {
		return tokenClaims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		// Some issuers pad; RawURLEncoding rejects that, so retry padded.
		payload, err = base64.URLEncoding.DecodeString(segments[1])
		if err != nil {
			return tokenClaims{}, false
		}
	}
	var claims tokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return tokenClaims{}, false
	}
	return claims, true
}

func parseScopes(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.Fields(asString)
	}
	var asList []string
	if err := json.Unmarshal(raw, &asList); err == nil {
		return asList
	}
	return nil
}
