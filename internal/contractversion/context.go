package contractversion

import (
	"context"

	"github.com/formancehq/reconciliation/internal/models"
)

type contextKey struct{}

func WithContext(ctx context.Context, version models.ContractVersion) context.Context {
	return context.WithValue(ctx, contextKey{}, version)
}

func FromContext(ctx context.Context) (models.ContractVersion, bool) {
	version, ok := ctx.Value(contextKey{}).(models.ContractVersion)
	return version, ok
}
