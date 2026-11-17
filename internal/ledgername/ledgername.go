// Package ledgername defines the Ledger identifier contract shared by request
// validation and outbound SDK calls.
package ledgername

import (
	"errors"
	"net/url"
)

// ErrInvalid never includes the supplied identifier.
var ErrInvalid = errors.New("invalid ledger name: expected 1-64 ASCII letters, digits or . _ : -; dot segments are forbidden")

// Validate follows invariants.ValidateLedgerName (v0.11.0), with an additional
// rejection of the URL dot segments. Leading/trailing dots and colons remain
// valid. It does not apply Ledger v3's admission reserved-name policy to
// these Ledger v2 reads.
func Validate(name string) error {
	if len(name) == 0 || len(name) > 64 || name == "." || name == ".." {
		return ErrInvalid
	}
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == ':' || c == '-' {
			continue
		}
		return ErrInvalid
	}
	return nil
}

// PathSegment validates then escapes the complete name exactly once for the
// SDK's raw simple path parameter. Never split names on path separators.
func PathSegment(name string) (string, error) {
	if err := Validate(name); err != nil {
		return "", err
	}
	return url.PathEscape(name), nil
}
