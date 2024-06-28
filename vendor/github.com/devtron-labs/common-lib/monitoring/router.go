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

package monitoring

import (
	"github.com/devtron-labs/common-lib/monitoring/pprof"
	"github.com/devtron-labs/common-lib/monitoring/statsViz"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

type MonitoringRouter struct {
	pprofRouter    pprof.PProfRouter
	statsVizRouter statsViz.StatsVizRouter
}

func (r MonitoringRouter) InitMonitoringRouter(pprofSubRouter *mux.Router, statvizSubRouter *mux.Router, servicePrefix string) {
	r.pprofRouter.InitPProfRouter(pprofSubRouter)
	r.statsVizRouter.InitStatsVizRouter(statvizSubRouter, servicePrefix)
}

func NewMonitoringRouter(logger *zap.SugaredLogger) *MonitoringRouter {
	return &MonitoringRouter{
		pprofRouter:    pprof.NewPProfRouter(logger),
		statsVizRouter: statsViz.NewStatsVizRouter(logger),
	}
}
