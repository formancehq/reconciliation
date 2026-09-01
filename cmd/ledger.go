package cmd

import (
	"context"

	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"
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
	flags.String(eventsSinkURLFlag, "", "HTTP webhook URL for the ledger events sink (alert transition delivery); empty disables the sink")
	flags.String(eventsSinkSecretFlag, "", "Optional HMAC-SHA256 secret for the events sink X-Webhook-Signature header")
}

// reconciliationEventsSinkName is the stable name of the webhook sink recon
// provisions. Per-sink cursor/status key off the name, so it must be constant
// across boots (AddEventsSink is add-or-update).
const reconciliationEventsSinkName = "reconciliation"

// reconciliationSinkEventTypes are the ledger event types that carry alert
// transitions. Current lifecycle and snooze/unsnooze writes are
// COMMITTED_TRANSACTION events because they append activity atomically. Metadata
// events remain subscribed for compatibility with older or direct writers.
func reconciliationSinkEventTypes() []commonpb.EventType {
	return []commonpb.EventType{
		commonpb.EventType_COMMITTED_TRANSACTION,
		commonpb.EventType_SAVED_METADATA,
		commonpb.EventType_DELETED_METADATA,
	}
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
		fx.Invoke(func(lc fx.Lifecycle, client *ledger.Client, logger v5log.Logger) {
			control := flagStr(cmd, ledgerControlNameFlag)
			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					prov := ledger.NewProvisioner(client, control, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
					if err := prov.Provision(ctx); err != nil {
						return err
					}

					// Register the alert-transition delivery sink (RFC §4.4), when
					// configured. Idempotent add-or-update; the sink delivers the
					// self-describing transition events (ED-1) to the webhook.
					if url := flagStr(cmd, eventsSinkURLFlag); url != "" {
						if err := client.AddEventsSink(ctx, ledger.EventSinkConfig{
							Name:       reconciliationEventsSinkName,
							Endpoint:   url,
							Secret:     flagStr(cmd, eventsSinkSecretFlag),
							EventTypes: reconciliationSinkEventTypes(),
						}); err != nil {
							return err // a misconfigured sink should fail boot loudly
						}

						logger.Infof("events sink %q → %s registered", reconciliationEventsSinkName, url)
					}

					return nil
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
