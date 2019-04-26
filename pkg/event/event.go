/*
Copyright 2016 Skippbox, Ltd.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package event

import (
	"fmt"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/bitnami-labs/kubewatch/pkg/utils"
	apps_v1beta1 "k8s.io/api/apps/v1beta1"
	batch_v1 "k8s.io/api/batch/v1"
	api_v1 "k8s.io/api/core/v1"
	ext_v1beta1 "k8s.io/api/extensions/v1beta1"
)

// Event represent an event got from k8s api server
// Events from different endpoints need to be casted to KubewatchEvent
// before being able to be handled by handler
type Event struct {
	Namespace         string    `json:"namespace"`
	Kind              string    `json:"kind"`
	Component         string    `json:"component"`
	Host              string    `json:"host"`
	Reason            string    `json:"reason"`
	Status            string    `json:"status"`
	Name              string    `json:"name"`
	message           string    `json:"message"`
	ResourceRevision  string    `json:"resourceRevision"`
	CreationTimeStamp v1.Time   `json:"creationTimeStamp"`
	UID               types.UID `json:"uid"`
}

var m = map[string]string{
	"created": "Normal",
	"deleted": "Danger",
	"updated": "Warning",
}

// New create new KubewatchEvent
func New(obj interface{}, action string) Event {
	var namespace, kind, component, host, reason, status, name string
	var eventMetaData *EventMetaData
	objectMeta := utils.GetObjectMetaData(obj)
	namespace = objectMeta.Namespace
	name = objectMeta.Name
	reason = action
	status = m[action]
	fmt.Printf("KubeWatchEvent %+v\n", obj)
	switch object := obj.(type) {
	case *ext_v1beta1.DaemonSet:
		kind = "daemon set"
	case *apps_v1beta1.Deployment:
		kind = "deployment"
	case *batch_v1.Job:
		kind = "job"
	case *api_v1.Namespace:
		kind = "namespace"
	case *ext_v1beta1.Ingress:
		kind = "ingress"
	case *api_v1.PersistentVolume:
		kind = "persistent volume"
	case *api_v1.Pod:
		kind = "pod"
		host = object.Spec.NodeName
	case *api_v1.ReplicationController:
		kind = "replication controller"
	case *ext_v1beta1.ReplicaSet:
		kind = "replica set"
	case *api_v1.Service:
		kind = "service"
		component = string(object.Spec.Type)
	case *api_v1.Secret:
		kind = "secret"
	case *api_v1.ConfigMap:
		kind = "configmap"
	case *api_v1.Event:
		kind = "event"
		eventMetaData = getRolloutEventMetadata(object)
	case Event:
		name = object.Name
		kind = object.Kind
		namespace = object.Namespace
	}

	kbEvent := Event{
		Namespace: namespace,
		Kind:      kind,
		Component: component,
		Host:      host,
		Reason:    reason,
		Status:    status,
		Name:      name,
	}
	if eventMetaData != nil {
		kbEvent.Kind = eventMetaData.Kind
		kbEvent.Component = eventMetaData.SourceComponent
		kbEvent.Namespace = eventMetaData.NameSpace
		kbEvent.Reason = eventMetaData.Reason
		kbEvent.message = eventMetaData.Message
		kbEvent.ResourceRevision = eventMetaData.ResourceVersion
		kbEvent.CreationTimeStamp = eventMetaData.CreationTimeStamp
		kbEvent.Name = eventMetaData.Name
		kbEvent.UID = eventMetaData.UID
	}
	return kbEvent
}

// Message returns event message in standard format.
// included as a part of event packege to enhance code resuablity across handlers.
func (e *Event) Message() (msg string) {
	// using switch over if..else, since the format could vary based on the kind of the object in future.
	fmt.Println("Message type: " + e.Kind)
	switch e.Kind {
	case "namespace":
		msg = fmt.Sprintf(
			"A namespace `%s` has been `%s`",
			e.Name,
			e.Reason,
		)
	case "Event":
		return e.message
	default:
		msg = fmt.Sprintf(
			"A `%s` in namespace `%s` has been `%s`:\n`%s`",
			e.Kind,
			e.Namespace,
			e.Reason,
			e.Name,
		)
	}
	return msg
}

func getRolloutEventMetadata(event *api_v1.Event) *EventMetaData {
	if event.InvolvedObject.Kind == "Rollout" {
		eventMetaData := &EventMetaData{}
		eventMetaData.Name = event.InvolvedObject.Name
		eventMetaData.Kind = event.InvolvedObject.Kind
		eventMetaData.NameSpace = event.InvolvedObject.Namespace
		eventMetaData.UID = event.InvolvedObject.UID
		eventMetaData.ResourceVersion = event.InvolvedObject.ResourceVersion
		eventMetaData.CreationTimeStamp = event.ObjectMeta.CreationTimestamp
		eventMetaData.Message = event.Message
		eventMetaData.Reason = event.Reason
		eventMetaData.SourceComponent = event.Source.Component
		ann := event.Annotations
		eventMetaData.PipelineName, _ = ann["pipelineName"]
		eventMetaData.ReleaseVersion, _ = ann["releaseVersion"]
		eventMetaData.RS, _ = ann["rs"]
		return eventMetaData
	}
	return nil
}

type EventMetaData struct {
	Kind              string    `json:"kind"`
	Name              string    `json:"name"`
	NameSpace         string    `json:"nameSpace"`
	ResourceVersion   string    `json:"resourceVersion"`
	CreationTimeStamp v1.Time   `json:"creationTimeStamp"`
	SourceComponent   string    `json:"sourceComponent"`
	Reason            string    `json:"reason"`
	Message           string    `json:"message"`
	UID               types.UID `json:"uid"`
	PipelineName      string    `json:"pipelineName"`
	ReleaseVersion    string    `json:"releaseVersion"`
	RS                string    `json:"old"`
}
