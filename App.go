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

package main

import (
	"context"
	"fmt"
	"github.com/caarlos0/env"
	pubsub "github.com/devtron-labs/common-lib/pubsub-lib"
	api "github.com/devtron-labs/kubewatch/api/router"
	repository "github.com/devtron-labs/kubewatch/pkg/cluster"
	"github.com/devtron-labs/kubewatch/pkg/controller"
	"github.com/devtron-labs/kubewatch/pkg/informer"
	"github.com/devtron-labs/kubewatch/pkg/sql"
	"github.com/go-pg/pg"
	"go.uber.org/zap"
	"net/http"
	"os"
	"time"
)

type App struct {
	MuxRouter       *api.RouterImpl
	Logger          *zap.SugaredLogger
	server          *http.Server
	k8sInformerImpl *informer.K8sInformerImpl
	clusterCfg      *controller.ClusterConfig
	externalConfig  *controller.ExternalConfig
	db              *pg.DB
	defaultTimeout  time.Duration
}

type Timeout struct {
	SleepTimeout int `env:"SLEEP_TIMEOUT" envDefault:"5"`
}

func GetTimeout() (*Timeout, error) {
	cfg := &Timeout{}
	err := env.Parse(cfg)
	return cfg, err
}

func NewApp(MuxRouter *api.RouterImpl, Logger *zap.SugaredLogger, clusterCfg *controller.ClusterConfig, externalConfig *controller.ExternalConfig) *App {
	timeout, err := GetTimeout()
	if err != nil {
		fmt.Println(err)
		return nil
	}
	return &App{
		MuxRouter:      MuxRouter,
		Logger:         Logger,
		clusterCfg:     clusterCfg,
		externalConfig: externalConfig,
		defaultTimeout: time.Duration(timeout.SleepTimeout) * time.Second,
	}
}
func (app *App) Start() {
	port := 8080 //TODO: extract from environment variable
	app.Logger.Infow("starting server on ", "port", port)
	app.MuxRouter.Init()

	app.server = &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: app.MuxRouter.Router}
	err := app.server.ListenAndServe()
	if err != nil {
		app.Logger.Errorw("error in startup", "err", err)
		os.Exit(2)
	}
}

func (app *App) getPubSubClientForInternalConfig() *pubsub.PubSubClientServiceImpl {

	if app.externalConfig.External {
		return nil
	}
	client, err := pubsub.NewPubSubClientServiceImpl(app.Logger)
	if err != nil {
		app.Logger.Errorw("error in startup", "err", err)
		os.Exit(2)
	}
	return client
}

func (app *App) Stop() {

	app.Logger.Infow("kubewatch shutdown initiating")

	timeoutContext, _ := context.WithTimeout(context.Background(), app.defaultTimeout)
	app.Logger.Infow("closing router")
	err := app.server.Shutdown(timeoutContext)
	if err != nil {
		app.Logger.Errorw("error in mux router shutdown", "err", err)
	}
	app.Logger.Infow("router closed successfully")

	if app.isClusterTypeAllAndIsInternalConfig() {
		app.k8sInformerImpl.StopAllSystemWorkflowInformer()
		app.Logger.Infow("closing db connection")
		err = app.db.Close()
		if err != nil {
			app.Logger.Errorw("Error while closing DB", "error", err)
		}
		app.Logger.Infow("db closed successfully")
	}
}

func GetExternalConfig() (*controller.ExternalConfig, error) {
	externalConfig := &controller.ExternalConfig{}
	err := env.Parse(externalConfig)
	if err != nil {
		return nil, err
	}
	return externalConfig, err
}

func GetClusterConfig() (*controller.ClusterConfig, error) {
	clusterCfg := &controller.ClusterConfig{}
	err := env.Parse(clusterCfg)
	if err != nil {
		return nil, err
	}
	return clusterCfg, err
}

func (app *App) buildInformerForAllClusters(client *pubsub.PubSubClientServiceImpl) {
	var err error
	config, _ := sql.GetConfig()
	app.db, err = sql.NewDbConnection(config, app.Logger)
	if err != nil {
		app.Logger.Errorw("error in loading external config", "err", err)
		os.Exit(2)
	}
	clusterRepositoryImpl := repository.NewClusterRepositoryImpl(app.db, app.Logger)
	app.k8sInformerImpl = informer.NewK8sInformerImpl(app.Logger, clusterRepositoryImpl, client)
	err = app.k8sInformerImpl.BuildInformerForAllClusters()
}

func (app *App) isClusterTypeAllAndIsInternalConfig() bool {
	return app.clusterCfg.ClusterType == controller.ClusterTypeAll && !app.externalConfig.External
}
