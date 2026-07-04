package cmd

import (
	"context"

	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerauth"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/ledgerstore"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.uber.org/fx"
)

// The control-ledger LedgerStore is the sole storage backend — assert it
// satisfies the service contract at the wiring seam (keeps the ledgerstore
// package from depending on the service layer).
var _ service.Store = (*ledgerstore.LedgerStore)(nil)

// addLedgerFlags registers the control-ledger transport + naming flags.
func addLedgerFlags(flags *pflag.FlagSet) {
	flags.String(ledgerAddressFlag, "127.0.0.1:8888", "Ledger v3 gRPC address (control-ledger + data-ledger reads)")
	flags.String(ledgerControlNameFlag, ledgerschema.DefaultControlLedger, "Name of the control-ledger holding reconciliation state")
	flags.String(ledgerAuthKeyIDFlag, "", "Ed25519 signing key ID (JWT kid) for ledger request signing")
	flags.String(ledgerAuthKeyFileFlag, "", "Path to the Ed25519 seed file for ledger request signing")
	flags.String(ledgerAuthSubjectFlag, "reconciliation", "JWT subject for ledger request signing")
	flags.String(ledgerTLSCAFlag, "", "PEM CA bundle to verify the ledger server certificate")
	flags.String(ledgerTLSCertFlag, "", "PEM client certificate for mTLS to the ledger")
	flags.String(ledgerTLSKeyFlag, "", "PEM client key for mTLS to the ledger")
	flags.String(ledgerTLSServerNameFlag, "", "Override the TLS server name for the ledger handshake")
	flags.Bool(ledgerTLSSkipVerifyFlag, false, "Disable ledger TLS certificate verification (dev only)")
	flags.Bool(ledgerInsecureFlag, false, "Allow a plaintext, unauthenticated ledger connection (local/dev only)")
}

// ledgerClientModule wires the ledger gRPC client (secure transport per F2),
// binds the LedgerStore as the sole service.Store, and provisions the
// control-ledger at startup.
func ledgerClientModule(cmd *cobra.Command) fx.Option {
	return fx.Options(
		fx.Provide(func(lc fx.Lifecycle) (*ledger.Client, error) {
			cfg := ledgerauth.Config{
				Address:       flagStr(cmd, ledgerAddressFlag),
				AllowInsecure: flagBool(cmd, ledgerInsecureFlag),
				AuthKeyID:     flagStr(cmd, ledgerAuthKeyIDFlag),
				AuthKeyFile:   flagStr(cmd, ledgerAuthKeyFileFlag),
				AuthSubject:   flagStr(cmd, ledgerAuthSubjectFlag),
				TLS: ledgerauth.TLSConfig{
					CAFile:             flagStr(cmd, ledgerTLSCAFlag),
					CertFile:           flagStr(cmd, ledgerTLSCertFlag),
					KeyFile:            flagStr(cmd, ledgerTLSKeyFlag),
					ServerName:         flagStr(cmd, ledgerTLSServerNameFlag),
					InsecureSkipVerify: flagBool(cmd, ledgerTLSSkipVerifyFlag),
				},
			}

			creds, dialOpts, err := cfg.Build()
			if err != nil {
				return nil, err
			}

			client, err := ledger.NewClient(cfg.Address, creds, dialOpts...)
			if err != nil {
				return nil, err
			}

			lc.Append(fx.Hook{OnStop: func(context.Context) error { return client.Close() }})
			return client, nil
		}),

		fx.Provide(func(client *ledger.Client) service.Store {
			return ledgerstore.New(client, flagStr(cmd, ledgerControlNameFlag))
		}),

		// Provision the control-ledger (chart + metadata indexes + numscripts) at
		// startup. Idempotent: a restart against an existing ledger is a no-op.
		fx.Invoke(func(lc fx.Lifecycle, client *ledger.Client) {
			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					prov := ledger.NewProvisioner(
						client,
						flagStr(cmd, ledgerControlNameFlag),
						commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT,
					)
					return prov.Provision(ctx)
				},
			})
		}),
	)
}

func flagStr(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func flagBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}
