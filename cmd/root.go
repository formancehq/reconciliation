package cmd

import (
	"github.com/formancehq/go-libs/service"
	"github.com/spf13/cobra"
)

var (
	ServiceName = "reconciliation"
	Version     = "develop"
	BuildDate   = "-"
	Commit      = "-"
)

const (
	listenFlag = "listen"
	uiURLFlag  = "ui-url"

	// Control-ledger storage (ledger-native — replaces Postgres). The transport
	// refuses an insecure connection unless --ledger-insecure is set (F2).
	ledgerAddressFlag       = "ledger-grpc-address"
	ledgerControlNameFlag   = "ledger-control-name"
	ledgerAuthKeyIDFlag     = "ledger-auth-key-id"
	ledgerAuthKeyFileFlag   = "ledger-auth-key-file"
	ledgerAuthSubjectFlag   = "ledger-auth-subject"
	ledgerTLSCAFlag         = "ledger-tls-ca-file"
	ledgerTLSCertFlag       = "ledger-tls-cert-file"
	ledgerTLSKeyFlag        = "ledger-tls-key-file"
	ledgerTLSServerNameFlag = "ledger-tls-server-name"
	ledgerTLSSkipVerifyFlag = "ledger-tls-insecure-skip-verify"
	ledgerInsecureFlag      = "ledger-insecure"

	// Audit content-signing (EN-1930): the Ed25519 seed reconciliation signs every
	// control-ledger write with, so the ledger stores an externally-verifiable
	// signature on each entry. Distinct from the ledger-auth JWT above, which
	// authenticates the transport ("who is calling"); this signs the batch bytes
	// ("this content came from recon, unaltered"). Empty → a key is generated at
	// boot and its seed logged, so dev works out of the box; production pins it.
	auditSigningKeySeedFlag = "audit-signing-key-seed"

	// Event delivery (RFC §4.4): the ledger's HTTP webhook sink delivers alert
	// transition events; reconciliation runs no message bus. Unset URL → no sink.
	eventsSinkURLFlag    = "events-sink-url"
	eventsSinkSecretFlag = "events-sink-secret"
)

func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{}

	cobra.EnableTraverseRunHooks = true

	serveCmd := newServeCommand(Version)
	cmd.AddCommand(serveCmd)
	versionCmd := newVersionCommand()
	cmd.AddCommand(versionCmd)
	return cmd
}

func Execute() {
	service.Execute(NewRootCommand())
}
