package api

import (
	"fmt"
	"github.com/numary/reconciliation/pkg/helper"
	"net/http"

	"go.mongodb.org/mongo-driver/mongo"
)

func EndToEndHandler(db *mongo.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		flowIDpath := r.URL.Query().Get("flow_id_path") //TODO: see if camelcase standard
		if flowIDpath == "" {
			//TODO: see if default is a good idea
			flowIDpath = "metadata.flow_id" //TODO: flow_id
		}

		// end to end
		err := helper.ReconciliateEndToEnd(ctx, db, flowIDpath) //TODO: async
		if err != nil {
			fmt.Printf("error: could not reconciliate flow : %v", err)
		}
		// TODO: format result
	}
}

func AmountMatchingHandler(db *mongo.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		pspIdPath := r.URL.Query().Get("psp_id_path")
		if pspIdPath == "" {
			pspIdPath = "metadata.psp_id"
		}

		err := helper.ReconciliatePayins(ctx, db, pspIdPath)
		if err != nil {
			return
		}
		// TODO: format result

		err = helper.ReconciliatePayouts(ctx, db, pspIdPath)
		if err != nil {
			return
		}
		// TODO: format result
	}
}
