package engine

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/formancehq/formance-sdk-go/v3"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
	"github.com/formancehq/reconciliation/internal/stackclient"
)

// SEC-86: oauth2.Transport adds the bearer token on every RoundTrip, after
// net/http has filtered copied redirect headers. Token acquisition uses the
// underlying HTTP client and can also replay client credentials on redirects.
// All fixtures use loopback listeners and fake credentials.

const (
	sec86FakeClientID     = "fake-client-id"
	sec86FakeClientSecret = "fake-client-secret"
	sec86FakeAccessToken  = "fake-access-token-SEC86"
)

// sec86Record is one request observed by a loopback origin.
type sec86Record struct {
	Method        string
	Path          string
	Host          string
	Authorization string
	Body          string
}

// sec86Origin is a loopback HTTP(S) server that records every request it gets.
type sec86Origin struct {
	server *httptest.Server
	mu     sync.Mutex
	reqs   []sec86Record
}

func sec86Recording(o *sec86Origin, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Recording must not consume the form used by the token handler.
		r.Body = io.NopCloser(bytes.NewReader(body))
		o.mu.Lock()
		o.reqs = append(o.reqs, sec86Record{
			Method: r.Method, Path: r.URL.Path, Host: r.Host,
			Authorization: r.Header.Get("Authorization"), Body: string(body),
		})
		o.mu.Unlock()
		next(w, r)
	}
}

func newSec86Origin(t *testing.T, next http.HandlerFunc) *sec86Origin {
	t.Helper()
	o := &sec86Origin{}
	o.server = httptest.NewServer(sec86Recording(o, next))
	t.Cleanup(o.server.Close)
	return o
}

// newSec86TLSOrigin is an https loopback origin (ephemeral port, no :443).
func newSec86TLSOrigin(t *testing.T, next http.HandlerFunc) *sec86Origin {
	t.Helper()
	o := &sec86Origin{}
	o.server = httptest.NewTLSServer(sec86Recording(o, next))
	t.Cleanup(o.server.Close)
	return o
}

func (o *sec86Origin) url() string { return o.server.URL }

func (o *sec86Origin) requests() []sec86Record {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]sec86Record, len(o.reqs))
	copy(out, o.reqs)
	return out
}

func (o *sec86Origin) count() int { return len(o.requests()) }

// bearerSeen reports whether the fake bearer token reached this origin.
func (o *sec86Origin) bearerSeen() bool {
	for _, r := range o.requests() {
		if r.Authorization == "Bearer "+sec86FakeAccessToken {
			return true
		}
	}
	return false
}

// secretSeen reports whether the fake client secret reached this origin, in
// plaintext (POST body) or base64-encoded (Basic authorization header).
func (o *sec86Origin) secretSeen() bool {
	basic := base64.StdEncoding.EncodeToString([]byte(sec86FakeClientID + ":" + sec86FakeClientSecret))
	for _, r := range o.requests() {
		if strings.Contains(r.Authorization, sec86FakeClientSecret) ||
			strings.Contains(r.Authorization, basic) ||
			strings.Contains(r.Body, sec86FakeClientSecret) {
			return true
		}
	}
	return false
}

