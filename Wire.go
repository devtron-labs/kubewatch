//go:build wireinject
// +build wireinject

package main

import (
	api "github.com/devtron-labs/kubewatch/api/router"
	"github.com/devtron-labs/kubewatch/internal/logger"
	"github.com/google/wire"
)

func InitializeApp() (*App, error) {
	wire.Build(
		logger.NewSugardLogger,
		NewApp,
		api.NewRouter,
	)
	return &App{}, nil
}
