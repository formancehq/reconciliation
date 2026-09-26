// Package grpcprotocol declares the Ledger v3 client-facing gRPC contract
// revision this build implements.
//
// Ledger v3 is unreleased: its protobuf field numbers are renumbered between
// development revisions, so a request built against an older contract can
// decode on the server with a *different meaning* rather than fail to decode.
// EN-1851 closes that hole with a mandatory handshake — every business RPC must
// carry exactly one MetadataKey value equal to the server's own revision, or the
// server rejects it with FailedPrecondition before any handler runs. There is no
// negotiation, no legacy support and no bypass flag.
//
// This package is a vendored mirror of the ledger's own pkg/grpcprotocol.
// Version must be re-checked against that file on every proto re-sync
// (`just sync-ledger-proto`): the protos and the revision are one contract, and
// bumping only half of it reintroduces exactly the silent-misdecode failure the
// gate exists to prevent.
package grpcprotocol

import (
	"context"

	"google.golang.org/grpc"
)

const (
	// Version is the ledger service protocol revision this build speaks. It
	// tracks pkg/grpcprotocol.Version in the ledger repo — currently "13" at
	// release/v3.0 7dd615dba — and is independent of the ledger's release
	// SemVer, its git SHA and reconciliation's own version.
	Version = "13"

	// MetadataKey carries the revision. It is public metadata, not a credential.
	MetadataKey = "ledger-protocol-version"
)

// ClientOption declares Version on every unary and streaming RPC, including
// retries and calls issued after the connection switches backend — the gate is
// evaluated per RPC, so a single successful handshake authorizes nothing later.
//
// It is implemented as per-RPC credentials because that is the one dial option
// gRPC consults on every call. grpc-go accumulates PerRPCCredentials rather than
// replacing them, so this composes with the Ed25519 token provider in
// internal/ledgerauth.
func ClientOption() grpc.DialOption {
	return grpc.WithPerRPCCredentials(protocolCredentials{})
}

type protocolCredentials struct{}

func (protocolCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{MetadataKey: Version}, nil
}

// RequireTransportSecurity reports false: the revision is public metadata, so
// requiring TLS here would break the insecure local/dev transport that
// ledgerauth.Config gates behind its own explicit opt-in.
func (protocolCredentials) RequireTransportSecurity() bool {
	return false
}
