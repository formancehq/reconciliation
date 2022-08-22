package api

import (
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

	router.Path("/amount-match").Methods(http.MethodPost).Handler(AmountMatchingHandler(db))
	router.Path("/end-to-end").Methods(http.MethodPost).Handler(EndToEndHandler(db))

	return router
}
