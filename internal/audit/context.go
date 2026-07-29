package audit

import (
	"context"

	"github.com/formancehq/reconciliation/internal/models"
)

type subjectKey struct{}

// WithSubject attaches the attribution for everything recorded downstream. The
// HTTP layer sets it from the verified bearer token; background components set a
// system subject naming themselves.
func WithSubject(ctx context.Context, s models.Subject) context.Context {
	return context.WithValue(ctx, subjectKey{}, s)
}

// SubjectFrom returns the attribution carried on the context.
//
// When nothing was attached the result is an unknown subject rather than a
// plausible-looking one. An unattributed entry is a wiring bug worth seeing in
// the journal; inventing "system" or copying a client-supplied name would hide
// it, and a hidden attribution bug in an audit journal is worse than a visible
// gap.
func SubjectFrom(ctx context.Context) models.Subject {
	s, ok := ctx.Value(subjectKey{}).(models.Subject)
	if !ok {
		return models.Subject{}
	}
	return s
}

// Component names for system subjects, kept here so the strings that end up
// inside hashes have one definition.
const (
	// ComponentScheduler is the cron-driven evaluation path.
	ComponentScheduler = "scheduler"
	// ComponentWorker is the job worker that executes scheduled evaluations.
	ComponentWorker = "worker"
	// ComponentEngine is an evaluation-driven automatic transition, such as an
	// alert auto-resolving because the next evaluation passed.
	ComponentEngine = "engine"
)

// WithSystemSubject is shorthand for attributing downstream writes to an
// internal component.
func WithSystemSubject(ctx context.Context, component string) context.Context {
	return WithSubject(ctx, models.SystemSubject(component))
}
