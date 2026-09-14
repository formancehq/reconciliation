package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/formancehq/reconciliation/pkg/client/models/components"
)

// The fctl plugin imports this module by its exact path and injects its own
// transport through WithClient. Both are part of the published contract, so a
// rename or a widened injection point must fail here rather than downstream.
func TestModuleDeclaresTheExactPublicPathAndInjectionPoint(t *testing.T) {
	const wantModule = "module github.com/formancehq/reconciliation/pkg/client"
	contents, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(contents), wantModule+"\n") {
		t.Fatalf("go.mod does not declare %q:\n%s", wantModule, contents)
	}
	var injected HTTPClient = contractHTTPClient(func(*http.Request) (*http.Response, error) { return nil, nil })
	if _, ok := injected.(interface {
		Do(*http.Request) (*http.Response, error)
	}); !ok {
		t.Fatal("HTTPClient is not the declared Do(*http.Request) (*http.Response, error) contract")
	}
}

type contractHTTPClient func(*http.Request) (*http.Response, error)

func (client contractHTTPClient) Do(request *http.Request) (*http.Response, error) {
	return client(request)
}

func contractResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestReadUsesInjectedHTTPClientAndDecodesTypedResponse(t *testing.T) {
	transport := contractHTTPClient(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "https://product.invalid/policies/policy-1" {
			t.Fatalf("request = %s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "" {
			t.Fatalf("generated security was enabled: %#v", request.Header)
		}
		return contractResponse(http.StatusOK, `{"data":{"id":"policy-1","name":"daily","createdAt":"2026-09-14T10:00:00Z","ledgerName":"default","ledgerQuery":{"account":"users:*"},"paymentsPoolID":"pool-1"}}`), nil
	})

	result, err := New("https://product.invalid", WithClient(transport)).Reconciliation.V1.GetPolicy(context.Background(), "policy-1")
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.PolicyResponse == nil || result.PolicyResponse.Data.ID != "policy-1" || result.PolicyResponse.Data.LedgerName != "default" {
		t.Fatalf("typed response = %#v", result)
	}
}

func TestServerURLOptionsOverrideConstructorURL(t *testing.T) {
	tests := map[string]struct {
		option  SDKOption
		wantURL string
	}{
		"literal": {
			option:  WithServerURL("https://override.invalid"),
			wantURL: "https://override.invalid/policies/policy-1",
		},
		"templated": {
			option:  WithTemplatedServerURL("https://{tenant}.invalid", map[string]string{"tenant": "override"}),
			wantURL: "https://override.invalid/policies/policy-1",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			transport := contractHTTPClient(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() != test.wantURL {
					t.Fatalf("request URL = %q, want %q", request.URL, test.wantURL)
				}
				return contractResponse(http.StatusOK, `{"data":{"id":"policy-1"}}`), nil
			})

			_, err := New("https://constructor.invalid", test.option, WithClient(transport)).Reconciliation.V1.GetPolicy(context.Background(), "policy-1")
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMutationSerializesGeneratedJSONWithoutLosingIntegerTokens(t *testing.T) {
	transport := contractHTTPClient(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.String() != "https://product.invalid/policies" {
			t.Fatalf("request = %s %s", request.Method, request.URL)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		query, ok := decoded["ledgerQuery"].(map[string]any)
		exact, exactOK := query["exact"].(json.Number)
		if !ok || !exactOK || exact.String() != "9007199254740993" {
			t.Fatalf("mutation body lost its integer token: %s", body)
		}
		return contractResponse(http.StatusCreated, `{"data":{"id":"policy-1","name":"daily","createdAt":"2026-09-14T10:00:00Z","ledgerName":"default","ledgerQuery":{"exact":9007199254740993},"paymentsPoolID":"pool-1"}}`), nil
	})

	result, err := New("https://product.invalid", WithClient(transport)).Reconciliation.V1.CreatePolicy(context.Background(), components.PolicyRequest{
		Name:           "daily",
		LedgerName:     "default",
		LedgerQuery:    map[string]any{"exact": json.Number("9007199254740993")},
		PaymentsPoolID: "pool-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.PolicyResponse == nil || result.PolicyResponse.Data.ID != "policy-1" {
		t.Fatalf("typed mutation response = %#v", result)
	}
}

func TestEmptySuccessfulResponseFailsClosed(t *testing.T) {
	transport := contractHTTPClient(func(*http.Request) (*http.Response, error) {
		return contractResponse(http.StatusOK, ""), nil
	})

	result, err := New("https://product.invalid", WithClient(transport)).Reconciliation.V1.GetPolicy(context.Background(), "policy-1")
	if err == nil {
		t.Fatalf("empty successful response accepted as %#v", result)
	}
}

func TestContextCancellationReachesInjectedHTTPClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	want := errors.New("transport observed cancellation")
	transport := contractHTTPClient(func(request *http.Request) (*http.Response, error) {
		if !errors.Is(request.Context().Err(), context.Canceled) {
			t.Fatalf("request context error = %v", request.Context().Err())
		}
		return nil, want
	})

	_, err := New("https://product.invalid", WithClient(transport)).Reconciliation.V1.GetPolicy(ctx, "policy-1")
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want injected transport error", err)
	}
}

func TestNilHTTPResponseFailsClosed(t *testing.T) {
	transport := contractHTTPClient(func(*http.Request) (*http.Response, error) {
		return nil, nil
	})

	result, err := New("https://product.invalid", WithClient(transport)).Reconciliation.V1.GetPolicy(context.Background(), "policy-1")
	if err == nil {
		t.Fatalf("nil HTTP response accepted as %#v", result)
	}
}
