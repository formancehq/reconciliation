package stackclient

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckSameOriginRedirect(t *testing.T) {
	initial := mustURL(t, "http://stack.internal:8080/api/ledger/v2")
	cases := []struct {
		name    string
		target  string
		wantErr bool
	}{
		{name: "same origin", target: "http://stack.internal:8080/final", wantErr: false},
		{name: "same origin without explicit port", target: "http://stack.internal:80/final", wantErr: true},
		{name: "cross host", target: "http://localhost:8080/final", wantErr: true},
		{name: "cross port", target: "http://stack.internal:9090/final", wantErr: true},
		{name: "cross scheme", target: "https://stack.internal:8080/final", wantErr: true},
		{name: "https default port equals explicit 443", target: "https://stack.internal:443/final", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &http.Request{URL: mustURL(t, tc.target)}
			err := checkSameOriginRedirect(req, []*http.Request{{URL: initial}})
			if tc.wantErr && err == nil {
				t.Fatalf("expected cross-origin redirect to %s to be refused", tc.target)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected same-origin redirect to %s to be allowed, got %v", tc.target, err)
			}
		})
	}
}

func TestSameOriginEffectivePorts(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"http://example.com", "http://example.com:80", true},
		{"https://example.com", "https://example.com:443", true},
		{"http://EXAMPLE.com", "http://example.com:80", true},
		{"http://example.com", "http://example.com:8080", false},
		{"http://example.com", "https://example.com", false},
		{"http://example.com", "http://other.example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.a+" vs "+tc.b, func(t *testing.T) {
			if got := sameOrigin(mustURL(t, tc.a), mustURL(t, tc.b)); got != tc.want {
				t.Fatalf("sameOrigin(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestRedirectPolicyPreservesDefaultChainBound(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer server.Close()
	client := &http.Client{CheckRedirect: checkSameOriginRedirect, Timeout: time.Second}
	response, err := client.Get(server.URL)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		t.Fatal("a same-origin loop must stop")
	}
	if got := calls.Load(); got != 10 {
		t.Fatalf("requests = %d, want the standard limit of 10", got)
	}
}
