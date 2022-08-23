package server

import (
	"fmt"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/reconciliation/pkg/service"
	"github.com/numary/reconciliation/pkg/storage"
)

const (
	PathHealthCheck = "/_healthcheck"
	PathAmountMatch = "/amount-match"
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
	h.Router.POST(PathAmountMatch, h.amountMatchHandle)
	h.Router.POST(PathEndToEnd, h.endToEndHandle)

	return h
}

func (h *serverHandler) healthCheckHandle(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	sharedlogging.GetLogger(r.Context()).Infof("health check OK")
}

func (h *serverHandler) amountMatchHandle(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	pspIdPath := p.ByName(ParamPspId)
	if pspIdPath == "" {
		pspIdPath = PspIdDefault
	}

	if err := service.ReconciliatePayins(r.Context(), h.store, pspIdPath); err != nil {
		err = fmt.Errorf("service.ReconciliatePayins: %w", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := service.ReconciliatePayouts(r.Context(), h.store, pspIdPath); err != nil {
		err = fmt.Errorf("service.ReconciliatePayouts: %w", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sharedlogging.GetLogger(r.Context()).Infof("amount match OK")
}

func (h *serverHandler) endToEndHandle(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	flowIdPath := p.ByName(ParamFlowId)
	if flowIdPath == "" {
		flowIdPath = FlowIdDefault
	}

	if err := service.ReconciliateEndToEnd(r.Context(), h.store, flowIdPath); err != nil {
		err = fmt.Errorf("service.ReconciliateEndToEnd: %w", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sharedlogging.GetLogger(r.Context()).Infof("end to end OK")
}
