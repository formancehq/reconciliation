package service

import (
	"context"
	"errors"
	"strings"

	"github.com/formancehq/reconciliation/internal/models"
)

// subjectCtxKey carries the caller's verified access-token subject. It is set by
// the HTTP layer's subjectMiddleware (see internal/api) and read here so the
// service layer never has to know about HTTP or JWTs.
type subjectCtxKey struct{}

// WithSubject binds the verified end-user subject onto a context. Exported so the
// HTTP middleware (a different package) and tests can populate it.
func WithSubject(ctx context.Context, subject string) context.Context {
	return context.WithValue(ctx, subjectCtxKey{}, subject)
}

// SubjectFromContext returns the verified subject bound by subjectMiddleware and
// whether one was present. Empty/absent means the caller was not authenticated
// (e.g. auth disabled in local dev).
func SubjectFromContext(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(subjectCtxKey{}).(string)
	return s, ok && s != ""
}

// resolveActor decides the authoritative actor for a lifecycle transition and
// records its provenance (EN-1930, P1.2). When the caller is authenticated the
// verified subject is authoritative and any self-declared name is demoted to a
// note; otherwise the self-declared `by` stands in, tagged as unverified — and
// is then required, since an entirely unattributed transition is not acceptable.
//
// The returned `by` is what lands in the model's existing By field (so read
// paths and the UI keep working); the returned Actor is the tamper-evident
// provenance that travels inside the signed metadata alongside it.
func resolveActor(ctx context.Context, declaredBy string) (string, *models.Actor, error) {
	declaredBy = strings.TrimSpace(declaredBy)

	if subject, ok := SubjectFromContext(ctx); ok {
		return subject, &models.Actor{
			Subject:  subject,
			Source:   models.ActorSourceToken,
			Declared: declaredBy,
		}, nil
	}

	if declaredBy == "" {
		return "", nil, errors.New("'by' is required when the caller is not authenticated")
	}

	return declaredBy, &models.Actor{
		Source:   models.ActorSourceDeclared,
		Declared: declaredBy,
	}, nil
}
