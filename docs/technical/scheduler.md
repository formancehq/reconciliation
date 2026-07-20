# Durable cron scheduler

Cron rules are executed by the separate `reconciliation worker` process. API
pods are stateless and never run a scheduling loop.

## PostgreSQL coordination

PostgreSQL stores the durable cursor (`rule.next_run_at`) and one
`evaluation_job` per `(rule, revision, scheduled_at)` occurrence. Every worker
pod may run both planner and executor loops:

1. Planners lock due rule rows with `FOR UPDATE SKIP LOCKED`, insert jobs with a
   unique occurrence constraint, and advance the cursor atomically.
2. Executors claim pending or expired jobs with `FOR UPDATE SKIP LOCKED`, a
   two-minute lease, and a fresh fencing token.
3. Evaluations of the same rule take the existing PostgreSQL advisory lock.
   Different rules may run in parallel.
4. The final transaction validates the job token and rule revision before it
   writes the evaluation, alert transitions, alert events, and job success.

This provides at-least-once execution attempts and exactly one committed set of
database effects per occurrence. Ledger and Payments reads can be repeated when
a worker dies after reading but before committing.

## Catch-up and retries

- Scheduled evaluations use the occurrence time as their explicit PIT and then
  subtract the configured safety margin (30 seconds by default).
- After downtime, only occurrences from the last 24 hours are considered and
  only the most recent 100 per rule are materialized.
- Infrastructure failures are retried five times with exponential backoff.
  A persisted `ERROR` evaluation is a completed job, not an infrastructure
  retry.
- Terminal job rows are retained for 30 days. The scheduled occurrence identity
  is also stored on `evaluation`, so cleanup cannot remove the deduplication
  barrier.

## Scaling and delivery semantics

The Operator initially deploys one worker with four concurrent evaluations, but
multiple worker pods and rolling-update overlap are safe. `replicas: 1` is a
capacity choice, not a correctness mechanism.

Alert events still use the PostgreSQL-backed publisher circuit breaker from
`go-libs`. It retries messages that reach the publisher while the broker is
unavailable, but it is not a transactional outbox: a crash between the business
commit and `Publish` can lose a notification, and multiple publishers can replay
the same message. The stable `alert_event` UUID is the downstream idempotency
key; delivery is not described as exactly once.
