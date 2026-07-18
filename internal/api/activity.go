package api

import (
	"fmt"
	"net/http"

	"github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/reconciliation/internal/api/backend"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func listRuleActivitiesHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "ruleID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		q := store.GetRuleActivitiesQuery{}
		if raw := r.URL.Query().Get(QueryKeyCursor); raw != "" {
			if err := bunpaginate.UnmarshalCursor(raw, &q); err != nil {
				api.BadRequest(w, ErrValidation, fmt.Errorf("invalid '%s' query param", QueryKeyCursor))
				return
			}
		} else {
			opts, err := getPaginatedQueryOptionsRuleActivities(r)
			if err != nil {
				api.BadRequest(w, ErrValidation, err)
				return
			}
			q = store.NewGetRuleActivitiesQuery(*opts)
		}
		cursor, err := b.GetService().ListRuleActivities(r.Context(), id, q)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.RenderCursor(w, *cursor)
	}
}
