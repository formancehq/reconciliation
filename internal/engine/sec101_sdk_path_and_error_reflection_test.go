package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/formancehq/formance-sdk-go/v3"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/sdkerrors"
)

// upstreamMarker is a distinctive sentinel the stubbed payments service puts
// in its error body, simulating an upstream that echoes request context.
const upstreamMarker = "UPSTREAM-MARKER-7f3a"

const sec101PoolID = "0eb4a31f-751e-42d4-8d5b-2129e6d4cf4c"

// newPaymentsStub spins a benign local httptest server mimicking the payments
// v3 pool-balances endpoints and records every request line. Mode "ok" returns
// balances; mode "err" returns an upstream body and untrusted error code.
func newPaymentsStub(t *testing.T, mode string) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		seen = append(seen, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch mode {
		case "err":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errorCode":    "NOT_FOUND",
				"errorMessage": upstreamMarker + ": no such pool",
				"details":      "pool path was " + r.URL.EscapedPath(),
			})
		default:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"asset": "USD/2", "amount": 100}},
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// stackSDKClient adapts a real *sdk.Formance (pointed at the benign local
// stub) to the engine.SDKClient surface the resolvers consume.
type stackSDKClient struct{ c *sdk.Formance }

func (a stackSDKClient) V2GetBalancesAggregated(ctx context.Context, req operations.V2GetBalancesAggregatedRequest) (*operations.V2GetBalancesAggregatedResponse, error) {
	return a.c.Ledger.V2.GetBalancesAggregated(ctx, req)
}
func (a stackSDKClient) V2ListAccounts(ctx context.Context, req operations.V2ListAccountsRequest) (*operations.V2ListAccountsResponse, error) {
	return a.c.Ledger.V2.ListAccounts(ctx, req)
}
func (a stackSDKClient) V3GetPoolBalances(ctx context.Context, req operations.V3GetPoolBalancesRequest) (*operations.V3GetPoolBalancesResponse, error) {
	return a.c.Payments.V3.GetPoolBalances(ctx, req)
}
func (a stackSDKClient) V3GetPoolBalancesLatest(ctx context.Context, req operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error) {
	return a.c.Payments.V3.GetPoolBalancesLatest(ctx, req)
}

// newStackSDK uses the real SDK against an isolated local fixture.
func newStackSDK(t *testing.T, serverURL string) SDKClient {
	t.Helper()
	c := sdk.New(sdk.WithServerURL(serverURL), sdk.WithClient(&http.Client{Timeout: 5 * time.Second}))
	return stackSDKClient{c: c}
}

// A valid UUID pool read hits only the fixed configured origin path with the
// UUID in the {poolID} slot. (Same-origin by construction; no external-egress
// claim — see header comment.)
func TestSEC101_SDKPoolBalance_ValidUUIDStaysOnConfiguredOrigin(t *testing.T) {
	srv, seen := newPaymentsStub(t, "ok")
	resolver := NewSDKPaymentsResolver(newStackSDK(t, srv.URL))

	pit := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	balances, err := resolver.PoolBalance(t.Context(), sec101PoolID, &pit)
	if err != nil {
		t.Fatalf("PoolBalance(valid uuid): %v", err)
	}
	if balances["USD/2"] == nil || balances["USD/2"].Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("unexpected balances: %v", balances)
	}
	if len(*seen) == 0 {
		t.Fatal("stub recorded no request")
	}
	for _, line := range *seen {
		if !strings.HasPrefix(line, "GET /api/payments/v3/pools/"+sec101PoolID+"/balances?") {
			t.Errorf("unexpected request line: %q", line)
		}
	}
}

// Same fixed-origin check for the latest (no-PIT) route.
func TestSEC101_SDKPoolBalanceLatest_ValidUUID(t *testing.T) {
	srv, seen := newPaymentsStub(t, "ok")
	resolver := NewSDKPaymentsResolver(newStackSDK(t, srv.URL))

	if _, err := resolver.PoolBalance(t.Context(), sec101PoolID, nil); err != nil {
		t.Fatalf("PoolBalance latest(valid uuid): %v", err)
	}
	if len(*seen) != 1 || (*seen)[0] != "GET /api/payments/v3/pools/"+sec101PoolID+"/balances/latest?" {
		t.Errorf("unexpected requests: %v", *seen)
	}
}

// An uppercase-hex UUID is accepted (uuid.Parse contract) and canonicalized to
// lowercase before SDK path construction.
func TestSEC101_SDKPoolBalance_UppercaseUUIDCanonicalizedInPath(t *testing.T) {
	srv, seen := newPaymentsStub(t, "ok")
	resolver := NewSDKPaymentsResolver(newStackSDK(t, srv.URL))

	for _, value := range []string{strings.ToUpper(sec101PoolID), "{" + sec101PoolID + "}", "urn:uuid:" + sec101PoolID, strings.ReplaceAll(sec101PoolID, "-", "")} {
		if _, err := resolver.PoolBalance(t.Context(), value, nil); err != nil {
			t.Fatalf("valid UUID rejected: %v", err)
		}
	}
	if len(*seen) != 4 {
		t.Fatalf("requests: %v", *seen)
	}
	for _, line := range *seen {
		if line != "GET /api/payments/v3/pools/"+sec101PoolID+"/balances/latest?" {
			t.Errorf("expected canonical path, got %s", line)
		}
	}
}

