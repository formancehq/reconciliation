package cmd

import (
	"github.com/formancehq/go-libs/bun/bunmigrate"
	"github.com/formancehq/go-libs/service"
	"github.com/formancehq/reconciliation/internal/storage"
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

	// auditChainPepperFlag supplies the out-of-band half of the journal's hash
	// key. Without it the key derives from a database-resident salt alone, so
	// anyone with full database access could in principle rebuild the chain;
	// with it, a stolen dump is not enough.
	auditChainPepperFlag = "audit-chain-pepper"
	// auditSigningKeySeedFlag supplies the Ed25519 seed that signs period seals.
	// Supplying it here means the private key never touches the database.
	auditSigningKeySeedFlag = "audit-signing-key-seed"
)

// addAuditChainFlags registers the journal's key material on any command that
// opens the database.
func addAuditChainFlags(cmd *cobra.Command) {
	cmd.Flags().String(auditChainPepperFlag, "",
		"Out-of-band secret mixed into the audit journal's hash key. Strongly recommended: without it, database access alone is enough to rebuild the chain. Changing it after first boot is fatal.")
	cmd.Flags().String(auditSigningKeySeedFlag, "",
		"Ed25519 seed (base64 or hex) signing period seals, so auditors can verify them without trusting this service. Generated and stored in the database when unset.")
}

func auditChainSettings(cmd *cobra.Command) storage.AuditChainSettings {
	pepper, _ := cmd.Flags().GetString(auditChainPepperFlag)
	seed, _ := cmd.Flags().GetString(auditSigningKeySeedFlag)
	return storage.AuditChainSettings{Pepper: pepper, SigningKeySeed: seed}
}

func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{}

	cobra.EnableTraverseRunHooks = true

	serveCmd := newServeCommand(Version)
	addAutoMigrateCommand(serveCmd)
	cmd.AddCommand(serveCmd)
	cmd.AddCommand(newWorkerCommand(Version))
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
