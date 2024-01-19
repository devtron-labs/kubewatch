package pprof

import (
	"go.uber.org/zap"
	"net/http"
	"net/http/pprof"
)

type PProfRestHandler interface {
	Index(w http.ResponseWriter, r *http.Request)
	Cmdline(w http.ResponseWriter, r *http.Request)
	Profile(w http.ResponseWriter, r *http.Request)
	Symbol(w http.ResponseWriter, r *http.Request)
	Trace(w http.ResponseWriter, r *http.Request)
	Goroutine(w http.ResponseWriter, r *http.Request)
	ThreadCreate(w http.ResponseWriter, r *http.Request)
	Heap(w http.ResponseWriter, r *http.Request)
	Block(w http.ResponseWriter, r *http.Request)
	Mutex(w http.ResponseWriter, r *http.Request)
	Allocs(w http.ResponseWriter, r *http.Request)
}

type PProfRestHandlerImpl struct {
	logger *zap.SugaredLogger
}

func NewPProfRestHandler(logger *zap.SugaredLogger) *PProfRestHandlerImpl {
	return &PProfRestHandlerImpl{logger: logger}
}
func (p *PProfRestHandlerImpl) Index(w http.ResponseWriter, r *http.Request) {
	pprof.Index(w, r)
}

func (p *PProfRestHandlerImpl) Cmdline(w http.ResponseWriter, r *http.Request) {
	pprof.Cmdline(w, r)
}

func (p *PProfRestHandlerImpl) Profile(w http.ResponseWriter, r *http.Request) {
	pprof.Profile(w, r)
}

func (p *PProfRestHandlerImpl) Symbol(w http.ResponseWriter, r *http.Request) {
	pprof.Symbol(w, r)
}

func (p *PProfRestHandlerImpl) Trace(w http.ResponseWriter, r *http.Request) {
	pprof.Trace(w, r)
}

func (p *PProfRestHandlerImpl) Goroutine(w http.ResponseWriter, r *http.Request) {
	pprof.Handler("goroutine").ServeHTTP(w, r)
}

func (p *PProfRestHandlerImpl) ThreadCreate(w http.ResponseWriter, r *http.Request) {
	pprof.Handler("threadcreate").ServeHTTP(w, r)
}

func (p *PProfRestHandlerImpl) Heap(w http.ResponseWriter, r *http.Request) {
	pprof.Handler("heap").ServeHTTP(w, r)
}

func (p *PProfRestHandlerImpl) Block(w http.ResponseWriter, r *http.Request) {
	pprof.Handler("block").ServeHTTP(w, r)
}

func (p *PProfRestHandlerImpl) Mutex(w http.ResponseWriter, r *http.Request) {
	pprof.Handler("mutex").ServeHTTP(w, r)
}

func (p *PProfRestHandlerImpl) Allocs(w http.ResponseWriter, r *http.Request) {
	pprof.Handler("allocs").ServeHTTP(w, r)
}