// A malformed (non-UUID) pool ID — e.g. one stored by an older version — is
// rejected by the resolver BEFORE any request is built: the stub must record
// no pool request at all.
func TestSEC101_SDKPoolBalance_MalformedIDRejectedBeforeRequest(t *testing.T) {
	for _, pit := range []*time.Time{nil, new(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))} {
		for _, poolID := range []string{"not-a-uuid", "..%2fpool", "pool?at=1", "https://example.invalid/pool"} {
			client := &fakeSDKClient{}
			_, err := NewSDKPaymentsResolver(client).PoolBalance(t.Context(), poolID, pit)
			if !errors.Is(err, ErrInvalidResourceID) {
				t.Fatalf("expected invalid identifier, got %v", err)
			}
			if client.atCalls+client.latestCalls != 0 {
				t.Fatal("malformed ID reached SDK")
			}
			if strings.Contains(err.Error(), poolID) {
				t.Fatalf("identifier reflected: %s", err)
			}
		}
	}
}

// Upstream JSON error: the returned .Error() must NOT contain the pool
// identifier or the upstream body marker, but must still unwrap to the typed
// *sdkerrors.V3ErrorResponse (chain preserved for logs/debugging).
func TestSEC101_SDKPoolBalance_UpstreamErrorDoesNotLeak(t *testing.T) {
	srv, _ := newPaymentsStub(t, "err")
	resolver := NewSDKPaymentsResolver(newStackSDK(t, srv.URL))

	pit := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := resolver.PoolBalance(t.Context(), sec101PoolID, &pit)
	if err == nil {
		t.Fatal("expected upstream error, got nil")
	}

	msg := err.Error()
	if strings.Contains(msg, sec101PoolID) {
		t.Errorf("LEAK: error reflects poolID: %q", msg)
	}
	if strings.Contains(msg, upstreamMarker) {
		t.Errorf("LEAK: error reflects upstream body marker: %q", msg)
	}

	if _, ok := errors.AsType[*sdkerrors.V3ErrorResponse](err); !ok {
		t.Errorf("error chain must still unwrap to *sdkerrors.V3ErrorResponse, got %T (%v)", err, err)
	}
}

// Same no-leak assertion through the PaymentsResolver interface (latest route).
func TestSEC101_SDKPoolBalanceLatest_UpstreamErrorDoesNotLeak(t *testing.T) {
	srv, _ := newPaymentsStub(t, "err")
	var resolver PaymentsResolver = NewSDKPaymentsResolver(newStackSDK(t, srv.URL))

	poolID := "22222222-3333-4444-5555-666666666666"
	_, err := resolver.PoolBalance(t.Context(), poolID, nil)
	if err == nil {
		t.Fatal("expected upstream error, got nil")
	}
	msg := err.Error()
	if strings.Contains(msg, poolID) || strings.Contains(msg, upstreamMarker) {
		t.Errorf("LEAK: resolver error reflects identifier/body: %q", msg)
	}
}

// A non-JSON upstream error (text/plain body) must not leak either.
func TestSEC101_SDKPoolBalance_UpstreamPlainBodyDoesNotLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, upstreamMarker+" plain body")
	}))
	t.Cleanup(srv.Close)
	resolver := NewSDKPaymentsResolver(newStackSDK(t, srv.URL))

	_, err := resolver.PoolBalance(t.Context(), sec101PoolID, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), upstreamMarker) {
		t.Errorf("LEAK: plain upstream body reflected: %q", err.Error())
	}
}

// Ledger side: AggregateBalance / ListAccounts upstream errors must not reflect
// the ledger name or the upstream body either.
func TestSEC101_SDKLedgerResolver_UpstreamErrorDoesNotLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errorCode":    "VALIDATION",
			"errorMessage": upstreamMarker + " bad ledger query",
		})
	}))
	t.Cleanup(srv.Close)
	ledger := "main"
	resolver := NewSDKLedgerResolver(newStackSDK(t, srv.URL))

	_, err := resolver.AggregateBalance(t.Context(), ledger, json.RawMessage(`{}`), time.Now())
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), ledger) || strings.Contains(err.Error(), upstreamMarker) {
		t.Errorf("LEAK: ledger error reflects name/body: %q", err.Error())
	}

	_, err = resolver.ListAccounts(t.Context(), ledger, json.RawMessage(`{}`), time.Now(), 10)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), ledger) || strings.Contains(err.Error(), upstreamMarker) {
		t.Errorf("LEAK: list-accounts error reflects name/body: %q", err.Error())
	}
}
