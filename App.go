package main

import (
	"context"
	"fmt"
	"github.com/caarlos0/env"
	api "github.com/devtron-labs/kubewatch/api/router"
	"github.com/devtron-labs/kubewatch/pkg/controller"
	"github.com/devtron-labs/kubewatch/pkg/informer"
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
	server := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: app.MuxRouter.Router}
	app.server = server
	err := server.ListenAndServe()
	if err != nil {
		app.Logger.Errorw("error in startup", "err", err)
		os.Exit(2)
	}
	clusterCfg := &controller.ClusterConfig{}
	err = env.Parse(clusterCfg)
	if err != nil {
		//TODO: handle error
	}
	externalConfig := &controller.ExternalConfig{}
	err = env.Parse(externalConfig)
	if err != nil {

	}
	if clusterCfg.ClusterType == controller.ClusterTypeAll && !externalConfig.External {
		app.k8sInformerImpl.BuildInformerForAllClusters()
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

	// Gracefully stop all Kubernetes informers
	app.k8sInformerImpl.StopAllSystemWorkflowInformer()

	app.Logger.Infow("closing db connection")
	err = app.db.Close()
	if err != nil {
		app.Logger.Errorw("Error while closing DB", "error", err)
	}

}
