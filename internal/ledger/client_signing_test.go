package ledger

import (
	"context"
	"crypto/ed25519"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// signingServer records every ApplyRequest it serves and serves a configurable
// keystore, so a test can assert on the exact bytes a signed write puts on the
// wire — which is the only place the EN-1930 guarantee actually lives.
type signingServer struct {
	servicepb.UnimplementedBucketServiceServer

	applies []*servicepb.ApplyRequest

	// registered is the keystore ListSigningKeys serves.
	registered []*commonpb.SigningKey
	// applyErr, when set, is returned from Apply instead of a success.
	applyErr error
}

func (s *signingServer) Apply(_ context.Context, req *servicepb.ApplyRequest) (*servicepb.ApplyResponse, error) {
	s.applies = append(s.applies, req)
	if s.applyErr != nil {
		return nil, s.applyErr
	}

	return &servicepb.ApplyResponse{}, nil
}

func (s *signingServer) ListSigningKeys(_ *servicepb.ListSigningKeysRequest, stream grpc.ServerStreamingServer[commonpb.SigningKey]) error {
	for _, key := range s.registered {
		if err := stream.Send(key); err != nil {
			return err
		}
	}

	return nil
}

func (s *signingServer) lastApply(t *testing.T) *servicepb.ApplyRequest {
	t.Helper()
	require.NotEmpty(t, s.applies, "no Apply reached the server")

	return s.applies[len(s.applies)-1]
}

func signedClient(t *testing.T, srv *signingServer) (*Client, SigningKey) {
	t.Helper()

	key, err := GenerateSigningKey()
	require.NoError(t, err)

	client := dialBufconn(t, srv)
	client.UseSigningKey(&key)

	return client, key
}

// The audit-chain guarantee is that a third party holding only the public key
// can verify a log entry: ed25519.Verify(pub, sig.Payload, sig.Signature),
// where Payload is the exact serialized ApplyBatch. This reproduces that check
// end to end against the bytes the client actually transmitted — the crypto in
// signing.go being correct proves nothing if the client sends the wrong bytes,
// or sends them unsigned.
func TestSignedWriteIsVerifiableFromThePublicKeyAlone(t *testing.T) {
	srv := &signingServer{}
	client, key := signedClient(t, srv)

	require.NoError(t, client.CreateTransaction(context.Background(), CreateTransactionInput{
		Ledger:         "recon",
		ScriptName:     "activity",
		ScriptVersion:  "2.0.0",
		IdempotencyKey: "idem-1",
	}))

	signed := srv.lastApply(t).GetSigned()
	require.NotNil(t, signed, "a client with a signing key must send a SignedApplyBatch")
	require.Equal(t, key.ID, signed.GetKeyId())

	// Verify exactly as an external auditor would: public key, payload bytes,
	// signature. No client state involved.
	auditor := SigningKey{Public: key.Public}
	require.True(t, auditor.Verify(signed.GetPayload(), signed.GetSignature()),
		"signature does not verify over the transmitted payload")

	// ...and the signed bytes must be the real batch, not an empty or unrelated
	// one: a signature over the wrong payload verifies just as happily.
	var batch servicepb.ApplyBatch
	require.NoError(t, batch.UnmarshalVT(signed.GetPayload()))
	require.Equal(t, "idem-1", batch.GetIdempotencyKey())
	require.NotEmpty(t, batch.GetRequests(), "the signed payload carries no requests")
}

// A tampered payload must fail the same check — otherwise the assertion above
// would pass for any bytes at all and prove nothing.
func TestSignedWriteDoesNotVerifyAfterTampering(t *testing.T) {
	srv := &signingServer{}
	client, key := signedClient(t, srv)

	require.NoError(t, client.CreateTransaction(context.Background(), CreateTransactionInput{
		Ledger: "recon", ScriptName: "activity", ScriptVersion: "2.0.0", IdempotencyKey: "idem-1",
	}))

	signed := srv.lastApply(t).GetSigned()
	tampered := append([]byte(nil), signed.GetPayload()...)
	tampered[len(tampered)-1] ^= 0xFF

	auditor := SigningKey{Public: key.Public}
	require.False(t, auditor.Verify(tampered, signed.GetSignature()))
	require.False(t, auditor.Verify(signed.GetPayload(), make([]byte, ed25519.SignatureSize)))
}

func TestUnsignedClientSendsUnsignedBatch(t *testing.T) {
	srv := &signingServer{}
	client := dialBufconn(t, srv)

	require.NoError(t, client.CreateTransaction(context.Background(), CreateTransactionInput{
		Ledger: "recon", ScriptName: "activity", ScriptVersion: "2.0.0", IdempotencyKey: "idem-1",
	}))

	last := srv.lastApply(t)
	require.Nil(t, last.GetSigned())
	require.NotNil(t, last.GetUnsigned())
}

// RegisterSigningKey must bypass the signer even when one is configured: the
// ledger accepts an unsigned RegisterSigningKey only while its keystore is
// empty, and it cannot verify a batch signed by a key it does not yet know. A
// regression here signs the bootstrap registration and bricks first startup
// against a fresh cluster — which no unsigned-client test would catch.
func TestRegisterSigningKeyIsSentUnsignedEvenWhenSignerConfigured(t *testing.T) {
	srv := &signingServer{}
	client, key := signedClient(t, srv)

	require.NoError(t, client.RegisterSigningKey(context.Background(), &key))

	unsigned := srv.lastApply(t).GetUnsigned()
	require.NotNil(t, unsigned, "the bootstrap registration must not be signed")

	req := unsigned.GetRequests()[0].GetRegisterSigningKey()
	require.NotNil(t, req)
	require.Equal(t, key.ID, req.GetKeyId())
	require.Equal(t, []byte(key.Public), req.GetPublicKey())
}

// Re-registering on every boot has to be a no-op, and it has to be a no-op by
// checking first rather than by swallowing an error: once the keystore holds
// our key, an unsigned re-registration is rejected for a missing signature, not
// as AlreadyExists.
func TestRegisterConfiguredSigningKeySkipsAnAlreadyRegisteredKey(t *testing.T) {
	srv := &signingServer{}
	client, key := signedClient(t, srv)
	srv.registered = []*commonpb.SigningKey{{KeyId: key.ID, PublicKey: key.Public}}

	require.NoError(t, client.RegisterConfiguredSigningKey(context.Background()))
	require.Empty(t, srv.applies, "an already-registered key must not be re-registered")
}

func TestRegisterConfiguredSigningKeyRegistersAnAbsentKey(t *testing.T) {
	srv := &signingServer{}
	client, key := signedClient(t, srv)
	srv.registered = []*commonpb.SigningKey{{KeyId: "someone-else", PublicKey: key.Public}}

	require.NoError(t, client.RegisterConfiguredSigningKey(context.Background()))

	unsigned := srv.lastApply(t).GetUnsigned()
	require.NotNil(t, unsigned)
	require.Equal(t, key.ID, unsigned.GetRequests()[0].GetRegisterSigningKey().GetKeyId())
}

func TestRegisterConfiguredSigningKeyIsNoOpWithoutASigner(t *testing.T) {
	srv := &signingServer{}
	client := dialBufconn(t, srv)

	require.NoError(t, client.RegisterConfiguredSigningKey(context.Background()))
	require.Empty(t, srv.applies)
}

// A concurrent bootstrap can register the key between our check and our write,
// so AlreadyExists is the benign race, not a failure.
func TestRegisterSigningKeySwallowsAlreadyExists(t *testing.T) {
	srv := &signingServer{applyErr: status.Error(codes.AlreadyExists, "key already registered")}
	client, key := signedClient(t, srv)

	require.NoError(t, client.RegisterSigningKey(context.Background(), &key))
}

func TestListSigningKeysReturnsTheKeystore(t *testing.T) {
	srv := &signingServer{registered: []*commonpb.SigningKey{
		{KeyId: "root", PublicKey: []byte("pub-root")},
		{KeyId: "rotated", PublicKey: []byte("pub-rotated"), ParentKeyId: "root"},
	}}
	client := dialBufconn(t, srv)

	keys, err := client.ListSigningKeys(context.Background())
	require.NoError(t, err)
	require.Len(t, keys, 2)
	require.Equal(t, "root", keys[0].KeyID)
	require.Equal(t, "rotated", keys[1].KeyID)
	require.Equal(t, "root", keys[1].ParentKeyID, "rotation ancestry must survive the read")
}
