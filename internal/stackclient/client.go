// Package stackclient builds the Reconciliation client used to call the
// Formance Stack.
//
// SEC-86: the HTTP client rejects every redirect whose effective origin
// differs from the origin of the first request of the redirect chain. The
// comparison covers scheme, hostname and effective port, and it applies to
// both resource requests and OAuth2 token requests. This prevents
// oauth2.Transport from re-attaching a bearer token, and the token request
// from replaying client credentials, on a cross-origin hop.
package stackclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdk "github.com/formancehq/formance-sdk-go/v3"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// DefaultTimeout bounds both Stack API calls and the OAuth2 token exchange.
const DefaultTimeout = 5 * time.Minute

// New returns the SDK client for stackURL, using client credentials and base
// as the underlying HTTP transport.
func New(stackURL, clientID, clientSecret string, base http.RoundTripper, timeout time.Duration) *sdk.Formance {
	oauthConfig := clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     fmt.Sprintf("%s/api/auth/oauth/token", stackURL),
		Scopes:       []string{"openid", "ledger:read", "ledger:write", "payments:read", "payments:write"},
	}
	underlyingHTTPClient := &http.Client{
		Transport:     base,
		CheckRedirect: checkSameOriginRedirect,
		// Upper bound for ledger/payments calls (aggregated balance queries
		// can be slow on large ledgers) and for the OAuth2 token exchange,
		// which runs outside any request context.
		Timeout: timeout,
	}
	return sdk.New(
		sdk.WithClient(
			oauthConfig.Client(context.WithValue(context.Background(),
				oauth2.HTTPClient, underlyingHTTPClient)),
		),
		sdk.WithServerURL(stackURL),
	)
}

// checkSameOriginRedirect allows every hop of a redirect chain that keeps the
// effective origin (scheme, hostname and effective port) of the initial
// request. Any other hop is refused before it is sent.
func checkSameOriginRedirect(req *http.Request, via []*http.Request) error {
	// Preserve net/http's default redirect-chain bound when installing a
	// custom origin check. Otherwise same-origin loops run until timeout.
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if len(via) == 0 {
		return errors.New("net/http: redirect without an initial request")
	}
	initial := via[0].URL
	if sameOrigin(initial, req.URL) {
		return nil
	}
	return fmt.Errorf("refusing cross-origin redirect from %s to %s", originString(initial), originString(req.URL))
}

// sameOrigin reports whether two URLs have the same RFC 6454 origin. The port
// is effective: a missing port is normalized using the scheme default instead
// of treating two different textual URLs as equal or different by accident.
func sameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme &&
		equalHostname(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
}

// equalHostname compares hostnames case-insensitively, as required for URI
// host comparison. URL.Hostname strips brackets from IPv6 literals.
func equalHostname(a, b string) bool {
	return strings.EqualFold(a, b)
}

// effectivePort returns the explicit port of u, or the default port of its
// scheme when u has none.
func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	return defaultPort(u.Scheme)
}

func defaultPort(scheme string) string {
	switch scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

// originString renders an origin without credentials or path.
func originString(u *url.URL) string {
	port := effectivePort(u)
	if port == defaultPort(u.Scheme) {
		return u.Scheme + "://" + u.Hostname()
	}
	return u.Scheme + "://" + net.JoinHostPort(u.Hostname(), port)
}
