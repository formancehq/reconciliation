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
	stackURLFlag          = "stack-url"
	stackClientIDFlag     = "stack-client-id"
	stackClientSecretFlag = "stack-client-secret"
	listenFlag            = "listen"

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
