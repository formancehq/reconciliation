package worker

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type workerMetrics struct {
	jobsCreated   metric.Int64Counter
	jobsClaimed   metric.Int64Counter
	jobsReclaimed metric.Int64Counter
	jobsRetried   metric.Int64Counter
	jobsFailed    metric.Int64Counter
	rulesBusy     metric.Int64Counter
	skipped       metric.Int64Counter
	plannerLag    metric.Float64Histogram
	jobsByStatus  metric.Int64Gauge
}

func newWorkerMetrics() (*workerMetrics, error) {
	meter := otel.Meter("github.com/formancehq/reconciliation/internal/worker")
	created, err := meter.Int64Counter("reconciliation.scheduler.jobs.created")
	if err != nil {
		return nil, err
	}
	claimed, err := meter.Int64Counter("reconciliation.scheduler.jobs.claimed")
	if err != nil {
		return nil, err
	}
	reclaimed, err := meter.Int64Counter("reconciliation.scheduler.jobs.reclaimed")
	if err != nil {
		return nil, err
	}
	retried, err := meter.Int64Counter("reconciliation.scheduler.jobs.retried")
	if err != nil {
		return nil, err
	}
	failed, err := meter.Int64Counter("reconciliation.scheduler.jobs.failed")
	if err != nil {
		return nil, err
	}
	busy, err := meter.Int64Counter("reconciliation.scheduler.rule_busy")
	if err != nil {
		return nil, err
	}
	skipped, err := meter.Int64Counter("reconciliation.scheduler.occurrences.skipped")
	if err != nil {
		return nil, err
	}
	lag, err := meter.Float64Histogram("reconciliation.scheduler.planner.lag", metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	byStatus, err := meter.Int64Gauge("reconciliation.scheduler.jobs", metric.WithDescription("Current durable evaluation jobs by status"))
	if err != nil {
		return nil, err
	}
	return &workerMetrics{
		jobsCreated: created, jobsClaimed: claimed, jobsReclaimed: reclaimed,
		jobsRetried: retried, jobsFailed: failed, rulesBusy: busy,
		skipped: skipped, plannerLag: lag, jobsByStatus: byStatus,
	}, nil
}

func (m *workerMetrics) recordStatus(ctx context.Context, status string, count int64) {
	m.jobsByStatus.Record(ctx, count, metric.WithAttributes(attribute.String("status", status)))
}
