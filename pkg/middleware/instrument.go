/*
 * Copyright (c) 2020 Devtron Labs
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package middleware

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var UnreachableCluster = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "Kubewatch_unreachable_client_count",
		Help: "How many HTTP requests processed, partitioned by status code, method and HTTP path.",
	},
	[]string{"clusterName", "clusterId", "err"})

var UnreachableClusterAPI = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "Kubewatch_unreachable_client_count_API",
		Help: "How many HTTP requests processed, partitioned by status code, method and HTTP path.",
	},
	[]string{"Host", "path", "err"})

func IncUnUnreachableCluster(clusterName, clusterId, err string) {
	UnreachableCluster.WithLabelValues(clusterName, clusterId, err).Inc()
}
func IncUnUnreachableClusterAPI(host, path, err string) {
	UnreachableClusterAPI.WithLabelValues(host, path, err).Inc()
}
