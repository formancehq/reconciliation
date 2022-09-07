package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/julienschmidt/httprouter"
	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/reconciliation/internal/storage"
)

const (
	PathHealthCheck = "/_healthcheck"
	PathGetTxCheck  = "/transaction"
	PathEndToEnd    = "/end-to-end"

	ParamPspId  = "psp_id_path"
	ParamFlowId = "flow_id_path"

	PspIdDefault  = "metadata.psp_id"
	FlowIdDefault = "metadata.flow_id"
)

type serverHandler struct {
	*httprouter.Router

	store storage.Store
}

func newServerHandler(store storage.Store) http.Handler {
	h := &serverHandler{
		Router: httprouter.New(),
		store:  store,
	}

	h.Router.GET(PathHealthCheck, h.healthCheckHandle)
	h.Router.POST(PathGetTxCheck, h.getReconciliationHandle)

	return h
}

func (h *serverHandler) healthCheckHandle(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	sharedlogging.GetLogger(r.Context()).Infof("health check OK")
}

func (h *serverHandler) getReconciliationHandle(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	paramTxID := p.ByName("txid")
	if paramTxID == "" {

	}

	txID, err := strconv.ParseInt(paramTxID, 10, 64)
	if err == nil {
		//todo
	}

	transaction, err := h.store.GetTransaction(r.Context(), txID)
	if err != nil {
		//todo
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(transaction)

	sharedlogging.GetLogger(r.Context()).Infof("end to end OK")
}
