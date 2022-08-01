package api

import (
	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/mongo"
	"net/http"
)

func ReconciliationRouter(db *mongo.Database) *mux.Router {
	router := mux.NewRouter()
	router.Use(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			h.ServeHTTP(w, r)
		})
	})
	router.Path("/reconciliation/amountmatch").Methods(http.MethodGet).Handler(AmountMatchingHandler(db))
	router.Path("/reconciliation/endtoend").Methods(http.MethodGet).Handler(EndToEndHandler(db))

	return router
}
