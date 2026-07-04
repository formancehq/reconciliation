package ledgerauth

import (
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// ErrInsecureTransport is returned by Config.Build when neither TLS nor an
// explicit AllowInsecure opt-in is configured. Reconciliation writes
// financial-control data, so it refuses to talk to the ledger in the clear
// unless the operator explicitly asks for it (local/dev only). (F2)
var ErrInsecureTransport = errors.New("refusing insecure ledger transport: configure TLS (ledger-tls-*) or explicitly allow insecure (local/dev only)")

// Config is the full transport configuration for the ledger gRPC client:
// address, TLS material, and the Ed25519 request-signing key. It is the single
// place that decides whether a connection is allowed to be insecure.
type Config struct {
	// Address is the ledger gRPC endpoint (host:port).
	Address string

	// TLS carries the server/CA/client certificate material. When zero, the
	// transport is plaintext and requires AllowInsecure.
	TLS TLSConfig

	// AuthKeyID + AuthKeyFile enable Ed25519 JWT request signing (both must be
	// set together, or neither); AuthSubject is the JWT subject claim.
	AuthKeyID   string
	AuthKeyFile string
	AuthSubject string

	// AllowInsecure explicitly permits a plaintext connection with no TLS.
	// Without it, Build refuses such a transport (F2). Intended for local/dev.
	AllowInsecure bool
}

// Build assembles the gRPC transport credentials and per-RPC dial options from
// the config, enforcing the F2 rule that a plaintext transport requires an
// explicit AllowInsecure opt-in. The returned creds + dialOpts are passed
// straight to ledger.NewClient.
func (c Config) Build() (credentials.TransportCredentials, []grpc.DialOption, error) {
	creds, err := c.transportCredentials()
	if err != nil {
		return nil, nil, err
	}

	dialOpts, err := c.signingDialOptions()
	if err != nil {
		return nil, nil, err
	}

	return creds, dialOpts, nil
}

// transportCredentials returns TLS credentials when any TLS material is set,
// insecure credentials when insecure is explicitly allowed, and
// ErrInsecureTransport otherwise.
func (c Config) transportCredentials() (credentials.TransportCredentials, error) {
	if !c.TLS.IsZero() {
		creds, err := c.TLS.Build()
		if err != nil {
			return nil, fmt.Errorf("build ledger TLS config: %w", err)
		}

		return creds, nil
	}

	if c.AllowInsecure {
		return insecure.NewCredentials(), nil
	}

	return nil, ErrInsecureTransport
}

// signingDialOptions returns the per-RPC Ed25519 signing credentials when an
// auth key is configured, enforcing that key-id and key-file are set together.
func (c Config) signingDialOptions() ([]grpc.DialOption, error) {
	switch {
	case c.AuthKeyID != "" && c.AuthKeyFile != "":
		tp, err := NewTokenProviderFromFile(c.AuthKeyID, c.AuthKeyFile, c.AuthSubject)
		if err != nil {
			return nil, fmt.Errorf("load ledger auth key: %w", err)
		}

		return []grpc.DialOption{grpc.WithPerRPCCredentials(tp)}, nil
	case c.AuthKeyID != "" || c.AuthKeyFile != "":
		return nil, fmt.Errorf("ledger auth: both key-id and key-file must be set to enable request signing")
	default:
		return nil, nil
	}
}
