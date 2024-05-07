//go:build wireinject
// +build wireinject

package main

import (
	"github.com/devtron-labs/common-lib/monitoring"
	api "github.com/devtron-labs/kubewatch/api/router"
	"github.com/devtron-labs/kubewatch/pkg/logger"
	"github.com/google/wire"
)

func InitializeApp() (*App, error) {
	wire.Build(
		logger.NewSugaredLogger,
		NewApp,
		api.NewRouter,
		monitoring.NewMonitoringRouter,
		GetExternalConfig,
		GetClusterConfig,
	)
	return &App{}, nil
}
