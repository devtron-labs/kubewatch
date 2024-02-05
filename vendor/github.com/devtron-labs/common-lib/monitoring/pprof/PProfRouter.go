package pprof

import (
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

type PProfRouter interface {
	InitPProfRouter(router *mux.Router)
}

type PProfRouterImpl struct {
	logger           *zap.SugaredLogger
	pProfRestHandler PProfRestHandler
}

func NewPProfRouter(logger *zap.SugaredLogger) *PProfRouterImpl {
	return &PProfRouterImpl{
		logger:           logger,
		pProfRestHandler: NewPProfRestHandler(logger),
	}
}

func (r *PProfRouterImpl) InitPProfRouter(router *mux.Router) {
	router.HandleFunc("/", r.pProfRestHandler.Index)
	router.HandleFunc("/cmdline", r.pProfRestHandler.Cmdline)
	router.HandleFunc("/profile", r.pProfRestHandler.Profile)
	router.HandleFunc("/symbol", r.pProfRestHandler.Symbol)
	router.HandleFunc("/trace", r.pProfRestHandler.Trace)
	router.HandleFunc("/goroutine", r.pProfRestHandler.Goroutine)
	router.HandleFunc("/threadcreate", r.pProfRestHandler.ThreadCreate)
	router.HandleFunc("/heap", r.pProfRestHandler.Heap)
	router.HandleFunc("/block", r.pProfRestHandler.Block)
	router.HandleFunc("/mutex", r.pProfRestHandler.Mutex)
	router.HandleFunc("/allocs", r.pProfRestHandler.Allocs)
}
