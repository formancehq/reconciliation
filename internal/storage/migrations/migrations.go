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
		// V1.1: parent_incident_id must reference an incident with the same
		// rule_id. The base table FK only guarantees the parent row exists,
		// not that it belongs to the same rule — leaving the schema open to
		// cross-rule re-open chains that don't make sense for the lifecycle.
		// A BEFORE-INSERT/UPDATE trigger enforces the invariant; a composite
		// FK would also work but requires a (id, rule_id) unique index that
		// duplicates the primary key.
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					CREATE OR REPLACE FUNCTION reconciliations.incident_parent_same_rule()
					RETURNS trigger AS $$
					DECLARE
						parent_rule uuid;
					BEGIN
						IF NEW.parent_incident_id IS NULL THEN
							RETURN NEW;
						END IF;
						SELECT rule_id INTO parent_rule
						FROM reconciliations.incident
						WHERE id = NEW.parent_incident_id;
						IF parent_rule IS NULL THEN
							RAISE EXCEPTION 'parent_incident_id % does not exist', NEW.parent_incident_id
								USING ERRCODE = '23503';
						END IF;
						IF parent_rule <> NEW.rule_id THEN
							RAISE EXCEPTION 'parent_incident_id % belongs to rule %, not %', NEW.parent_incident_id, parent_rule, NEW.rule_id
								USING ERRCODE = '23514';
						END IF;
						RETURN NEW;
					END;
					$$ LANGUAGE plpgsql;

					DROP TRIGGER IF EXISTS incident_parent_same_rule ON reconciliations.incident;
					CREATE TRIGGER incident_parent_same_rule
						BEFORE INSERT OR UPDATE OF parent_incident_id ON reconciliations.incident
						FOR EACH ROW EXECUTE FUNCTION reconciliations.incident_parent_same_rule();
				`)
				return err
			},
		},
		// V1 design pivot: incidents → alerts.
		//
		// The previous model treated each resolve→reopen cycle as a fresh
		// incident row chained via parent_incident_id. That produced a linked
		// list of "incident episodes" — useful for narrative ("this is a
		// re-open of #abc") but redundant for analytics, where every useful
		// query already GROUP BY's (rule_id, fingerprint).
		//
		// The new shape:
		//
		//   alert        — one stable row per (rule_id, fingerprint). Status
		//                  cycles in-place through OPEN/ACKNOWLEDGED/RESOLVED.
		//                  Carries the *current* evidence / ack / resolution
		//                  for fast UI reads. occurrence_count is the
		//                  lifetime count of FAIL events on this alert.
		//
		//   alert_event  — append-only log: one row per evaluation that
		//                  touched the alert, plus one row per manual
		//                  transition (ack / resolve / accept). Carries
		//                  prev_status / new_status so historical state
		//                  transitions can be reconstructed without a join.
		//
		// alert_event is the seed of a future Ledger-style audit log: today
		// it's append-only by convention (no UPDATE/DELETE in code paths),
		// future work can add a per-alert sequence number + prev_hash/hash
		// chain on top of the existing columns without breaking readers.
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					-- Tear down the incident model in its entirety. Pre-GA, no
					-- data preservation needed.
					DROP TABLE IF EXISTS reconciliations.incident CASCADE;
					DROP FUNCTION IF EXISTS reconciliations.incident_parent_same_rule();

					CREATE TABLE IF NOT EXISTS reconciliations.alert (
						id                  uuid NOT NULL,
						rule_id             uuid NOT NULL,
						fingerprint         text NOT NULL,
						status              text NOT NULL DEFAULT 'OPEN',
						severity            text NOT NULL,
						first_seen_at       timestamp with time zone NOT NULL,
						last_seen_at        timestamp with time zone NOT NULL,
						occurrence_count    bigint NOT NULL DEFAULT 1,
						last_evaluation_id  uuid NOT NULL,
						evidence            jsonb,
						ack                 jsonb,
						resolution          jsonb,
						labels              jsonb,
						created_at          timestamp with time zone NOT NULL DEFAULT now(),
						updated_at          timestamp with time zone NOT NULL DEFAULT now(),
						CONSTRAINT alert_pk            PRIMARY KEY (id),
						CONSTRAINT alert_unique_pair   UNIQUE (rule_id, fingerprint),
						CONSTRAINT alert_status_chk    CHECK (status IN ('OPEN','ACKNOWLEDGED','RESOLVED')),
						CONSTRAINT alert_severity_chk  CHECK (severity IN ('info','low','medium','high','critical')),
						CONSTRAINT alert_rule_fk       FOREIGN KEY (rule_id)            REFERENCES reconciliations.rule(id) ON DELETE CASCADE,
						CONSTRAINT alert_last_eval_fk  FOREIGN KEY (last_evaluation_id) REFERENCES reconciliations.evaluation(id)
					);
					CREATE INDEX IF NOT EXISTS alert_rule_id_idx   ON reconciliations.alert (rule_id, last_seen_at DESC);
					CREATE INDEX IF NOT EXISTS alert_status_idx    ON reconciliations.alert (status, last_seen_at DESC);
					DROP TRIGGER IF EXISTS alert_touch_updated_at ON reconciliations.alert;
					CREATE TRIGGER alert_touch_updated_at BEFORE UPDATE ON reconciliations.alert
						FOR EACH ROW EXECUTE FUNCTION reconciliations.touch_updated_at();

					-- Append-only event log. prev_status is NULL only for the
					-- inaugural event of an alert. type discriminates the
					-- payload shape (evidence / ack / resolution).
					CREATE TABLE IF NOT EXISTS reconciliations.alert_event (
						id              uuid NOT NULL,
						alert_id        uuid NOT NULL,
						evaluation_id   uuid,
						type            text NOT NULL,
						prev_status     text,
						new_status      text NOT NULL,
						payload         jsonb,
						at              timestamp with time zone NOT NULL,
						created_at      timestamp with time zone NOT NULL DEFAULT now(),
						CONSTRAINT alert_event_pk        PRIMARY KEY (id),
						CONSTRAINT alert_event_type_chk  CHECK (type IN ('fail','pass','ack','resolve','accept')),
						CONSTRAINT alert_event_prev_chk  CHECK (prev_status IS NULL OR prev_status IN ('OPEN','ACKNOWLEDGED','RESOLVED')),
						CONSTRAINT alert_event_new_chk   CHECK (new_status IN ('OPEN','ACKNOWLEDGED','RESOLVED')),
						CONSTRAINT alert_event_alert_fk  FOREIGN KEY (alert_id)      REFERENCES reconciliations.alert(id)      ON DELETE CASCADE,
						CONSTRAINT alert_event_eval_fk   FOREIGN KEY (evaluation_id) REFERENCES reconciliations.evaluation(id)
					);
					CREATE INDEX IF NOT EXISTS alert_event_alert_idx ON reconciliations.alert_event (alert_id, at DESC);
					CREATE INDEX IF NOT EXISTS alert_event_type_idx  ON reconciliations.alert_event (alert_id, type, at DESC);
				`)
				return err
			},
		},
	)
}
