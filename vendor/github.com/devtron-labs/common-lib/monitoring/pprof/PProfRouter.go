/*
 * Copyright (c) 2024. Devtron Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

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
