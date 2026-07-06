package cmd

import (
	"context"
	"time"

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

// orphanedCheckpointMaxAge bounds how old a registered checkpoint must be before
// the startup reaper treats it as a crash orphan. Far above the engine's
// MaxWallClock (30s) so a live evaluation's checkpoint is never reaped.
const orphanedCheckpointMaxAge = 15 * time.Minute

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
// transitions: lifecycle moves (open/ack/resolve/accept/auto-resolve) are
// COMMITTED_TRANSACTION (marker move + account_metadata), snooze/unsnooze are
// SAVED_METADATA/DELETED_METADATA. Consumers filter on event.ledger == control.
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

		// The evaluation checkpointer: acquire → release func over the ledger
		// client (ADR-002 §6). It probes the control ledger to confirm the fresh
		// checkpoint's read index is materialized before use (F32). Provided here,
		// not in the api layer, because the probe ledger is the control-ledger flag.
		fx.Provide(func(client *ledger.Client) service.Checkpointer {
			control := flagStr(cmd, ledgerControlNameFlag)
			return checkpointerFunc(func(ctx context.Context) (uint64, func(context.Context) error, error) {
				cp, err := client.AcquireCheckpoint(ctx, control)
				if err != nil {
					return 0, nil, err
				}
				return cp.ID, cp.Release, nil
			})
		}),

		// Provision the control-ledger (chart + metadata indexes + numscripts) at
		// startup, then reap any query checkpoints a prior crash orphaned (F26).
		// Idempotent: a restart against an existing ledger is a no-op.
		fx.Invoke(func(lc fx.Lifecycle, client *ledger.Client, logger v5log.Logger) {
			control := flagStr(cmd, ledgerControlNameFlag)
			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					prov := ledger.NewProvisioner(client, control, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
					if err := prov.Provision(ctx); err != nil {
						return err
					}

					// Age-thresholded so a live checkpoint (held ≪ threshold) is never
					// reaped; boot-time cleanup never fails startup.
					if n, err := client.ReapOrphanedCheckpoints(ctx, control, orphanedCheckpointMaxAge, time.Now()); err != nil {
						logger.Infof("checkpoint reaper: %s (continuing)", err)
					} else if n > 0 {
						logger.Infof("checkpoint reaper: released %d orphaned checkpoint(s)", n)
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

// checkpointerFunc adapts a plain function to service.Checkpointer.
type checkpointerFunc func(ctx context.Context) (uint64, func(context.Context) error, error)

func (f checkpointerFunc) AcquireCheckpoint(ctx context.Context) (uint64, func(context.Context) error, error) {
	return f(ctx)
}

func flagStr(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func flagBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}
