package engine

// SEC-100 phase-1 reproduction tests (test-only; no source changes).
//
// Broken invariant under test: a caller-supplied ledger identifier coming
// from templateSpec.terms[].ledger (or legacy policy ledgerName) must not be
// able to alter the outbound Ledger SDK route, query or fragment, and the
// raw upstream error body must not become caller-visible evaluation detail.
//
// These tests drive the REAL speakeasy SDK URL construction
// (utils.GenerateURL over /api/ledger/v2/{ledger}/aggregate/balances and
// /api/ledger/v2/{ledger}/accounts) against an httptest server that records
// the wire request, exactly like the production wiring in
// internal/api/service/service.go (sdkFormanceClient -> sdk client).
//
// Expected on unpatched base c5435ac: the RED assertions fail, proving the
// defect (traversal/encoded slash reach the wire verbatim; ?/# truncate the
// route; the upstream error body is relayed in the returned error string).
// After the fix, the same tests must pass with zero requests recorded for
// invalid identifiers and a sanitized error for upstream failures.

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	sdk "github.com/formancehq/formance-sdk-go/v3"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
	"github.com/stretchr/testify/require"
)

// wireRecorder is an httptest handler that records every request line and
// returns a fixed upstream body, so tests can assert both what left the
// process and what came back.
type wireRecorder struct {
	mu        sync.Mutex
	paths     []string // r.URL.EscapedPath() + query, as seen on the wire
	rawQuery  []string
	fragments []string
	requests  int
	// upstream response
	status int
	body   string
}

func (w *wireRecorder) handler() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		w.requests++
		w.paths = append(w.paths, r.URL.EscapedPath())
		w.rawQuery = append(w.rawQuery, r.URL.RawQuery)
		w.fragments = append(w.fragments, r.URL.Fragment)
		w.mu.Unlock()
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(w.status)
		_, _ = rw.Write([]byte(w.body))
	}
}

func (w *wireRecorder) snapshot() (int, []string, []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	cp := append([]string(nil), w.paths...)
	q := append([]string(nil), w.rawQuery...)
	return w.requests, cp, q
}

// newRealSDKResolver builds an SDKLedgerResolver backed by the real speakeasy
// SDK client pointed at the test server — the same call chain the service
// layer uses (sdk.Formance.Ledger.V2.GetBalancesAggregated/ListAccounts).
func newRealSDKResolver(t *testing.T, serverURL string) *SDKLedgerResolver {
	t.Helper()
	client := sdk.New(sdk.WithServerURL(serverURL), sdk.WithClient(serverURLHTTPClient()))
	return NewSDKLedgerResolver(&realSDKAdapter{client: client})
}

// realSDKAdapter adapts *sdk.Formance to engine.SDKClient exactly like
// internal/api/service/sdkFormanceClient does.
type realSDKAdapter struct {
	client *sdk.Formance
}

func (a *realSDKAdapter) V2GetBalancesAggregated(ctx context.Context, req operations.V2GetBalancesAggregatedRequest) (*operations.V2GetBalancesAggregatedResponse, error) {
	return a.client.Ledger.V2.GetBalancesAggregated(ctx, req)
}

func (a *realSDKAdapter) V2ListAccounts(ctx context.Context, req operations.V2ListAccountsRequest) (*operations.V2ListAccountsResponse, error) {
	return a.client.Ledger.V2.ListAccounts(ctx, req)
}

func (a *realSDKAdapter) V3GetPoolBalances(ctx context.Context, req operations.V3GetPoolBalancesRequest) (*operations.V3GetPoolBalancesResponse, error) {
	return a.client.Payments.V3.GetPoolBalances(ctx, req)
}

func (a *realSDKAdapter) V3GetPoolBalancesLatest(ctx context.Context, req operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error) {
	return a.client.Payments.V3.GetPoolBalancesLatest(ctx, req)
}

func serverURLHTTPClient() *http.Client { return &http.Client{Timeout: 10 * time.Second} }

var sec100PIT = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// aggregateOKBody is a minimal valid V2AggregateBalancesResponse payload.
const aggregateOKBody = `{"data":{"USD/2":100}}`

func TestSEC100_AggregateBalance_LegitimateNameReachesIntendedRoute(t *testing.T) {
	rec := &wireRecorder{status: 200, body: aggregateOKBody}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	r := newRealSDKResolver(t, srv.URL)
	balances, err := r.AggregateBalance(context.Background(), "tenant:prod.eu-west-1", json.RawMessage(`{"$match":{"address":"world"}}`), sec100PIT)
	require.NoError(t, err)
	require.Equal(t, map[string]*big.Int{"USD/2": big.NewInt(100)}, balances)

	n, paths, _ := rec.snapshot()
	require.Equal(t, 1, n, "exactly one request must leave the process")
	require.Equal(t, "/api/ledger/v2/tenant:prod.eu-west-1/aggregate/balances", paths[0],
		"a supported ledger name (charset [a-zA-Z0-9._:-] per invariants.ValidateLedgerName) must hit the intended single-segment route verbatim")
}

