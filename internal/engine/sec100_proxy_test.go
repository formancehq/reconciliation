package engine

import (
	"encoding/json"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Model a gateway that decodes and cleans paths before routing. Both accepted
// names and rejected inputs exercise the real SDK through a loopback proxy.
func TestSEC100_LedgerRoutesThroughNormalizingProxy(t *testing.T) {
	for _, list := range []bool{false, true} {
		t.Run(map[bool]string{false: "aggregate", true: "accounts"}[list], func(t *testing.T) {
			body := aggregateOKBody
			suffix := "/aggregate/balances"
			if list {
				body = `{"cursor":{"data":[],"hasMore":false}}`
				suffix = "/accounts"
			}
			rec := &wireRecorder{status: 200, body: body}
			backend := httptest.NewServer(rec.handler())
			defer backend.Close()
			target, err := url.Parse(backend.URL)
			require.NoError(t, err)
			proxy := &httputil.ReverseProxy{Rewrite: func(req *httputil.ProxyRequest) {
				req.SetURL(target)
				decoded, decodeErr := url.PathUnescape(req.Out.URL.Path)
				if decodeErr == nil {
					req.Out.URL.Path = path.Clean(decoded)
					req.Out.URL.RawPath = ""
				}
			}}
			gateway := httptest.NewServer(proxy)
			defer gateway.Close()
			resolver := newRealSDKResolver(t, gateway.URL)
			for _, name := range []string{"tenant:prod.eu-west-1", ".ledger", "ledger.", "a..b", "...", strings.Repeat("a", 64)} {
				if list {
					_, err = resolver.ListAccounts(t.Context(), name, json.RawMessage(`{}`), sec100PIT, 100)
				} else {
					_, err = resolver.AggregateBalance(t.Context(), name, json.RawMessage(`{}`), sec100PIT)
				}
				require.NoError(t, err)
				n, paths, queries := rec.snapshot()
				require.Equal(t, "/api/ledger/v2/"+name+suffix, paths[n-1])
				require.NotContains(t, queries[n-1], "next=")
			}
			before, _, _ := rec.snapshot()
			for _, name := range []string{".", "..", "main/../../auth", "main%2F..%2Fauth", "main%252F..%252Fauth", "main?next=1", "main#frag", `main\..\auth`, strings.Repeat("a", 65)} {
				if list {
					_, err = resolver.ListAccounts(t.Context(), name, json.RawMessage(`{}`), sec100PIT, 100)
				} else {
					_, err = resolver.AggregateBalance(t.Context(), name, json.RawMessage(`{}`), sec100PIT)
				}
				require.Error(t, err)
			}
			after, _, _ := rec.snapshot()
			require.Equal(t, before, after, "invalid identifiers must not reach the backend")
		})
	}
}

func TestSEC100_ListAccountsMasksUpstreamError(t *testing.T) {
	rec := &wireRecorder{status: 500, body: `{"errorMessage":"fake-secret PostgresError"}`}
	server := httptest.NewServer(rec.handler())
	defer server.Close()
	_, err := newRealSDKResolver(t, server.URL).ListAccounts(t.Context(), "main", json.RawMessage(`{}`), sec100PIT, 100)
	require.EqualError(t, err, "list ledger accounts: upstream request failed")
	require.NotContains(t, err.Error(), "fake-secret")
	require.NotContains(t, err.Error(), "PostgresError")
}
