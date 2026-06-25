package storage

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSameEvidenceJSON pins the canonical-equality contract that drives
// notification suppression: equal CONTENT (regardless of key order or
// whitespace) is "same"; any change in value is "different"; and anything that
// can't be parsed falls back to "different" so we err towards notifying.
func TestSameEvidenceJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{"byte-identical", `{"drift":"50"}`, `{"drift":"50"}`, true},
		{"key order differs", `{"asset":"USD/2","drift":"50"}`, `{"drift":"50","asset":"USD/2"}`, true},
		{"insignificant whitespace", `{"drift":"50"}`, `{  "drift" :  "50" }`, true},
		{"nested key order", `{"o":{"a":1,"b":2}}`, `{"o":{"b":2,"a":1}}`, true},
		{"big integer preserved", `{"n":900000000000000001}`, `{"n":900000000000000001}`, true},
		{"big integer changed", `{"n":900000000000000001}`, `{"n":900000000000000002}`, false},
		{"value changed", `{"drift":"50"}`, `{"drift":"75"}`, false},
		{"extra key", `{"drift":"50"}`, `{"drift":"50","extra":1}`, false},
		{"both empty", ``, ``, true},
		{"one empty", `{"drift":"50"}`, ``, false},
		{"invalid left treated as different", `{not json`, `{"drift":"50"}`, false},
		{"array order is significant", `[1,2]`, `[2,1]`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sameEvidenceJSON(json.RawMessage(tc.a), json.RawMessage(tc.b))
			require.Equal(t, tc.want, got)
		})
	}
}
