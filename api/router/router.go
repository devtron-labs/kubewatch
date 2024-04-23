package api

import (
	"encoding/json"
	"github.com/devtron-labs/common-lib/monitoring"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"net/http"
)

type RouterImpl struct {
	logger           *zap.SugaredLogger
	Router           *mux.Router
	monitoringRouter *monitoring.MonitoringRouter
}

func NewRouter(logger *zap.SugaredLogger, monitoringRouter *monitoring.MonitoringRouter) *RouterImpl {
	return &RouterImpl{
		logger:           logger,
		Router:           mux.NewRouter(),
		monitoringRouter: monitoringRouter,
	}
}

type Response struct {
	Code   int
	Result string
}

func (r *RouterImpl) Init() {

	pProfListenerRouter := r.Router.PathPrefix("/kubewatch/debug/pprof/").Subrouter()
	statsVizRouter := r.Router.PathPrefix("/kubewatch").Subrouter()

	r.monitoringRouter.InitMonitoringRouter(pProfListenerRouter, statsVizRouter, "/kubewatch")
	r.Router.Handle("/metrics", promhttp.Handler())
	r.Router.Path("/health").HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(200)
		response := Response{}
		response.Code = 200
		response.Result = "OK"
		b, err := json.Marshal(response)
		if err != nil {
			b = []byte("OK")
			r.logger.Errorw("Unexpected error in apiError", "err", err)
		}
		_, _ = writer.Write(b)
	})
}
