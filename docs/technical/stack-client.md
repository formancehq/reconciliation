# Stack client redirect policy

The API and worker build their Formance Stack SDK client through
`internal/stackclient.New` (`cmd/serve.go` and `cmd/worker.go` call it with the
OTLP-instrumented default transport).

The underlying `http.Client` refuses every redirect whose effective origin
differs from the origin of the first request in the chain. Effective origin means
all three of:

- URL scheme
- hostname, compared case-insensitively
- effective port, where a missing port means the scheme default (`80` for HTTP,
  `443` for HTTPS)

The policy is installed as `http.Client.CheckRedirect`, which `oauth2.NewClient`
copies onto the OAuth2 client. It therefore applies to:

- resource requests sent through `oauth2.Transport`, whose `RoundTrip` would
  otherwise attach a fresh bearer token on every redirect hop
- the OAuth2 client-credentials token request, which uses the same underlying
  client and could otherwise forward client credentials to a redirected target

Same-origin redirects, including chains that stay on the same origin, keep
working and keep their bearer token. A cross-origin hop is not followed and the
call returns an error before any request reaches the foreign origin.

This is an outbound credential-protection boundary for this service only. It
does not replace OIDC discovery, token-endpoint validation, or authorization
checks on the receiving side.
