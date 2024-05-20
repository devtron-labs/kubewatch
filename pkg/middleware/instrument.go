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

// metrics name constants
const (
	KUBEWATCH_UNREACHABLE_CLIENT_COUNT     = "Kubewatch_unreachable_client_count"
	KUBEWATCH_UNREACHABLE_CLIENT_COUNT_API = "Kubewatch_unreachable_client_count_API"
)

// metrics labels constants
const (
	CLUSTER_NAME = "clusterName"
	CLUSTER_ID   = "clusterId"
	HOST         = "host"
	PATH         = "path"
)

var UnreachableCluster = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: KUBEWATCH_UNREACHABLE_CLIENT_COUNT,
		Help: "How many HTTP requests processed, partitioned by status code, method and HTTP path.",
	},
	[]string{CLUSTER_NAME, CLUSTER_ID})

func IncUnUnreachableCluster(clusterName, clusterId string) {
	UnreachableCluster.WithLabelValues(clusterName, clusterId).Inc()
}
