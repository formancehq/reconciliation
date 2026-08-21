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

func IsUpToDate(ctx context.Context, db *bun.DB) (bool, error) {
	migrator := migrations.NewMigrator()
	registerMigrations(migrator)

	return migrator.IsUpToDate(ctx, db)
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
		// Merged from main (#72): drop the created_at UNIQUE on
		// reconciliations.reconciliation — created_at is not a business
		// invariant, two reconciliations in the same microsecond must not
		// collide on insert. Kept at THIS position (migration version 4) so it
		// matches deployments that already applied it on main before the V1
		// migrations below; the index-based migrator runs everything after it next.
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					ALTER TABLE reconciliations.reconciliation DROP CONSTRAINT IF EXISTS reconciliation_created_at_key;
					CREATE INDEX IF NOT EXISTS reconciliation_created_at_idx ON reconciliations.reconciliation (created_at);
					CREATE INDEX IF NOT EXISTS reconciliation_policy_id_idx ON reconciliations.reconciliation (policy_id);
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

					-- Rule: customer-facing entity. Template kind + spec drive evaluation.
					-- compiled_cel is the historical name of the representative explanation;
					-- a later migration renames it to explanation_cel.
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
						-- Current operator snooze (until/by/at/note). A snoozed alert keeps
						-- failing and keeps counting against period-green; only its
						-- notifications are muted. See docs/technical/notification-suppression.md.
						snooze              jsonb,
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
						-- notify is the per-row notification decision: false marks a
						-- transition kept for audit but not published (a repeated identical
						-- fail, or a fail muted by an active snooze). Default true preserves
						-- "every transition notifies" for all but those suppressed cases.
						notify          boolean NOT NULL DEFAULT true,
						at              timestamp with time zone NOT NULL,
						created_at      timestamp with time zone NOT NULL DEFAULT now(),
						CONSTRAINT alert_event_pk        PRIMARY KEY (id),
						CONSTRAINT alert_event_type_chk  CHECK (type IN ('fail','pass','ack','resolve','accept','snooze','unsnooze')),
						CONSTRAINT alert_event_prev_chk  CHECK (prev_status IS NULL OR prev_status IN ('OPEN','ACKNOWLEDGED','RESOLVED')),
						CONSTRAINT alert_event_new_chk   CHECK (new_status IN ('OPEN','ACKNOWLEDGED','RESOLVED')),
						CONSTRAINT alert_event_alert_fk  FOREIGN KEY (alert_id)      REFERENCES reconciliations.alert(id)      ON DELETE CASCADE,
						-- ON DELETE CASCADE so deleting a rule (which cascades to its
						-- evaluations) doesn't trip this FK from the surviving event
						-- rows. Without it, no rule that ever fired an alert can be
						-- deleted: the rule→evaluation cascade collides with the
						-- alert_event→evaluation reference. The alert→alert_event
						-- cascade above already removes these rows on the alert path;
						-- this covers the evaluation path too.
						CONSTRAINT alert_event_eval_fk   FOREIGN KEY (evaluation_id) REFERENCES reconciliations.evaluation(id) ON DELETE CASCADE
					);
					CREATE INDEX IF NOT EXISTS alert_event_alert_idx ON reconciliations.alert_event (alert_id, at DESC);
					CREATE INDEX IF NOT EXISTS alert_event_type_idx  ON reconciliations.alert_event (alert_id, type, at DESC);
				`)
				return err
			},
		},
		// V1: period-scoped alert identity.
		//
		// Reconciliation is periodic: "March reconciled" is a permanent claim
		// about a bounded accounting period. An immortal (rule_id, fingerprint)
		// alert that resolves and reopens forever lets a later green state mask
		// that an earlier period had an open case — the books look clean during
		// an audit, then the discrepancy resurfaces (cf. Wirecard). It also
		// conflates genuinely distinct incidents (a March break and an
		// unrelated November break of the same asset) into one timeline.
		//
		// Fix: scope the dedup identity by period. A rule declares a `cadence`
		// (continuous | daily | monthly); each failing evaluation derives a
		// `period_id` from (cadence, evaluation PIT). The alert's identity
		// becomes (rule_id, fingerprint, period_id), so:
		//   - a new period opens a *fresh* case (new id) instead of reopening a
		//     prior period's — past periods are immutable historical record;
		//   - reopen-in-place is bounded to within a period (a flap window);
		//   - `continuous` keeps the original single-scope behaviour for live
		//     monitoring (period_id = 'continuous'), which is also the default,
		//     so this migration is behaviour-preserving for existing rows.
		//
		// See docs/technical/alert-period-model.md for the full reasoning.
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					-- Rule cadence drives period derivation. Default 'continuous'
					-- preserves prior behaviour; periodic scoping is opt-in.
					ALTER TABLE reconciliations.rule
						ADD COLUMN IF NOT EXISTS cadence text NOT NULL DEFAULT 'continuous';
					ALTER TABLE reconciliations.rule DROP CONSTRAINT IF EXISTS rule_cadence_chk;
					ALTER TABLE reconciliations.rule ADD CONSTRAINT rule_cadence_chk
						CHECK (cadence IN ('continuous','daily','monthly'));

					-- Period the alert belongs to. Existing rows default to the
					-- 'continuous' scope, under which (rule_id, fingerprint,
					-- 'continuous') is equivalent to the old (rule_id, fingerprint).
					ALTER TABLE reconciliations.alert
						ADD COLUMN IF NOT EXISTS period_id text NOT NULL DEFAULT 'continuous';

					-- Identity is now the triple. Swap the unique constraint.
					ALTER TABLE reconciliations.alert DROP CONSTRAINT IF EXISTS alert_unique_pair;
					ALTER TABLE reconciliations.alert DROP CONSTRAINT IF EXISTS alert_unique_scope;
					ALTER TABLE reconciliations.alert ADD CONSTRAINT alert_unique_scope
						UNIQUE (rule_id, fingerprint, period_id);

					-- Backs "is (rule, period) reconciled?" — i.e. are there any
					-- active (OPEN/ACKNOWLEDGED) alerts scoped to the period.
					CREATE INDEX IF NOT EXISTS alert_period_status_idx
						ON reconciliations.alert (rule_id, period_id, status);
				`)
				return err
			},
		},
		// V1: add 'weekly' to the rule cadence vocabulary (ISO-week period
		// buckets). Additive — existing rows are unaffected; only widens the
		// allowed set in the CHECK constraint.
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					ALTER TABLE reconciliations.rule DROP CONSTRAINT IF EXISTS rule_cadence_chk;
					ALTER TABLE reconciliations.rule ADD CONSTRAINT rule_cadence_chk
						CHECK (cadence IN ('continuous','daily','weekly','monthly'));
				`)
				return err
			},
		},
		// V3: durable, multi-replica-safe cron scheduling.
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					ALTER TABLE reconciliations.rule
						ADD COLUMN IF NOT EXISTS revision bigint NOT NULL DEFAULT 1,
						ADD COLUMN IF NOT EXISTS next_run_at timestamp with time zone;

					CREATE INDEX IF NOT EXISTS rule_schedule_due_idx
						ON reconciliations.rule (next_run_at, id)
						WHERE enabled AND next_run_at IS NOT NULL
						  AND schedule->>'kind' = 'cron';

					CREATE TABLE IF NOT EXISTS reconciliations.evaluation_job (
						id            uuid NOT NULL,
						rule_id       uuid NOT NULL,
						rule_revision bigint NOT NULL,
						scheduled_at  timestamp with time zone NOT NULL,
						status        text NOT NULL DEFAULT 'PENDING',
						attempts      integer NOT NULL DEFAULT 0,
						available_at  timestamp with time zone NOT NULL DEFAULT now(),
						lease_until   timestamp with time zone,
						claim_token   uuid,
						last_error    text,
						created_at    timestamp with time zone NOT NULL DEFAULT now(),
						updated_at    timestamp with time zone NOT NULL DEFAULT now(),
						CONSTRAINT evaluation_job_pk PRIMARY KEY (id),
						CONSTRAINT evaluation_job_rule_fk FOREIGN KEY (rule_id)
							REFERENCES reconciliations.rule(id) ON DELETE CASCADE,
						CONSTRAINT evaluation_job_occurrence_unique
							UNIQUE (rule_id, rule_revision, scheduled_at),
						CONSTRAINT evaluation_job_status_chk CHECK
							(status IN ('PENDING','RUNNING','SUCCEEDED','FAILED','CANCELLED')),
						CONSTRAINT evaluation_job_attempts_chk CHECK (attempts >= 0),
						CONSTRAINT evaluation_job_claim_chk CHECK (
							(status = 'RUNNING' AND claim_token IS NOT NULL AND lease_until IS NOT NULL) OR
							(status <> 'RUNNING' AND claim_token IS NULL AND lease_until IS NULL)
						)
					);
					CREATE INDEX IF NOT EXISTS evaluation_job_claim_idx
						ON reconciliations.evaluation_job (available_at, scheduled_at)
						WHERE status = 'PENDING';
					CREATE INDEX IF NOT EXISTS evaluation_job_expired_lease_idx
						ON reconciliations.evaluation_job (lease_until)
						WHERE status = 'RUNNING';
					CREATE INDEX IF NOT EXISTS evaluation_job_active_status_idx
						ON reconciliations.evaluation_job (status)
						WHERE status IN ('PENDING','RUNNING','FAILED');
					DROP TRIGGER IF EXISTS evaluation_job_touch_updated_at
						ON reconciliations.evaluation_job;
					CREATE TRIGGER evaluation_job_touch_updated_at
						BEFORE UPDATE ON reconciliations.evaluation_job
						FOR EACH ROW EXECUTE FUNCTION reconciliations.touch_updated_at();

					ALTER TABLE reconciliations.evaluation
						ADD COLUMN IF NOT EXISTS scheduled_at timestamp with time zone,
						ADD COLUMN IF NOT EXISTS rule_revision bigint;
					ALTER TABLE reconciliations.evaluation
						DROP CONSTRAINT IF EXISTS evaluation_schedule_identity_chk;
					ALTER TABLE reconciliations.evaluation
						ADD CONSTRAINT evaluation_schedule_identity_chk CHECK (
							(scheduled_at IS NULL AND rule_revision IS NULL) OR
							(scheduled_at IS NOT NULL AND rule_revision IS NOT NULL)
						);
					CREATE UNIQUE INDEX IF NOT EXISTS evaluation_scheduled_occurrence_unique
						ON reconciliations.evaluation (rule_id, rule_revision, scheduled_at)
						WHERE scheduled_at IS NOT NULL;
				`)
				return err
			},
		},
		// V3: make the persisted rule-level CEL contract explicit. The original
		// column name implied that this value was the complete runtime program,
		// while template_spec is the executable source of truth and this string is
		// only a representative explanation. Keep this as an additive migration so
		// environments that already exercised the pre-merge V1 migrations converge.
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					ALTER TABLE reconciliations.rule
						RENAME COLUMN compiled_cel TO explanation_cel;
				`)
				return err
			},
		},
		// Audit journal: a tamper-evident hash chain over every state-bearing
		// operation, the versioned control definitions the chain refers to, and
		// the period seals that close ranges of it.
		//
		// Three things this migration deliberately does NOT do:
		//
		//  1. It does not compute any hash in SQL. The Ledger V2 log put its
		//     hashing in a PL/pgSQL trigger that reproduced Go's json.Marshal
		//     output by hand; the two implementations drifted, which is why that
		//     repository carries a migration named "Fix hashing function". Here
		//     the encoding has one implementation, in internal/audit, and the
		//     database's only job is to refuse rewrites.
		//  2. It gives audit_entry no foreign keys. The journal must not depend
		//     on the continued existence of any row it describes — that is the
		//     whole point of a journal.
		//  3. It does not pretend REVOKE achieves immutability. A table's owner
		//     keeps implicit privileges no REVOKE can remove, so the trigger is
		//     the enforcement that always holds; running the service as a
		//     non-owner role and revoking UPDATE/DELETE is documented as an
		//     operator hardening step on top.
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					-- Singleton row holding the installation's chain key material.
					-- The salt is generated once, on first boot. An operator may
					-- additionally configure a pepper out-of-band; key_check then
					-- lets boot detect a missing or wrong one instead of silently
					-- appending entries under a key that cannot verify the
					-- existing chain.
					CREATE TABLE IF NOT EXISTS reconciliations.audit_chain_config (
						singleton            boolean NOT NULL DEFAULT true,
						salt                 bytea   NOT NULL,
						key_check            bytea   NOT NULL,
						has_pepper           boolean NOT NULL DEFAULT false,
						signing_key_id       text,
						signing_public_key   bytea,
						-- Present only when the service generated the key itself.
						-- An operator supplying the seed by configuration leaves
						-- this null, which is the recommended production setup.
						signing_private_seed bytea,
						created_at           timestamp with time zone NOT NULL DEFAULT now(),
						CONSTRAINT audit_chain_config_pk PRIMARY KEY (singleton),
						CONSTRAINT audit_chain_config_singleton_chk CHECK (singleton)
					);

					-- Every signing key this installation has ever used. Seals are
					-- immutable and each carries the id of the key that signed
					-- it, so a rotation must not orphan the seals that came
					-- before: an auditor asking "which key verifies this seal"
					-- gets an answer years later, from us, without having had to
					-- archive the key themselves.
					CREATE TABLE IF NOT EXISTS reconciliations.audit_signing_key (
						key_id     text  NOT NULL,
						public_key bytea NOT NULL,
						created_at timestamp with time zone NOT NULL DEFAULT now(),
						retired_at timestamp with time zone,
						CONSTRAINT audit_signing_key_pk PRIMARY KEY (key_id)
					);

					-- The chain. seq is physical insertion order and may skip
					-- values (a rolled-back transaction consumes a bigserial);
					-- sequence is the dense logical counter assigned under the
					-- chain advisory lock, and it is what makes a deleted entry
					-- detectable. Ledger V2's logs table splits the same two
					-- roles across (seq, id).
					CREATE TABLE IF NOT EXISTS reconciliations.audit_entry (
						seq            bigserial NOT NULL,
						sequence       bigint    NOT NULL,
						at             timestamp with time zone NOT NULL,
						kind           text      NOT NULL,
						rule_id        uuid,
						rule_revision  bigint,
						alert_id       uuid,
						evaluation_id  uuid,
						period_id      text,
						subject        jsonb     NOT NULL,
						memento        bytea     NOT NULL,
						memento_digest bytea     NOT NULL,
						prev_hash      bytea,
						hash           bytea     NOT NULL,
						hash_version   integer   NOT NULL DEFAULT 1,
						created_at     timestamp with time zone NOT NULL DEFAULT now(),
						CONSTRAINT audit_entry_pk PRIMARY KEY (seq),
						CONSTRAINT audit_entry_sequence_unique UNIQUE (sequence),
						CONSTRAINT audit_entry_sequence_positive_chk CHECK (sequence > 0),
						CONSTRAINT audit_entry_kind_chk CHECK (kind IN (
							'rule.created', 'rule.revised', 'rule.deleted',
							'evaluation.committed', 'alert.transition', 'period.sealed'
						))
					);

					CREATE INDEX IF NOT EXISTS audit_entry_kind_idx
						ON reconciliations.audit_entry (kind, sequence);
					CREATE INDEX IF NOT EXISTS audit_entry_rule_idx
						ON reconciliations.audit_entry (rule_id, sequence)
						WHERE rule_id IS NOT NULL;
					CREATE INDEX IF NOT EXISTS audit_entry_alert_idx
						ON reconciliations.audit_entry (alert_id, sequence)
						WHERE alert_id IS NOT NULL;
					CREATE INDEX IF NOT EXISTS audit_entry_evaluation_idx
						ON reconciliations.audit_entry (evaluation_id, sequence)
						WHERE evaluation_id IS NOT NULL;
					CREATE INDEX IF NOT EXISTS audit_entry_period_idx
						ON reconciliations.audit_entry (period_id, sequence)
						WHERE period_id IS NOT NULL;
					CREATE INDEX IF NOT EXISTS audit_entry_at_idx
						ON reconciliations.audit_entry (at, sequence);

					-- The enforcement that always holds, owner or not: the rows
					-- cannot be updated or deleted through SQL at all.
					CREATE OR REPLACE FUNCTION reconciliations.audit_entry_immutable()
						RETURNS trigger
						LANGUAGE plpgsql
					AS $$
					BEGIN
						RAISE EXCEPTION
							'reconciliations.audit_entry is append-only: % is not permitted', TG_OP
							USING ERRCODE = 'restrict_violation',
							      HINT = 'The audit journal is immutable by construction. Corrections are recorded as new entries.';
					END;
					$$;

					DROP TRIGGER IF EXISTS audit_entry_no_rewrite ON reconciliations.audit_entry;
					CREATE TRIGGER audit_entry_no_rewrite
						BEFORE UPDATE OR DELETE ON reconciliations.audit_entry
						FOR EACH ROW EXECUTE FUNCTION reconciliations.audit_entry_immutable();

					-- Defence in depth: only meaningful when the service connects
					-- as a role that does not own the table, which is the
					-- documented production posture.
					REVOKE UPDATE, DELETE, TRUNCATE ON reconciliations.audit_entry FROM PUBLIC;

					-- Immutable snapshots of control definitions. Without these
					-- the chain proves a verdict but not what produced it,
					-- because the rule row only ever holds the latest spec.
					CREATE TABLE IF NOT EXISTS reconciliations.rule_revision (
						rule_id         uuid    NOT NULL,
						revision        bigint  NOT NULL,
						name            text    NOT NULL,
						template_kind   text    NOT NULL,
						template_spec   jsonb   NOT NULL,
						explanation_cel text,
						severity        text    NOT NULL,
						cadence         text    NOT NULL,
						enabled         boolean NOT NULL,
						schedule        jsonb,
						notifications   jsonb,
						labels          jsonb,
						audit_sequence  bigint  NOT NULL,
						created_at      timestamp with time zone NOT NULL DEFAULT now(),
						CONSTRAINT rule_revision_pk PRIMARY KEY (rule_id, revision)
					);

					DROP TRIGGER IF EXISTS rule_revision_no_rewrite ON reconciliations.rule_revision;
					CREATE TRIGGER rule_revision_no_rewrite
						BEFORE UPDATE OR DELETE ON reconciliations.rule_revision
						FOR EACH ROW EXECUTE FUNCTION reconciliations.audit_entry_immutable();

					-- Period seals partition the chain: first_sequence continues
					-- from the previous seal's last_sequence + 1, with no gap and
					-- no overlap, exactly as a Ledger V3 chapter closes at an
					-- audit-sequence boundary rather than by filtering content.
					CREATE TABLE IF NOT EXISTS reconciliations.period_seal (
						period_id        text   NOT NULL,
						first_sequence   bigint NOT NULL,
						last_sequence    bigint NOT NULL,
						entry_count      bigint NOT NULL,
						last_audit_hash  bytea,
						state_hash       bytea  NOT NULL,
						sealing_hash     bytea  NOT NULL,
						signature        bytea,
						signing_key_id   text,
						sealed_by        jsonb  NOT NULL,
						alert_count      bigint NOT NULL DEFAULT 0,
						unresolved_count bigint NOT NULL DEFAULT 0,
						sealed_at        timestamp with time zone NOT NULL DEFAULT now(),
						audit_sequence   bigint NOT NULL,
						CONSTRAINT period_seal_pk PRIMARY KEY (period_id),
						CONSTRAINT period_seal_range_chk CHECK (last_sequence >= first_sequence - 1)
					);

					CREATE INDEX IF NOT EXISTS period_seal_last_sequence_idx
						ON reconciliations.period_seal (last_sequence);

					DROP TRIGGER IF EXISTS period_seal_no_rewrite ON reconciliations.period_seal;
					CREATE TRIGGER period_seal_no_rewrite
						BEFORE UPDATE OR DELETE ON reconciliations.period_seal
						FOR EACH ROW EXECUTE FUNCTION reconciliations.audit_entry_immutable();

					-- Deleting a rule used to cascade away every evaluation,
					-- alert and transition it had produced. With a chain that is
					-- worse than before: the journal would reference rows that no
					-- longer exist. Deletion becomes a tombstone plus a chain
					-- entry, and the audit-bearing children are pinned by
					-- RESTRICT so no future code path can reintroduce the cascade.
					ALTER TABLE reconciliations.rule
						ADD COLUMN IF NOT EXISTS deleted_at timestamp with time zone;

					CREATE INDEX IF NOT EXISTS rule_live_idx
						ON reconciliations.rule (id) WHERE deleted_at IS NULL;

					ALTER TABLE reconciliations.evaluation DROP CONSTRAINT IF EXISTS evaluation_rule_fk;
					ALTER TABLE reconciliations.evaluation ADD CONSTRAINT evaluation_rule_fk
						FOREIGN KEY (rule_id) REFERENCES reconciliations.rule (id) ON DELETE RESTRICT;

					ALTER TABLE reconciliations.alert DROP CONSTRAINT IF EXISTS alert_rule_fk;
					ALTER TABLE reconciliations.alert ADD CONSTRAINT alert_rule_fk
						FOREIGN KEY (rule_id) REFERENCES reconciliations.rule (id) ON DELETE RESTRICT;

					-- Link the existing read models to the chain. Nullable
					-- because rows written before this migration have no entry —
					-- an honest null beats a fabricated sequence number.
					ALTER TABLE reconciliations.evaluation
						ADD COLUMN IF NOT EXISTS audit_sequence bigint;

					-- Evaluations gain the period they belong to. Previously only
					-- alerts carried one, so "the evidence for May" had to be
					-- reached through the alerts of May — which misses every
					-- passing check, i.e. most of what proves the controls ran.
					-- Storing it also removes an ambiguity that would otherwise
					-- be baked into the journal: the runner scopes alerts by the
					-- point-in-time it read, not by the wall-clock end of the
					-- evaluation, and those two can straddle a period boundary.
					ALTER TABLE reconciliations.evaluation
						ADD COLUMN IF NOT EXISTS period_id text;
					CREATE INDEX IF NOT EXISTS evaluation_period_idx
						ON reconciliations.evaluation (period_id, created_at DESC)
						WHERE period_id IS NOT NULL;
					ALTER TABLE reconciliations.alert
						ADD COLUMN IF NOT EXISTS audit_sequence bigint;
					ALTER TABLE reconciliations.alert_event
						ADD COLUMN IF NOT EXISTS audit_sequence bigint;
				`)
				return err
			},
		},
		// Backfill one rule_revision row per rule that predates the journal, so
		// a rule created before this feature still has a retrievable definition
		// at its current revision. audit_sequence 0 marks "not chained" — these
		// snapshots are recovered, not witnessed, and the distinction should stay
		// visible rather than being papered over with a plausible number.
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					INSERT INTO reconciliations.rule_revision (
						rule_id, revision, name, template_kind, template_spec,
						explanation_cel, severity, cadence, enabled, schedule,
						notifications, labels, audit_sequence, created_at
					)
					SELECT
						id, revision, name, template_kind, template_spec,
						explanation_cel, severity, cadence, enabled, schedule,
						notifications, labels, 0, created_at
					FROM reconciliations.rule
					ON CONFLICT (rule_id, revision) DO NOTHING;
				`)
				return err
			},
		},
		// Let a manually triggered evaluation record the revision it evaluated.
		//
		// The original constraint required both-or-neither on
		// (scheduled_at, rule_revision), which forced manual evaluations to store
		// a NULL revision — so their journal entries could not be linked back to
		// the frozen definition that produced the verdict, which is most of the
		// point of keeping revisions. Only the forward implication is actually
		// load-bearing: a scheduled occurrence must carry a revision, because the
		// partial unique index on (rule_id, rule_revision, scheduled_at) fences
		// duplicate committed effects. Manual rows are outside that index.
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					ALTER TABLE reconciliations.evaluation
						DROP CONSTRAINT IF EXISTS evaluation_schedule_identity_chk;
					ALTER TABLE reconciliations.evaluation
						ADD CONSTRAINT evaluation_schedule_identity_chk CHECK (
							scheduled_at IS NULL OR rule_revision IS NOT NULL
						);
				`)
				return err
			},
		},
		// Close the TRUNCATE bypass, and make the guard name the table it fired on.
		//
		// A FOR EACH ROW trigger does not fire on TRUNCATE, so the entire journal
		// could be emptied with no enforcement at all — verified by doing it: five
		// entries to zero, silently. That falsifies the whole immutability claim,
		// since removing every entry is strictly easier than editing one. REVOKE
		// does not help, because the owner keeps implicit privileges.
		//
		// The message fix is small but matters for an audit feature: the shared
		// function hardcoded audit_entry, so an operator blocked on period_seal was
		// told about a table they had not touched.
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					CREATE OR REPLACE FUNCTION reconciliations.audit_entry_immutable()
						RETURNS trigger
						LANGUAGE plpgsql
					AS $$
					BEGIN
						RAISE EXCEPTION
							'%.% is append-only: % is not permitted',
							TG_TABLE_SCHEMA, TG_TABLE_NAME, TG_OP
							USING ERRCODE = 'restrict_violation',
							      HINT = 'The audit journal is immutable by construction. Corrections are recorded as new entries.';
					END;
					$$;

					DROP TRIGGER IF EXISTS audit_entry_no_truncate ON reconciliations.audit_entry;
					CREATE TRIGGER audit_entry_no_truncate
						BEFORE TRUNCATE ON reconciliations.audit_entry
						FOR EACH STATEMENT EXECUTE FUNCTION reconciliations.audit_entry_immutable();

					DROP TRIGGER IF EXISTS rule_revision_no_truncate ON reconciliations.rule_revision;
					CREATE TRIGGER rule_revision_no_truncate
						BEFORE TRUNCATE ON reconciliations.rule_revision
						FOR EACH STATEMENT EXECUTE FUNCTION reconciliations.audit_entry_immutable();

					DROP TRIGGER IF EXISTS period_seal_no_truncate ON reconciliations.period_seal;
					CREATE TRIGGER period_seal_no_truncate
						BEFORE TRUNCATE ON reconciliations.period_seal
						FOR EACH STATEMENT EXECUTE FUNCTION reconciliations.audit_entry_immutable();
				`)
				return err
			},
		},
		// Closures replace period seals.
		//
		// A seal used to derive its range at close time from a period id the caller
		// typed: first = previous seal's last + 1. That single derivation is what
		// forced three ordering guards, a calendar validator and an irreversible
		// mistake if any of them was wrong. A closure instead has its range opened
		// with it — first_sequence is a fact recorded as it happens, not a
		// deduction — and closing takes no argument at all, so the mistakes are
		// unexpressible rather than caught. This follows Ledger V3's chapters, where
		// CloseChapter discards its request payload entirely.
		//
		// The business period label survives, because nothing else can carry it: it
		// is derived from the evaluation's point-in-time, not from the wall clock, so
		// a backfill of May run in August belongs to May. It lives in the per-period
		// breakdown, which the state hash covers and the signature therefore binds.
		//
		// period_seal is left in place, unread. Dropping a table whose whole purpose
		// is to be immutable evidence would be a strange first act for this feature,
		// and an installation that sealed under the old scheme keeps its rows
		// verifiable on their own terms.
		migrations.Migration{
			Up: func(tx bun.Tx) error {
				_, err := tx.Exec(`
					-- The journal has a new kind of entry. Extended here rather than
					-- edited into the original migration, which a database already at
					-- 15 would never re-run.
					ALTER TABLE reconciliations.audit_entry
						DROP CONSTRAINT IF EXISTS audit_entry_kind_chk;
					ALTER TABLE reconciliations.audit_entry
						ADD CONSTRAINT audit_entry_kind_chk CHECK (kind IN (
							'rule.created', 'rule.revised', 'rule.deleted',
							'evaluation.committed', 'alert.transition',
							'period.sealed', 'closure.sealed'
						));

					CREATE TABLE IF NOT EXISTS reconciliations.closure (
						id bigserial PRIMARY KEY,
						status text NOT NULL,
						opened_at timestamptz NOT NULL,
						closed_at timestamptz,
						first_sequence bigint NOT NULL,
						last_sequence bigint,
						entry_count bigint NOT NULL DEFAULT 0,
						last_audit_hash bytea,
						periods jsonb NOT NULL DEFAULT '[]'::jsonb,
						state_hash bytea,
						sealing_hash bytea,
						signature bytea,
						signing_key_id text,
						closed_by jsonb,
						audit_sequence bigint,
						CONSTRAINT closure_status_chk CHECK (status IN ('OPEN', 'CLOSED')),
						CONSTRAINT closure_range_chk CHECK (last_sequence IS NULL OR last_sequence >= first_sequence - 1)
					);

					-- Exactly one closure is open at any time. The ledger states the same
					-- invariant and enforces it in a single-writer FSM; we have neither,
					-- so it is a database constraint rather than a convention.
					CREATE UNIQUE INDEX IF NOT EXISTS closure_single_open
						ON reconciliations.closure ((status)) WHERE status = 'OPEN';

					CREATE INDEX IF NOT EXISTS closure_last_sequence_idx
						ON reconciliations.closure (last_sequence) WHERE last_sequence IS NOT NULL;

					-- Which business periods stopped accepting writes, and under which
					-- closure. Kept as its own table rather than derived from the
					-- breakdown so the barrier stays a primary-key lookup on the hot
					-- alert path.
					CREATE TABLE IF NOT EXISTS reconciliations.frozen_period (
						period_id text PRIMARY KEY,
						closure_id bigint NOT NULL REFERENCES reconciliations.closure (id),
						frozen_at timestamptz NOT NULL
					);

					-- The closing cadence, runtime-modifiable rather than a boot flag.
					-- Changing it mid-flight simply makes the next closure longer or
					-- shorter; closures already closed are unaffected, which is the
					-- property that makes the cadence safe to change at all.
					CREATE TABLE IF NOT EXISTS reconciliations.closing_schedule (
						singleton boolean PRIMARY KEY DEFAULT true,
						cron text NOT NULL,
						updated_at timestamptz NOT NULL,
						CONSTRAINT closing_schedule_singleton_chk CHECK (singleton)
					);

					-- A closure is mutable exactly once, on the open -> closed
					-- transition that records its seal. After that it is evidence, and
					-- the trigger says so. UPDATE on an already-closed row, and DELETE
					-- or TRUNCATE on any row, are refused.
					CREATE OR REPLACE FUNCTION reconciliations.closure_immutable_once_closed()
						RETURNS trigger
						LANGUAGE plpgsql
					AS $$
					BEGIN
						IF TG_OP = 'UPDATE' AND OLD.status <> 'CLOSED' THEN
							RETURN NEW;
						END IF;
						RAISE EXCEPTION
							'%.% is append-only once closed: % is not permitted',
							TG_TABLE_SCHEMA, TG_TABLE_NAME, TG_OP
							USING ERRCODE = 'restrict_violation',
							      HINT = 'A closed closure is evidence. Corrections are recorded as new entries.';
					END;
					$$;

					DROP TRIGGER IF EXISTS closure_immutable ON reconciliations.closure;
					CREATE TRIGGER closure_immutable
						BEFORE UPDATE OR DELETE ON reconciliations.closure
						FOR EACH ROW EXECUTE FUNCTION reconciliations.closure_immutable_once_closed();

					DROP TRIGGER IF EXISTS closure_no_truncate ON reconciliations.closure;
					CREATE TRIGGER closure_no_truncate
						BEFORE TRUNCATE ON reconciliations.closure
						FOR EACH STATEMENT EXECUTE FUNCTION reconciliations.audit_entry_immutable();

					DROP TRIGGER IF EXISTS frozen_period_no_truncate ON reconciliations.frozen_period;
					CREATE TRIGGER frozen_period_no_truncate
						BEFORE TRUNCATE ON reconciliations.frozen_period
						FOR EACH STATEMENT EXECUTE FUNCTION reconciliations.audit_entry_immutable();

					REVOKE UPDATE, DELETE, TRUNCATE ON reconciliations.closure FROM PUBLIC;
					REVOKE UPDATE, DELETE, TRUNCATE ON reconciliations.frozen_period FROM PUBLIC;
				`)
				return err
			},
		},
	)
}
