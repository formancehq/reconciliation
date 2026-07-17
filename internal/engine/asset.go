package engine

import (
	"strconv"
	"strings"
)

// assetPrecisionMax is the upper bound on an asset's precision suffix. It mirrors
// the ledger's own limit (precision is stored as a single uint8 in the canonical
// volume key), so a precision above it is not a valid ledger asset.
const assetPrecisionMax = 255

// ValidAssetCode reports whether s is a well-formed ledger asset code, e.g.
// "USD", "USDC", "EURC/6". The grammar mirrors the validator the ledger enforces
// on postings — github.com/formancehq/invariants v0.11.0:
//
//	base      [A-Z][A-Z0-9]{0,16}          (1–17 chars, first is a letter, no underscore)
//	precision optional /[1-9][0-9]{0,2}    (1–3 digits, no leading zero, value 1–255)
//
// It is kept as a local mirror rather than importing the ledger's module:
// reconciliation talks to the ledger only via generated protobuf and pins no
// ledger/invariants dependency. This is the single asset-code grammar shared by
// the engine's metadata-prefix guard and the templates' create-time validation,
// so a suffix that clears one clears the other.
func ValidAssetCode(s string) bool {
	base := s
	if i := strings.IndexByte(s, '/'); i >= 0 {
		base = s[:i]
		if !validAssetPrecision(s[i+1:]) {
			return false
		}
	}
	return validAssetBase(base)
}

// validAssetBase checks the pre-slash portion: 1–17 chars, first an uppercase
// letter, the rest uppercase letters or digits.
func validAssetBase(base string) bool {
	if len(base) < 1 || len(base) > 17 {
		return false
	}
	if base[0] < 'A' || base[0] > 'Z' {
		return false
	}
	for i := 1; i < len(base); i++ {
		c := base[i]
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// validAssetPrecision checks the post-slash portion: 1–3 digits, no leading
// zero, decimal value in [1, assetPrecisionMax].
func validAssetPrecision(prec string) bool {
	if len(prec) < 1 || len(prec) > 3 {
		return false
	}
	if prec[0] == '0' { // rejects a leading zero and the bare "0"
		return false
	}
	for i := 0; i < len(prec); i++ {
		if prec[i] < '0' || prec[i] > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(prec)
	if err != nil {
		return false
	}
	return n >= 1 && n <= assetPrecisionMax
}