// sec86LedgerOK answers a syntactically valid V2ListLedgers response.
func sec86LedgerOK(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"cursor":null,"data":[]}`)
}

// sec86Redirect answers code with Location.
func sec86Redirect(location string, code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", location)
		w.WriteHeader(code)
	}
}

// sec86BasicHasSecret reports whether a Basic authorization header carries the
// fake client secret as the password (base64-decoded, not substring-matched).
func sec86BasicHasSecret(authHeader string) bool {
	scheme, encoded, ok := strings.Cut(authHeader, " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	_, password, ok := strings.Cut(string(decoded), ":")
	return ok && password == sec86FakeClientSecret
}

// sec86Token issues the fake bearer token when the client secret is present,
// either in the POST body (AuthStyleInParams) or Basic header
// (AuthStyleInHeader).
func sec86Token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if r.PostForm.Get("client_secret") != sec86FakeClientSecret &&
		!sec86BasicHasSecret(r.Header.Get("Authorization")) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, fmt.Sprintf(
		`{"access_token":%q,"token_type":"Bearer","expires_in":3600}`, sec86FakeAccessToken))
}

// newSec86StackClient exercises the production constructor used by
// cmd/serve.go. The token endpoint is a path on the same loopback stack origin,
// exactly like the production TokenURL. base is http.DefaultTransport except
// for the TLS fixture, which needs InsecureSkipVerify on loopback.
func newSec86StackClient(t *testing.T, stackURL string, base http.RoundTripper) *sdk.Formance {
	t.Helper()
	return stackclient.New(stackURL, sec86FakeClientID, sec86FakeClientSecret, base, 30*time.Second)
}

// sec86ListLedgers issues a real SDK call through the oauth2 client to a
// loopback origin.
func sec86ListLedgers(t *testing.T, client *sdk.Formance, serverURL string) error {
	t.Helper()
	_, err := client.Ledger.V2.ListLedgers(t.Context(), operations.V2ListLedgersRequest{
		RequestBody: map[string]any{},
	}, operations.WithServerURL(serverURL))
	return err
}

// sec86AssertNoForeignTraffic fails if any request — let alone one carrying
// the bearer token or client secret — reached the foreign (cross-origin)
// origin. After the fix the cross-origin hop must be blocked outright, so zero
// requests is the expected state.
func sec86AssertNoForeignTraffic(t *testing.T, foreign *sec86Origin, what string) {
	t.Helper()
	if foreign.bearerSeen() {
		t.Fatalf("SEC-86 REPRODUCED: %s: bearer token forwarded to cross-origin target %s", what, foreign.url())
	}
	if foreign.secretSeen() {
		t.Fatalf("SEC-86 REPRODUCED: %s: client secret forwarded to cross-origin target %s", what, foreign.url())
	}
	if n := foreign.count(); n > 0 {
		t.Fatalf("SEC-86 REPRODUCED: %s: %d request(s) reached cross-origin target %s", what, n, foreign.url())
	}
}

// TestSEC86_InitialRequestCarriesBearer is the positive control: the initial
// request to the stack origin legitimately carries the bearer token.
func TestSEC86_InitialRequestCarriesBearer(t *testing.T) {
	tokenSrv := newSec86Origin(t, sec86Token)
	stack := newSec86Origin(t, sec86LedgerOK)

	client := newSec86StackClient(t, tokenSrv.url(), http.DefaultTransport)
	if err := sec86ListLedgers(t, client, stack.url()); err != nil {
		t.Fatalf("control failed: initial ledger call errored: %v", err)
	}
	if !stack.bearerSeen() {
		t.Fatal("control failed: initial request did not carry the bearer token")
	}
}

// TestSEC86_SameOriginRedirectKeepsBearer is the positive control for
// redirects: ONE server, redirect from one path to another (same origin,
// different path) must keep working and keep the bearer token.
func TestSEC86_SameOriginRedirectKeepsBearer(t *testing.T) {
	tokenSrv := newSec86Origin(t, sec86Token)
	// Single origin, two paths: /redirect -> 302 -> /final (same origin).
	stack := newSec86Origin(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect/api/ledger/v2" {
			sec86Redirect("/final", http.StatusFound)(w, r)
			return
		}
		sec86LedgerOK(w, r)
	})

	client := newSec86StackClient(t, tokenSrv.url(), http.DefaultTransport)
	if err := sec86ListLedgers(t, client, stack.url()+"/redirect"); err != nil {
		t.Fatalf("control failed: same-origin redirect call errored: %v", err)
	}
	reqs := stack.requests()
	if len(reqs) != 2 || reqs[1].Path != "/final" {
		t.Fatalf("control failed: expected the same-origin redirect to /final, got %+v", reqs)
	}
	for _, req := range reqs {
		if req.Authorization != "Bearer "+sec86FakeAccessToken {
			t.Fatal("control failed: same-origin redirect lost the bearer token")
		}
	}
}

// TestSEC86_CrossOriginRedirectLeaksBearer is the core SEC-86 reproduction:
// a redirect from the stack origin to a DIFFERENT origin (host, port or
// scheme) must not carry the bearer token, and after the fix must not be
// followed at all. Cross-origin targets are other loopback listeners.
func TestSEC86_CrossOriginRedirectLeaksBearer(t *testing.T) {
	// cross-origin target factories, all loopback.
	targets := map[string]func(t *testing.T) (location string, foreign *sec86Origin, base http.RoundTripper){
		// different hostname, same machine: 127.0.0.1 -> localhost
		"cross-host": func(t *testing.T) (string, *sec86Origin, http.RoundTripper) {
			foreign := newSec86Origin(t, sec86LedgerOK)
			_, port, err := net.SplitHostPort(strings.TrimPrefix(foreign.url(), "http://"))
			if err != nil {
				t.Fatal(err)
			}
			return "http://localhost:" + port + "/api/ledger/v2", foreign, http.DefaultTransport
		},
		// same hostname, different port => different origin (RFC 6454)
		"cross-port": func(t *testing.T) (string, *sec86Origin, http.RoundTripper) {
			foreign := newSec86Origin(t, sec86LedgerOK)
			return foreign.url() + "/api/ledger/v2", foreign, http.DefaultTransport
		},
		// http -> https on an ephemeral port (no :443 needed)
		"cross-scheme-https": func(t *testing.T) (string, *sec86Origin, http.RoundTripper) {
			foreign := newSec86TLSOrigin(t, sec86LedgerOK)
			// loopback fixture only: trust the self-signed httptest cert
			base := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
			return foreign.url() + "/api/ledger/v2", foreign, base
		},
	}
	codes := map[string]int{
		"301": http.StatusMovedPermanently,
		"302": http.StatusFound,
		"303": http.StatusSeeOther,
		"307": http.StatusTemporaryRedirect,
		"308": http.StatusPermanentRedirect,
	}
	for tname, makeTarget := range targets {
		for cname, code := range codes {
			t.Run(tname+"/"+cname, func(t *testing.T) {
				tokenSrv := newSec86Origin(t, sec86Token)
				location, foreign, base := makeTarget(t)
				redirector := newSec86Origin(t, sec86Redirect(location, code))

				client := newSec86StackClient(t, tokenSrv.url(), base)
				err := sec86ListLedgers(t, client, redirector.url())
				if !redirector.bearerSeen() {
					t.Fatal("control failed: redirecting origin did not receive the bearer")
				}
				sec86AssertNoForeignTraffic(t, foreign, "redirect "+cname+" "+tname)
				if err == nil {
					t.Fatal("cross-origin redirect must return an error")
				}
			})
		}
	}
}

// TestSEC86_RedirectChainCrossOriginLeaksBearer: a chain with a VALID
// same-origin intermediate hop (different path on the same server) that then
// redirects cross-origin must not forward the bearer token.
func TestSEC86_RedirectChainCrossOriginLeaksBearer(t *testing.T) {
	tokenSrv := newSec86Origin(t, sec86Token)
	foreign := newSec86Origin(t, sec86LedgerOK)
	// One origin: /start -> 302 /hop (same origin) -> 302 cross-origin.
	stack := newSec86Origin(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start/api/ledger/v2":
			sec86Redirect("/hop", http.StatusFound)(w, r)
		case "/hop":
			sec86Redirect(foreign.url()+"/api/ledger/v2", http.StatusFound)(w, r)
		default:
			sec86LedgerOK(w, r)
		}
	})

	client := newSec86StackClient(t, tokenSrv.url(), http.DefaultTransport)
	err := sec86ListLedgers(t, client, stack.url()+"/start")

	// the same-origin intermediate hop must really have been followed
	if n := stack.count(); n < 2 {
		t.Fatalf("chain did not execute the same-origin hop (only %d request(s) at %s)", n, stack.url())
	}
	sec86AssertNoForeignTraffic(t, foreign, "redirect chain via same-origin hop")
	if err == nil {
		t.Fatal("cross-origin redirect chain must return an error")
	}
}

// TestSEC86_TokenEndpointRedirectDoesNotForwardClientSecret: the token POST
// goes through the same underlying *http.Client. A redirect on the token
// endpoint (302/307/308) to a cross-origin target must not forward the client
// secret — neither base64-encoded in the Basic authorization header nor
// replayed in the POST body (307/308 re-send the body via GetBody).
func TestSEC86_TokenEndpointRedirectDoesNotForwardClientSecret(t *testing.T) {
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(fmt.Sprintf("code-%d", code), func(t *testing.T) {
			foreign := newSec86Origin(t, sec86LedgerOK) // records whatever arrives
			tokenRedirector := newSec86Origin(t, sec86Redirect(foreign.url()+"/token", code))
			ledger := newSec86Origin(t, sec86LedgerOK)

			client := newSec86StackClient(t, tokenRedirector.url(), http.DefaultTransport)
			err := sec86ListLedgers(t, client, ledger.url()) // forces the token fetch
			if !tokenRedirector.secretSeen() {
				t.Fatal("control failed: token endpoint did not receive the client secret")
			}
			sec86AssertNoForeignTraffic(t, foreign, fmt.Sprintf("token-endpoint redirect %d", code))
			if err == nil {
				t.Fatal("cross-origin token redirect must return an error")
			}
			if ledger.count() != 0 {
				t.Fatal("ledger call must not proceed after the token redirect fails")
			}
		})
	}
}
