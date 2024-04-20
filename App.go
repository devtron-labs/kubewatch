package main

import (
	"context"
	"fmt"
	"github.com/caarlos0/env"
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
	db              *pg.DB
	k8sInformerImpl *informer.K8sInformerImpl
}

func NewApp(MuxRouter *api.RouterImpl, Logger *zap.SugaredLogger, db *pg.DB, K8sInformerImpl *informer.K8sInformerImpl) *App {
	return &App{
		MuxRouter:       MuxRouter,
		Logger:          Logger,
		db:              db,
		k8sInformerImpl: K8sInformerImpl,
	}
}
func (app *App) Start() {
	port := 8080 //TODO: extract from environment variable
	app.Logger.Infow("starting server on ", "port", port)
	app.MuxRouter.Init()
	clusterCfg := &controller.ClusterConfig{}
	err := env.Parse(clusterCfg)
	if err != nil {
		app.Logger.Errorw("error in loading cluster config", "err", err)
		os.Exit(2)
	}
	externalConfig := &controller.ExternalConfig{}
	err = env.Parse(externalConfig)
	if err != nil {
		app.Logger.Errorw("error in loading external config", "err", err)
		os.Exit(2)
	}
	if clusterCfg.ClusterType == controller.ClusterTypeAll && !externalConfig.External {
		config, _ := sql.GetConfig()
		connection, err := sql.NewDbConnection(config, app.Logger)
		if err != nil {
			app.Logger.Errorw("error in loading external config", "err", err)
			os.Exit(2)
		}
		app.db = connection
		clusterRepositoryImpl := repository.NewClusterRepositoryImpl(connection, app.Logger)
		k8sInformerImpl := informer.NewK8sInformerImpl(app.Logger, clusterRepositoryImpl, client)
		app.k8sInformerImpl = k8sInformerImpl
		app.k8sInformerImpl.BuildInformerForAllClusters()
	}
	server := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: app.MuxRouter.Router}
	app.server = server
	err = server.ListenAndServe()
	if err != nil {
		app.Logger.Errorw("error in startup", "err", err)
		os.Exit(2)
	}

}

func (app *App) Stop() {

	app.Logger.Infow("kubewatch shutdown initiating")

	timeoutContext, _ := context.WithTimeout(context.Background(), 5*time.Second)
	app.Logger.Infow("closing router")
	err := app.server.Shutdown(timeoutContext)
	if err != nil {
		app.Logger.Errorw("error in mux router shutdown", "err", err)
	}

	clusterCfg := &controller.ClusterConfig{}
	err = env.Parse(clusterCfg)
	if err != nil {
		app.Logger.Errorw("error in loading cluster config", "err", err)
	}
	externalConfig := &controller.ExternalConfig{}
	err = env.Parse(externalConfig)
	if err != nil {
		app.Logger.Errorw("error in loading external config", "err", err)
	}

	if clusterCfg.ClusterType == controller.ClusterTypeAll && !externalConfig.External {
		// Gracefully stop all Kubernetes informers
		app.k8sInformerImpl.StopAllSystemWorkflowInformer()

		app.Logger.Infow("closing db connection")
		err = app.db.Close()
		if err != nil {
			app.Logger.Errorw("Error while closing DB", "error", err)
		}
	}
}
