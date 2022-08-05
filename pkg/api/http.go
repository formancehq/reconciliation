package api

import (
	"context"
	"net/http"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/mongo"
)

func ReconciliationRouter(db *mongo.Database) *mux.Router {
	router := mux.NewRouter()
	router.Use(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			h.ServeHTTP(w, r)
		})
	})

	ctx := context.Background() // TODO: see if something better

	router.Path("/reconciliation/amountmatch").Methods(http.MethodPost).Handler(AmountMatchingHandler(ctx, db))
	router.Path("/reconciliation/endtoend").Methods(http.MethodPost).Handler(EndToEndHandler(ctx, db))

	return router
}
