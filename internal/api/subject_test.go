package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/formancehq/go-libs/auth"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeJWT builds an unsigned-looking three-segment token carrying the given sub.
// subjectFromAuthHeader does not verify the signature (auth.Middleware does that
// upstream), so a placeholder header/signature is enough to exercise decoding.
func makeJWT(sub string) string {
	seg := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	header := seg(map[string]string{"alg": "RS256", "typ": "JWT"})
	payload := seg(map[string]string{"sub": sub})
	return header + "." + payload + ".c2ln"
}

func TestSubjectFromAuthHeader(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		header string
		want   string
	}{
		"bearer with sub":     {"Bearer " + makeJWT("auth0|alice"), "auth0|alice"},
		"lowercase bearer":    {"bearer " + makeJWT("svc-account"), "svc-account"},
		"empty header":        {"", ""},
		"no bearer prefix":    {makeJWT("nope"), ""},
		"not a jwt":           {"Bearer not-a-jwt", ""},
		"two segments only":   {"Bearer a.b", ""},
		"bad base64 payload":  {"Bearer a.!!!.c", ""},
		"payload without sub": {"Bearer " + threeSeg(map[string]string{"aud": "x"}), ""},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, subjectFromAuthHeader(tc.header))
		})
	}
}

func threeSeg(payload any) string {
	b, _ := json.Marshal(payload)
	return "aGRy." + base64.RawURLEncoding.EncodeToString(b) + ".c2ln"
}

func TestSubjectMiddleware_BindsSubject(t *testing.T) {
	t.Parallel()

	var seen string
	var present bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, present = service.SubjectFromContext(r.Context())
	})

	r := httptest.NewRequest(http.MethodPost, "/alerts/x/ack", nil)
	r.Header.Set("Authorization", "Bearer "+makeJWT("auth0|eve"))
	subjectMiddleware(true)(next).ServeHTTP(httptest.NewRecorder(), r)
	require.True(t, present)
	assert.Equal(t, "auth0|eve", seen)
}

func TestSubjectMiddleware_NoTokenLeavesContextEmpty(t *testing.T) {
	t.Parallel()

	var present bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, present = service.SubjectFromContext(r.Context())
	})

	r := httptest.NewRequest(http.MethodPost, "/alerts/x/ack", nil)
	subjectMiddleware(true)(next).ServeHTTP(httptest.NewRecorder(), r)
	assert.False(t, present)
}

// With auth disabled, auth.Middleware is backed by noAuth: it admits every
// request without reading the Authorization header, so nothing upstream has
// verified the token. Binding its `sub` would let any caller name the actor
// recorded in signed control-ledger metadata, so nothing may be bound — however
// well-formed the token looks.
func TestSubjectMiddleware_AuthDisabledBindsNothing(t *testing.T) {
	t.Parallel()

	var present bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, present = service.SubjectFromContext(r.Context())
	})

	handler := auth.Middleware(auth.NewNoAuth())(subjectMiddleware(false)(next))

	for name, header := range map[string]string{
		"well-formed token": "Bearer " + makeJWT("auth0|alice"),
		"unsigned token":    "Bearer " + threeSeg(map[string]string{"sub": "cfo@bank.example"}),
	} {
		t.Run(name, func(t *testing.T) {
			present = false
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/alerts/x/resolve", nil)
			r.Header.Set("Authorization", header)
			handler.ServeHTTP(rec, r)

			// noAuth lets the request through; the subject must not follow it.
			require.Equal(t, http.StatusOK, rec.Code)
			assert.False(t, present, "no subject may be bound when auth is not enforced")
		})
	}
}
