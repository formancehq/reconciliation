package migrations

import (
	"context"

	"github.com/formancehq/go-libs/migrations"
	"github.com/uptrace/bun"
)

func Migrate(ctx context.Context, db *bun.DB) error {
	migrator := migrations.NewMigrator()
	registerMigrations(migrator)

	return migrator.Up(ctx, db)
}

func registerMigrations(migrator *migrations.Migrator) {
	migrator.RegisterMigrations(
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					CREATE SCHEMA IF NOT EXISTS reconciliations;

					CREATE TABLE IF NOT EXISTS reconciliations.policy (
						id uuid NOT NULL,
						created_at timestamp with time zone NOT NULL,
						name text NOT NULL,
						ledger_name text NOT NULL,
						ledger_query jsonb NOT NULL,
						payments_pool_id uuid NOT NULL,
						CONSTRAINT policy_pk PRIMARY KEY (id)
					);

					CREATE TABLE IF NOT EXISTS reconciliations.reconciliation (
						id uuid NOT NULL,
						policy_id uuid NOT NULL,
						created_at timestamp with time zone NOT NULL UNIQUE,
						reconciled_at timestamp with time zone,
						status text NOT NULL,
						ledger_balances jsonb NOT NULL,
						payments_balances jsonb NOT NULL,
						error text,
					   	CONSTRAINT reconciliation_pk PRIMARY KEY (id)
					);

					ALTER TABLE reconciliations.reconciliation DROP CONSTRAINT IF EXISTS reconciliation_policy_fk;
					ALTER TABLE reconciliations.reconciliation ADD CONSTRAINT reconciliation_policy_fk
					FOREIGN KEY (policy_id)
					REFERENCES reconciliations.policy (id)
					ON DELETE CASCADE
					NOT DEFERRABLE
					INITIALLY IMMEDIATE
					;
				`)
				return err
			},
		},
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					ALTER TABLE reconciliations.reconciliation RENAME COLUMN reconciled_at TO reconciled_at_ledger;
					ALTER TABLE reconciliations.reconciliation ADD COLUMN reconciled_at_payments timestamp with time zone;
				`)
				return err
			},
		},
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					ALTER TABLE reconciliations.reconciliation ADD COLUMN drift_balances jsonb;
				`)
				return err
			},
		},
		// V1: Ledger Clarity tables (Rule, Evaluation, Incident).
		// Additive only — legacy reconciliations.policy and reconciliations.reconciliation
		// are preserved verbatim and remain the storage for the legacy /policies API facade.
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					-- Shared trigger to touch updated_at on every UPDATE.
					CREATE OR REPLACE FUNCTION reconciliations.touch_updated_at() RETURNS trigger AS $$
					BEGIN
						NEW.updated_at = now();
						RETURN NEW;
					END;
					$$ LANGUAGE plpgsql;

					-- Rule: customer-facing entity. Template kind + spec drive evaluation;
					-- compiled_cel is persisted for explainability and post-GA power mode.
					CREATE TABLE IF NOT EXISTS reconciliations.rule (
						id              uuid NOT NULL,
						name            text NOT NULL,
						template_kind   text NOT NULL,
						template_spec   jsonb NOT NULL,
						compiled_cel    text NOT NULL DEFAULT '',
						enabled         boolean NOT NULL DEFAULT true,
						severity        text NOT NULL DEFAULT 'medium',
						schedule        jsonb,
						notifications   jsonb,
						labels          jsonb,
						created_at      timestamp with time zone NOT NULL DEFAULT now(),
						updated_at      timestamp with time zone NOT NULL DEFAULT now(),
						CONSTRAINT rule_pk PRIMARY KEY (id),
						CONSTRAINT rule_severity_chk CHECK (severity IN ('info','low','medium','high','critical'))
					);
					CREATE INDEX IF NOT EXISTS rule_name_idx    ON reconciliations.rule (name);
					CREATE INDEX IF NOT EXISTS rule_enabled_idx ON reconciliations.rule (enabled) WHERE enabled;
					DROP TRIGGER IF EXISTS rule_touch_updated_at ON reconciliations.rule;
					CREATE TRIGGER rule_touch_updated_at BEFORE UPDATE ON reconciliations.rule
						FOR EACH ROW EXECUTE FUNCTION reconciliations.touch_updated_at();

					-- Evaluation: one row per rule execution. Always persisted (PASS / FAIL / ERROR)
					-- for audit. pit_per_source records the PIT each Source resolver actually used.
					CREATE TABLE IF NOT EXISTS reconciliations.evaluation (
						id              uuid NOT NULL,
						rule_id         uuid NOT NULL,
						started_at      timestamp with time zone NOT NULL,
						ended_at        timestamp with time zone NOT NULL,
						pit_per_source  jsonb NOT NULL DEFAULT '{}'::jsonb,
						result          text NOT NULL,
						evidence        jsonb,
						error           text,
						cost_units      bigint NOT NULL DEFAULT 0,
						created_at      timestamp with time zone NOT NULL DEFAULT now(),
						CONSTRAINT evaluation_pk PRIMARY KEY (id),
						CONSTRAINT evaluation_result_chk CHECK (result IN ('PASS','FAIL','ERROR')),
						CONSTRAINT evaluation_rule_fk FOREIGN KEY (rule_id)
							REFERENCES reconciliations.rule(id) ON DELETE CASCADE
					);
					CREATE INDEX IF NOT EXISTS evaluation_rule_id_idx ON reconciliations.evaluation (rule_id, created_at DESC);
					CREATE INDEX IF NOT EXISTS evaluation_result_idx  ON reconciliations.evaluation (result);

					-- Incident: stateful, fingerprint-dedup'd record of a failing rule.
					-- Active = OPEN or ACKNOWLEDGED; a partial unique index enforces at most one
					-- active incident per (rule_id, fingerprint). Re-opens are new rows with
					-- parent_incident_id pointing at the prior closed incident.
					CREATE TABLE IF NOT EXISTS reconciliations.incident (
						id                  uuid NOT NULL,
						rule_id             uuid NOT NULL,
						fingerprint         text NOT NULL,
						status              text NOT NULL DEFAULT 'OPEN',
						severity            text NOT NULL,
						opened_at           timestamp with time zone NOT NULL,
						last_seen_at        timestamp with time zone NOT NULL,
						occurrence_count    integer NOT NULL DEFAULT 1,
						first_evaluation_id uuid NOT NULL,
						last_evaluation_id  uuid NOT NULL,
						evidence            jsonb,
						ack                 jsonb,
						resolution          jsonb,
						parent_incident_id  uuid,
						labels              jsonb,
						created_at          timestamp with time zone NOT NULL DEFAULT now(),
						updated_at          timestamp with time zone NOT NULL DEFAULT now(),
						CONSTRAINT incident_pk PRIMARY KEY (id),
						CONSTRAINT incident_status_chk   CHECK (status IN ('OPEN','ACKNOWLEDGED','RESOLVED')),
						CONSTRAINT incident_severity_chk CHECK (severity IN ('info','low','medium','high','critical')),
						CONSTRAINT incident_rule_fk       FOREIGN KEY (rule_id)             REFERENCES reconciliations.rule(id)       ON DELETE CASCADE,
						CONSTRAINT incident_first_eval_fk FOREIGN KEY (first_evaluation_id) REFERENCES reconciliations.evaluation(id),
						CONSTRAINT incident_last_eval_fk  FOREIGN KEY (last_evaluation_id)  REFERENCES reconciliations.evaluation(id),
						CONSTRAINT incident_parent_fk     FOREIGN KEY (parent_incident_id)  REFERENCES reconciliations.incident(id)
					);
					CREATE INDEX IF NOT EXISTS incident_rule_id_idx     ON reconciliations.incident (rule_id, opened_at DESC);
					CREATE INDEX IF NOT EXISTS incident_status_open_idx ON reconciliations.incident (opened_at DESC)
						WHERE status IN ('OPEN','ACKNOWLEDGED');
					CREATE INDEX IF NOT EXISTS incident_fingerprint_idx ON reconciliations.incident (rule_id, fingerprint);
					CREATE UNIQUE INDEX IF NOT EXISTS incident_active_per_fingerprint
						ON reconciliations.incident (rule_id, fingerprint)
						WHERE status IN ('OPEN','ACKNOWLEDGED');
					DROP TRIGGER IF EXISTS incident_touch_updated_at ON reconciliations.incident;
					CREATE TRIGGER incident_touch_updated_at BEFORE UPDATE ON reconciliations.incident
						FOR EACH ROW EXECUTE FUNCTION reconciliations.touch_updated_at();
				`)
				return err
			},
		},
	)
}
