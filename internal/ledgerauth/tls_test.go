package ledgerauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestCA(t *testing.T) (caPath, certPath, keyPath string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	require.NoError(t, err)

	dir := t.TempDir()
	caPath = filepath.Join(dir, "ca.crt")
	certPath = filepath.Join(dir, "tls.crt")
	keyPath = filepath.Join(dir, "tls.key")

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	require.NoError(t, os.WriteFile(caPath, caPEM, 0o600))
	require.NoError(t, os.WriteFile(certPath, caPEM, 0o600))

	keyDER, err := x509.MarshalECPrivateKey(priv)
	require.NoError(t, err)

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	return caPath, certPath, keyPath
}

func TestTLSConfig_IsZero(t *testing.T) {
	assert.True(t, TLSConfig{}.IsZero())
	assert.False(t, TLSConfig{CAFile: "/x"}.IsZero())
	assert.False(t, TLSConfig{InsecureSkipVerify: true}.IsZero())
	assert.False(t, TLSConfig{ServerName: "x"}.IsZero())
}

func TestTLSConfig_Build_CAOnly(t *testing.T) {
	caPath, _, _ := writeTestCA(t)

	creds, err := TLSConfig{CAFile: caPath, ServerName: "test"}.Build()
	require.NoError(t, err)
	assert.NotNil(t, creds)
	assert.Equal(t, "tls", creds.Info().SecurityProtocol)
}

func TestTLSConfig_Build_InsecureSkipVerify(t *testing.T) {
	creds, err := TLSConfig{InsecureSkipVerify: true}.Build()
	require.NoError(t, err)
	assert.NotNil(t, creds)
}

func TestTLSConfig_Build_MTLS(t *testing.T) {
	caPath, certPath, keyPath := writeTestCA(t)

	creds, err := TLSConfig{
		CAFile:   caPath,
		CertFile: certPath,
		KeyFile:  keyPath,
	}.Build()
	require.NoError(t, err)
	assert.NotNil(t, creds)
}

func TestTLSConfig_Build_MissingCA(t *testing.T) {
	_, err := TLSConfig{CAFile: "/does/not/exist"}.Build()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read CA file")
}

func TestTLSConfig_Build_InvalidCAPEM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.crt")
	require.NoError(t, os.WriteFile(path, []byte("not a pem"), 0o600))

	_, err := TLSConfig{CAFile: path}.Build()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid certificates")
}

func TestTLSConfig_Build_MTLSMissingKey(t *testing.T) {
	_, err := TLSConfig{CertFile: "/x"}.Build()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both cert-file and key-file")
}

func TestTLSConfig_Build_MTLSMissingCert(t *testing.T) {
	_, err := TLSConfig{KeyFile: "/x"}.Build()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both cert-file and key-file")
}
