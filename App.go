package main

import (
	"fmt"
	api "github.com/devtron-labs/kubewatch/api/router"
	"go.uber.org/zap"
	"net/http"
	"os"
)

type App struct {
	MuxRouter *api.RouterImpl
	Logger    *zap.SugaredLogger
	server    *http.Server
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
	server := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: app.MuxRouter.Router}
	app.server = server
	err := server.ListenAndServe()
	if err != nil {
		app.Logger.Errorw("error in startup", "err", err)
		os.Exit(2)
	}
}
