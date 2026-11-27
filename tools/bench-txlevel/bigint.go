package main

import "math/big"

type bigConv interface{ ToBigInt() (*big.Int, error) }

// bigv converts a Ledger big integer; ok is false when it is malformed.
func bigv(v bigConv) (*big.Int, bool) {
	b, err := v.ToBigInt()
	if err != nil || b == nil {
		return new(big.Int), false
	}
	return b, true
}

// bigz converts a Ledger big integer, reading a malformed value as zero.
func bigz(v bigConv) *big.Int {
	b, _ := bigv(v)
	return b
}