// RED on unpatched base: the traversal string is interpolated raw into the
// URL path template; dot segments reach the wire verbatim.
func TestSEC100_AggregateBalance_TraversalDoesNotReachWire(t *testing.T) {
	rec := &wireRecorder{status: 200, body: aggregateOKBody}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	r := newRealSDKResolver(t, srv.URL)
	ledger := "main/../../payments/v3/connectors/configurations"
	_, err := r.AggregateBalance(context.Background(), ledger, json.RawMessage(`{}`), sec100PIT)

	n, paths, _ := rec.snapshot()
	if n > 0 {
		t.Fatalf("REPRODUCED: invalid ledger identifier produced %d outbound request(s); wire path=%q — the identifier altered the outbound route (err=%v)", n, paths[0], err)
	}
	require.Error(t, err, "invalid ledger identifier must be rejected before any request")
	require.NotContains(t, err.Error(), "payments", "error must not echo the attempted route")
}

// RED on unpatched base: %2F is forwarded undecoded (ReplaceParameters does
// no escaping for style=simple path params), leaving the decision to a
// %2F-decoding gateway.
func TestSEC100_AggregateBalance_EncodedSlashDoesNotReachWire(t *testing.T) {
	rec := &wireRecorder{status: 200, body: aggregateOKBody}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	r := newRealSDKResolver(t, srv.URL)
	ledger := "main%2F..%2F..%2Fpayments%2Fv3%2Fconnectors"
	_, err := r.AggregateBalance(context.Background(), ledger, json.RawMessage(`{}`), sec100PIT)

	n, paths, _ := rec.snapshot()
	if n > 0 {
		t.Fatalf("REPRODUCED: percent-encoded traversal produced %d outbound request(s); wire path=%q (err=%v)", n, paths[0], err)
	}
	require.Error(t, err)
}

// RED on unpatched base: '?' and '#' truncate the route even against a stock
// Go server — "main?next=1" yields /api/ledger/v2/main (no longer the
// aggregate-balances route at all).
func TestSEC100_AggregateBalance_QueryFragmentDelimitersDoNotReshapeRoute(t *testing.T) {
	for _, ledger := range []string{"main?next=1", "main#frag", "main?next=1#f"} {
		rec := &wireRecorder{status: 200, body: aggregateOKBody}
		srv := httptest.NewServer(rec.handler())

		r := newRealSDKResolver(t, srv.URL)
		_, err := r.AggregateBalance(context.Background(), ledger, json.RawMessage(`{}`), sec100PIT)
		srv.Close()

		n, paths, queries := rec.snapshot()
		if n > 0 {
			t.Fatalf("REPRODUCED: ledger %q produced request path=%q query=%q — delimiters reshaped the outbound request (err=%v)", ledger, paths[0], queries[0], err)
		}
		require.Error(t, err, "ledger %q", ledger)
	}
}

// RED on unpatched base: dot-only segments ("..", ".") are path-traversal
// primitives once on the wire; url.PathEscape does NOT escape them, so a
// whole-identifier PathEscape alone cannot neutralize them.
func TestSEC100_AggregateBalance_DotOnlySegmentsRejected(t *testing.T) {
	for _, ledger := range []string{"..", ".", "../.."} {
		rec := &wireRecorder{status: 200, body: aggregateOKBody}
		srv := httptest.NewServer(rec.handler())

		r := newRealSDKResolver(t, srv.URL)
		_, err := r.AggregateBalance(context.Background(), ledger, json.RawMessage(`{}`), sec100PIT)
		srv.Close()

		n, paths, _ := rec.snapshot()
		if n > 0 {
			t.Fatalf("REPRODUCED: dot-only ledger %q produced request path=%q (err=%v)", ledger, paths[0], err)
		}
		require.Error(t, err, "ledger %q", ledger)
	}
}

// Same invariant through the ListAccounts path (per-account fan-out; live
// engine code even though creation is parked in V1).
func TestSEC100_ListAccounts_TraversalDoesNotReachWire(t *testing.T) {
	rec := &wireRecorder{status: 200, body: `{"cursor":{"data":[],"hasMore":false}}`}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	r := newRealSDKResolver(t, srv.URL)
	_, err := r.ListAccounts(context.Background(), "main/../../auth/v1", json.RawMessage(`{}`), sec100PIT, 100)

	n, paths, _ := rec.snapshot()
	if n > 0 {
		t.Fatalf("REPRODUCED: ListAccounts with traversal ledger produced request path=%q (err=%v)", paths[0], err)
	}
	require.Error(t, err)
}

// RED on unpatched base: on a non-2xx upstream response the speakeasy SDK
// returns sdkerrors.SDKError whose Error() embeds the raw upstream Body; the
// resolver wraps it with the ledger name and the runner stores it verbatim as
// evaluation.Error returned to the caller (internal/reconciliation/runner.go
// evaluation.Error = evalErr.Error(); internal/api/evaluation.go). The
// upstream body must not become public evaluation detail.
func TestSEC100_UpstreamErrorBodyNotRelayed(t *testing.T) {
	const secretUpstreamBody = `{"errorCode":"INTERNAL","errorMessage":"upstream secret: route /api/ledger/v2/main/../../payments showed parser class PostgresError at line 7"}`
	rec := &wireRecorder{status: 500, body: secretUpstreamBody}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	r := newRealSDKResolver(t, srv.URL)
	// Use a legitimate name so the ONLY thing under test is error relay.
	_, err := r.AggregateBalance(context.Background(), "main", json.RawMessage(`{}`), sec100PIT)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "upstream secret",
		"REPRODUCED if present: raw upstream error body is relayed to the caller as evaluation.Error")
	require.NotContains(t, err.Error(), "PostgresError")
}

// Ensure the SDK adapter we use matches the production surface (compile-time).
var _ SDKClient = (*realSDKAdapter)(nil)
