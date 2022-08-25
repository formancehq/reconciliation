package rules

import (
	"context"
	"github.com/numary/reconciliation/internal/model"

	"github.com/numary/reconciliation/internal/storage"
)

type Rule interface {
	Accept(ctx context.Context, event model.Event) error
	Reconcile(ctx context.Context, store storage.Store, event model.Event) error
}
