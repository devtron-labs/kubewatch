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
	"github.com/devtron-labs/kubewatch/pkg/controller"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	app, err := InitializeApp()
	if err != nil {
		log.Panic(err)
	}
	go app.Start()
	client := app.getPubSubClientForInternalConfig()

	if app.isClusterTypeAllAndIsInternalConfig() {
		app.buildInformerForAllClusters(client)
	}

	startInformer := controller.NewStartController(app.Logger, client, app.externalConfig)
	stopChan := make(chan int)
	go startInformer.Start(stopChan)
	var gracefulStop = make(chan os.Signal)
	signal.Notify(gracefulStop, syscall.SIGTERM, syscall.SIGINT)
	sig := <-gracefulStop
	stopChan <- 0
	app.Logger.Infow("caught sig: %+v", sig)
	app.Stop()
	time.Sleep(app.defaultTimeout)
}
