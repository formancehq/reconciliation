package cmd

import (
	"github.com/formancehq/go-libs/bun/bunmigrate"
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
)

func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{}

	cobra.EnableTraverseRunHooks = true

	serveCmd := newServeCommand(Version)
	addAutoMigrateCommand(serveCmd)
	cmd.AddCommand(serveCmd)
	versionCmd := newVersionCommand()
	cmd.AddCommand(versionCmd)
	migrate := newMigrate()
	cmd.AddCommand(migrate)
	return cmd
}

func Execute() {
	service.Execute(NewRootCommand())
}

func addAutoMigrateCommand(cmd *cobra.Command) {
	cmd.Flags().Bool(autoMigrateFlag, false, "Auto migrate database")
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		autoMigrate, _ := cmd.Flags().GetBool(autoMigrateFlag)
		if autoMigrate {
			return bunmigrate.Run(cmd, args, Migrate)
		}
		return nil
	}
}
