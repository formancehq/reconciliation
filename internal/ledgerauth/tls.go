package ledgerauth

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
)

// TLSConfig holds the TLS configuration for the ledger gRPC connection.
type TLSConfig struct {
	// CAFile is the path to a PEM-encoded CA bundle used to verify the
	// ledger's server certificate. If empty, the system trust store is used.
	CAFile string

	// CertFile and KeyFile are paths to a PEM-encoded client certificate and
	// private key for mutual TLS (mTLS). Both must be set together.
	CertFile string
	KeyFile  string

	// InsecureSkipVerify disables verification of the server certificate.
	// Use only for development.
	InsecureSkipVerify bool

	// ServerName overrides the hostname used during the TLS handshake.
	// Useful when the dial address has a trailing dot or differs from the
	// certificate's SAN entries.
	ServerName string
}

// IsZero reports whether the config is empty (no TLS material at all).
func (c TLSConfig) IsZero() bool {
	return c.CAFile == "" &&
		c.CertFile == "" &&
		c.KeyFile == "" &&
		!c.InsecureSkipVerify &&
		c.ServerName == ""
}

// Build constructs gRPC transport credentials from the config.
func (c TLSConfig) Build() (credentials.TransportCredentials, error) {
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: c.InsecureSkipVerify, //nolint:gosec // intentional, gated by flag/CRD
		ServerName:         c.ServerName,
	}

	if c.CAFile != "" {
		caPEM, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file %q: %w", c.CAFile, err)
		}

		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("no valid certificates found in CA file %q", c.CAFile)
		}

		tlsCfg.RootCAs = pool
	}

	if c.CertFile != "" || c.KeyFile != "" {
		if c.CertFile == "" || c.KeyFile == "" {
			return nil, fmt.Errorf("both cert-file and key-file must be set for mTLS")
		}

		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}

		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return credentials.NewTLS(tlsCfg), nil
}
