//go:build wireinject
// +build wireinject

package main

import (
	"github.com/devtron-labs/common-lib/monitoring"
	pubsub_lib "github.com/devtron-labs/common-lib/pubsub-lib"
	api "github.com/devtron-labs/kubewatch/api/router"
	repository "github.com/devtron-labs/kubewatch/pkg/cluster"
	"github.com/devtron-labs/kubewatch/pkg/informer"
	"github.com/devtron-labs/kubewatch/pkg/logger"
	"github.com/devtron-labs/kubewatch/pkg/sql"
	"github.com/google/wire"
)

func InitializeApp() (*App, error) {
	wire.Build(
		logger.NewSugaredLogger,
		NewApp,
		api.NewRouter,
		sql.NewDbConnection,
		sql.GetConfig,
		informer.NewK8sInformerImpl,
		pubsub_lib.NewPubSubClientServiceImpl,
		monitoring.NewMonitoringRouter,
		repository.NewClusterRepositoryImpl,
		wire.Bind(new(repository.ClusterRepository), new(*repository.ClusterRepositoryImpl)),
	)
	return &App{}, nil
}
