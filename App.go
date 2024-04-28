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
}

func NewApp(MuxRouter *api.RouterImpl, Logger *zap.SugaredLogger) *App {
	return &App{
		MuxRouter: MuxRouter,
		Logger:    Logger,
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

	app.clusterCfg = clusterCfg
	app.externalConfig = externalConfig

	var client *pubsub.PubSubClientServiceImpl

	if !externalConfig.External {
		client = pubsub.NewPubSubClientServiceImpl(app.Logger)
	}

	if clusterCfg.ClusterType == controller.ClusterTypeAll && !externalConfig.External {
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

	startController := controller.NewStartController(app.Logger, client)
	startController.Start()

	app.server = &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: app.MuxRouter.Router}
	err = app.server.ListenAndServe()
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

	if app.clusterCfg.ClusterType == controller.ClusterTypeAll && !app.externalConfig.External {
		// Gracefully stop all Kubernetes informers
		app.k8sInformerImpl.StopAllSystemWorkflowInformer()

		app.Logger.Infow("closing db connection")
		err = app.db.Close()
		if err != nil {
			app.Logger.Errorw("Error while closing DB", "error", err)
		}
	}
}
