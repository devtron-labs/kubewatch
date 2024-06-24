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

package statsViz

import (
	"github.com/arl/statsviz"
	"github.com/caarlos0/env"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

type StatsVizRouter interface {
	InitStatsVizRouter(router *mux.Router, servicePrefix string)
}

type StatVizConfig struct {
	EnableStatsviz bool `env:"ENABLE_STATSVIZ" envDefault:"false"`
}

func GetStatsVizConfig() (*StatVizConfig, error) {
	cfg := &StatVizConfig{}
	err := env.Parse(cfg)
	return cfg, err
}

type StatsVizRouterImpl struct {
	logger *zap.SugaredLogger
	config *StatVizConfig
}

func NewStatsVizRouter(logger *zap.SugaredLogger) *StatsVizRouterImpl {
	cfg, _ := GetStatsVizConfig()
	return &StatsVizRouterImpl{
		logger: logger,
		config: cfg,
	}
}

func (r *StatsVizRouterImpl) InitStatsVizRouter(router *mux.Router, servicePrefix string) {
	if !r.config.EnableStatsviz {
		return
	}
	stvServer, _ := statsviz.NewServer(statsviz.Root(servicePrefix + "/debug/statsviz"))

	router.Path("/debug/statsviz/ws").Name("GET /debug/statsviz/ws").HandlerFunc(stvServer.Ws())
	router.PathPrefix("/debug/statsviz/").Name("GET /debug/statsviz/").Handler(stvServer.Index())
}
