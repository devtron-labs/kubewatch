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

package controller

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"syscall"
	"time"

	v1alpha12 "github.com/argoproj/argo-cd/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/pkg/client/clientset/versioned"
	"github.com/argoproj/argo-cd/pkg/client/informers/externalversions/application/v1alpha1"
	"github.com/argoproj/argo/workflow/util"
	"github.com/caarlos0/env"
	"github.com/go-resty/resty/v2"
	"github.com/hashicorp/go-uuid"
	"github.com/nats-io/nats.go"
	"github.com/robfig/cron/v3"
	v13 "k8s.io/api/batch/v1"
	"k8s.io/api/extensions/v1beta1"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/devtron-labs/kubewatch/config"
	"github.com/devtron-labs/kubewatch/pkg/event"
	"github.com/devtron-labs/kubewatch/pkg/handlers"
	"github.com/devtron-labs/kubewatch/pkg/utils"
	"github.com/sirupsen/logrus"

	_ "github.com/argoproj/argo-cd/util/session"
	v1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const maxRetries = 5

var serverStartTime time.Time

// Event indicate the informerEvent
type Event struct {
	key          string
	eventType    string
	namespace    string
	resourceType string
}

type CronEvent struct {
	EventName     string            `json:"eventName"`
	EventTypeId   int               `json:"eventTypeId"`
	CorrelationId string            `json:"correlationId"`
	EventTime     string            `json:"eventTime"`
	Payload       map[string]string `json:"payload"`
}

type WorkflowUpdateReq struct {
	Key  string `json:"key"`
	Type string `json:"type"`
}

// Controller object
type Controller struct {
	logger       *logrus.Entry
	clientset    kubernetes.Interface
	queue        workqueue.RateLimitingInterface
	informer     cache.SharedIndexInformer
	eventHandler handlers.Handler
}

type PubSubClient struct {
	Conn       *nats.Conn
	JetStrCtxt nats.JetStreamContext
}

type PubSubConfig struct {
	NatsServerHost string `env:"NATS_SERVER_HOST" envDefault:"nats://devtron-nats.devtroncd:4222"`
}

type CiConfig struct {
	DefaultNamespace string `env:"DEFAULT_NAMESPACE" envDefault:"devtron-ci"`
	CiInformer       bool   `env:"CI_INFORMER" envDefault:"true"`
}

type CdConfig struct {
	DefaultNamespace string `env:"CD_DEFAULT_NAMESPACE" envDefault:"devtron-cd"`
	CdInformer       bool   `env:"CD_INFORMER" envDefault:"true"`
}

type ExternalCdConfig struct {
	External    bool   `env:"CD_EXTERNAL_REST_LISTENER" envDefault:"false"`
	Token       string `env:"CD_EXTERNAL_ORCHESTRATOR_TOKEN" envDefault:""`
	ListenerUrl string `env:"CD_EXTERNAL_LISTENER_URL" envDefault:"http://devtroncd-orchestrator-service-prod.devtroncd:80"`
	Namespace   string `env:"CD_EXTERNAL_NAMESPACE" envDefault:""`
}

type AcdConfig struct {
	ACDNamespace string `env:"ACD_NAMESPACE" envDefault:"devtroncd"`
	ACDInformer  bool   `env:"ACD_INFORMER" envDefault:"true"`
}

type EventType int

const Trigger EventType = 1
const Success EventType = 2
const Fail EventType = 3

const cronMinuteWiseEventName string = "minute-event"

var client *PubSubClient

func Start(conf *config.Config, eventHandler handlers.Handler) {
	var kubeClient kubernetes.Interface
	//cfg, _ := getDevConfig()
	cfg, err := rest.InClusterConfig()
	if err != nil {
		kubeClient = utils.GetClientOutOfCluster()
	} else {
		kubeClient = utils.GetClient()
	}
	if conf.Resource.Pod {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options v12.ListOptions) (runtime.Object, error) {
					return kubeClient.CoreV1().Pods(conf.Namespace).List(options)
				},
				WatchFunc: func(options v12.ListOptions) (watch.Interface, error) {
					return kubeClient.CoreV1().Pods(conf.Namespace).Watch(options)
				},
			},
			&v1.Pod{},
			0, //Skip resync
			cache.Indexers{},
		)

		c := newResourceController(kubeClient, eventHandler, informer, "pod")
		stopCh := make(chan struct{})
		defer close(stopCh)

		go c.Run(stopCh)
	}

	if conf.Resource.DaemonSet {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options v12.ListOptions) (runtime.Object, error) {
					return kubeClient.ExtensionsV1beta1().DaemonSets(conf.Namespace).List(options)
				},
				WatchFunc: func(options v12.ListOptions) (watch.Interface, error) {
					return kubeClient.ExtensionsV1beta1().DaemonSets(conf.Namespace).Watch(options)
				},
			},
			&v1beta1.DaemonSet{},
			0, //Skip resync
			cache.Indexers{},
		)

		c := newResourceController(kubeClient, eventHandler, informer, "daemonset")
		stopCh := make(chan struct{})
		defer close(stopCh)

		go c.Run(stopCh)
	}

	if conf.Resource.ReplicaSet {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options v12.ListOptions) (runtime.Object, error) {
					return kubeClient.ExtensionsV1beta1().ReplicaSets(conf.Namespace).List(options)
				},
				WatchFunc: func(options v12.ListOptions) (watch.Interface, error) {
					return kubeClient.ExtensionsV1beta1().ReplicaSets(conf.Namespace).Watch(options)
				},
			},
			&v1beta1.ReplicaSet{},
			0, //Skip resync
			cache.Indexers{},
		)

		c := newResourceController(kubeClient, eventHandler, informer, "replicaset")
		stopCh := make(chan struct{})
		defer close(stopCh)

		go c.Run(stopCh)
	}

	if conf.Resource.Services {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options v12.ListOptions) (runtime.Object, error) {
					return kubeClient.CoreV1().Services(conf.Namespace).List(options)
				},
				WatchFunc: func(options v12.ListOptions) (watch.Interface, error) {
					return kubeClient.CoreV1().Services(conf.Namespace).Watch(options)
				},
			},
			&v1.Service{},
			0, //Skip resync
			cache.Indexers{},
		)

		c := newResourceController(kubeClient, eventHandler, informer, "service")
		stopCh := make(chan struct{})
		defer close(stopCh)

		go c.Run(stopCh)
	}

	if conf.Resource.Deployment {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options v12.ListOptions) (runtime.Object, error) {
					return kubeClient.AppsV1beta1().Deployments(conf.Namespace).List(options)
				},
				WatchFunc: func(options v12.ListOptions) (watch.Interface, error) {
					return kubeClient.AppsV1beta1().Deployments(conf.Namespace).Watch(options)
				},
			},
			&v1beta1.Deployment{},
			0, //Skip resync
			cache.Indexers{},
		)

		c := newResourceController(kubeClient, eventHandler, informer, "deployment")
		stopCh := make(chan struct{})
		defer close(stopCh)

		go c.Run(stopCh)
	}

	if conf.Resource.Namespace {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options v12.ListOptions) (runtime.Object, error) {
					return kubeClient.CoreV1().Namespaces().List(options)
				},
				WatchFunc: func(options v12.ListOptions) (watch.Interface, error) {
					return kubeClient.CoreV1().Namespaces().Watch(options)
				},
			},
			&v1.Namespace{},
			0, //Skip resync
			cache.Indexers{},
		)

		c := newResourceController(kubeClient, eventHandler, informer, "namespace")
		stopCh := make(chan struct{})
		defer close(stopCh)

		go c.Run(stopCh)
	}

	if conf.Resource.ReplicationController {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options v12.ListOptions) (runtime.Object, error) {
					return kubeClient.CoreV1().ReplicationControllers(conf.Namespace).List(options)
				},
				WatchFunc: func(options v12.ListOptions) (watch.Interface, error) {
					return kubeClient.CoreV1().ReplicationControllers(conf.Namespace).Watch(options)
				},
			},
			&v1.ReplicationController{},
			0, //Skip resync
			cache.Indexers{},
		)

		c := newResourceController(kubeClient, eventHandler, informer, "replication controller")
		stopCh := make(chan struct{})
		defer close(stopCh)

		go c.Run(stopCh)
	}

	if conf.Resource.Job {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options v12.ListOptions) (runtime.Object, error) {
					return kubeClient.BatchV1().Jobs(conf.Namespace).List(options)
				},
				WatchFunc: func(options v12.ListOptions) (watch.Interface, error) {
					return kubeClient.BatchV1().Jobs(conf.Namespace).Watch(options)
				},
			},
			&v13.Job{},
			0, //Skip resync
			cache.Indexers{},
		)

		c := newResourceController(kubeClient, eventHandler, informer, "job")
		stopCh := make(chan struct{})
		defer close(stopCh)

		go c.Run(stopCh)
	}

	if conf.Resource.PersistentVolume {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options v12.ListOptions) (runtime.Object, error) {
					return kubeClient.CoreV1().PersistentVolumes().List(options)
				},
				WatchFunc: func(options v12.ListOptions) (watch.Interface, error) {
					return kubeClient.CoreV1().PersistentVolumes().Watch(options)
				},
			},
			&v1.PersistentVolume{},
			0, //Skip resync
			cache.Indexers{},
		)

		c := newResourceController(kubeClient, eventHandler, informer, "persistent volume")
		stopCh := make(chan struct{})
		defer close(stopCh)

		go c.Run(stopCh)
	}

	if conf.Resource.Secret {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options v12.ListOptions) (runtime.Object, error) {
					return kubeClient.CoreV1().Secrets(conf.Namespace).List(options)
				},
				WatchFunc: func(options v12.ListOptions) (watch.Interface, error) {
					return kubeClient.CoreV1().Secrets(conf.Namespace).Watch(options)
				},
			},
			&v1.Secret{},
			0, //Skip resync
			cache.Indexers{},
		)

		c := newResourceController(kubeClient, eventHandler, informer, "secret")
		stopCh := make(chan struct{})
		defer close(stopCh)

		go c.Run(stopCh)
	}

	if conf.Resource.ConfigMap {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options v12.ListOptions) (runtime.Object, error) {
					return kubeClient.CoreV1().ConfigMaps(conf.Namespace).List(options)
				},
				WatchFunc: func(options v12.ListOptions) (watch.Interface, error) {
					return kubeClient.CoreV1().ConfigMaps(conf.Namespace).Watch(options)
				},
			},
			&v1.ConfigMap{},
			0, //Skip resync
			cache.Indexers{},
		)

		c := newResourceController(kubeClient, eventHandler, informer, "configmap")
		stopCh := make(chan struct{})
		defer close(stopCh)

		go c.Run(stopCh)
	}

	if conf.Resource.Ingress {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options v12.ListOptions) (runtime.Object, error) {
					return kubeClient.ExtensionsV1beta1().Ingresses(conf.Namespace).List(options)
				},
				WatchFunc: func(options v12.ListOptions) (watch.Interface, error) {
					return kubeClient.ExtensionsV1beta1().Ingresses(conf.Namespace).Watch(options)
				},
			},
			&v1beta1.Ingress{},
			0, //Skip resync
			cache.Indexers{},
		)

		c := newResourceController(kubeClient, eventHandler, informer, "ingress")
		stopCh := make(chan struct{})
		defer close(stopCh)

		go c.Run(stopCh)
	}

	if conf.Resource.Events {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(options v12.ListOptions) (runtime.Object, error) {
					return kubeClient.CoreV1().Events(conf.Namespace).List(options)
				},
				WatchFunc: func(options v12.ListOptions) (watch.Interface, error) {
					return kubeClient.CoreV1().Events(conf.Namespace).Watch(options)
				},
			},
			&v1.Event{},
			0, //Skip resync
			cache.Indexers{},
		)

		c := newResourceController(kubeClient, eventHandler, informer, "event")
		stopCh := make(chan struct{})
		defer close(stopCh)

		go c.Run(stopCh)
	}

	externalCD := &ExternalCdConfig{}
	err = env.Parse(externalCD)
	if err != nil {
		log.Panic("err", err)
	}

	if !externalCD.External {
		client, err = NewPubSubClient()
		if err != nil {
			log.Panic("err", err)
		}

		ciCfg := &CiConfig{}
		err = env.Parse(ciCfg)
		if err != nil {
			log.Panic("err", err)
		}

		if ciCfg.CiInformer {

			informer := util.NewWorkflowInformer(cfg, ciCfg.DefaultNamespace, 0, nil)
			informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				// When a new wf gets created
				AddFunc: func(obj interface{}) {
					log.Println("workflow created")
				},
				// When a wf gets updated
				UpdateFunc: func(oldWf interface{}, newWf interface{}) {
					log.Println("workflow update detected")
					if workflow, ok := newWf.(*unstructured.Unstructured).Object["status"]; ok {
						wfJson, err := json.Marshal(workflow)
						if err != nil {
							log.Println("err", err)
							return
						}
						log.Println("sending workflow update event ", string(wfJson))
						var reqBody = []byte(wfJson)
						if client == nil {
							log.Println("dont't publish")
							return
						}
						err = AddStream(client.JetStrCtxt, KUBEWATCH_STREAM)
						if err != nil {
							log.Fatal("Error while adding stream", "err", err)
						}
						//Generate random string for passing as Header Id in message
						randString := "MsgHeaderId-" + utils.Generate(10)
						_, err = client.JetStrCtxt.Publish(WORKFLOW_STATUS_UPDATE_TOPIC, reqBody, nats.MsgId(randString))
						if err != nil {
							log.Println("Error while publishing Request", err)
							return
						}
						log.Println("workflow update sent")
					}
				},
				// When a wf gets deleted
				DeleteFunc: func(wf interface{}) {},
			})

			stopCh := make(chan struct{})
			defer close(stopCh)
			go informer.Run(stopCh)
		}

		///-------------------
		cdCfg := &CdConfig{}
		err = env.Parse(cdCfg)
		if err != nil {
			log.Panic("err", err)
		}

		if cdCfg.CdInformer {

			informer := util.NewWorkflowInformer(cfg, cdCfg.DefaultNamespace, 0, nil)
			informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				// When a new wf gets created
				AddFunc: func(obj interface{}) {
					log.Println("cd workflow created")
				},
				// When a wf gets updated
				UpdateFunc: func(oldWf interface{}, newWf interface{}) {
					log.Println("cd workflow update detected")
					if workflow, ok := newWf.(*unstructured.Unstructured).Object["status"]; ok {
						wfJson, err := json.Marshal(workflow)
						if err != nil {
							log.Println("err", err)
							return
						}
						log.Println("sending cd workflow update event ", string(wfJson))
						var reqBody = []byte(wfJson)
						if client == nil {
							log.Println("dont't publish")
							return
						}
						err = AddStream(client.JetStrCtxt, KUBEWATCH_STREAM)
						if err != nil {
							log.Fatal("Error while adding stream", "error", err)
						}
						//Generate random string for passing as Header Id in message
						randString := "MsgHeaderId-" + utils.Generate(10)
						_, err = client.JetStrCtxt.Publish(CD_WORKFLOW_STATUS_UPDATE, reqBody, nats.MsgId(randString))
						if err != nil {
							log.Println("Error while publishing Request", err)
							return
						}
						log.Println("cd workflow update sent")
					}
				},
				// When a wf gets deleted
				DeleteFunc: func(wf interface{}) {},
			})

			stopCh := make(chan struct{})
			defer close(stopCh)
			go informer.Run(stopCh)
		}

		acdCfg := &AcdConfig{}
		err = env.Parse(acdCfg)
		if err != nil {
			return
		}

		if acdCfg.ACDInformer {
			log.Println("starting acd informer")
			clientset := versioned.NewForConfigOrDie(cfg)
			acdInformer := v1alpha1.NewApplicationInformer(clientset, acdCfg.ACDNamespace, 0, nil)

			acdInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj interface{}) {
					log.Println("app added")
					if app, ok := obj.(*v1alpha12.Application); ok {
						log.Println("new app detected: " + app.Name + " " + app.Status.Health.Status)
						SendAppUpdate(app, client, nil)
					}
				},
				UpdateFunc: func(old interface{}, new interface{}) {
					log.Println("app update detected")
					if oldApp, ok := old.(*v1alpha12.Application); ok {
						if newApp, ok := new.(*v1alpha12.Application); ok {
							if newApp.Status.History != nil && len(newApp.Status.History) > 0 {
								if oldApp.Status.History == nil || len(oldApp.Status.History) == 0 {
									log.Println("new deployment detected")
									SendAppUpdate(newApp, client, nil)
								} else {
									log.Println("old deployment detected for update: name:" + oldApp.Name + ", status:" + oldApp.Status.Health.Status)
									oldRevision := oldApp.Status.Sync.Revision
									newRevision := newApp.Status.Sync.Revision
									oldReconciledAt := oldApp.Status.ReconciledAt
									newReconciledAt := newApp.Status.ReconciledAt
									oldStatus := oldApp.Status.Sync.Status
									newStatus := newApp.Status.Sync.Status
									if (oldRevision != newRevision) || (oldReconciledAt != newReconciledAt) || (oldStatus != newStatus) {
										SendAppUpdate(newApp, client, oldApp)
									} else {
										log.Println("skip updating old app as old and new revision mismatch:" + oldApp.Name + ", newRevision:" + newRevision)
									}
								}
							}
							if oldApp.Status.Health.Status == newApp.Status.Health.Status {
								return
							}
							SendAppUpdate(newApp, client, oldApp)
						} else {
							log.Println("app update detected, but skip updating, there is no new app")
						}
					} else {
						log.Println("app update detected, but skip updating, there is no old app")
					}
				},
				DeleteFunc: func(obj interface{}) {},
			})

			appStopCh := make(chan struct{})
			defer close(appStopCh)
			go acdInformer.Run(appStopCh)

			c := cron.New()
			_, err := c.AddFunc("@every 1m", FireDailyMinuteEvent)
			if err != nil {
				log.Panic("cannot start daily cron, err ", err)
			}
			go c.Start()
		}

	}
	///------------

	if externalCD.External {
		log.Println("applying listner for external")
		informer := util.NewWorkflowInformer(cfg, externalCD.Namespace, 0, nil)
		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			// When a new wf gets created
			AddFunc: func(obj interface{}) {
				log.Println("external cd workflow created")
			},
			// When a wf gets updated
			UpdateFunc: func(oldWf interface{}, newWf interface{}) {
				//TODO apply filter for devtron
				log.Println("external wf event received")
				if workflow, ok := newWf.(*unstructured.Unstructured).Object["status"]; ok {
					wfJson, err := json.Marshal(workflow)
					if err != nil {
						log.Println("err", err)
						return
					}
					log.Println("sending external cd workflow update event ", string(wfJson))
					var reqBody = []byte(wfJson)

					err = PublishEventsOnRest(reqBody, CD_WORKFLOW_STATUS_UPDATE, externalCD)
					if err != nil {
						log.Println("publish cd err", "err", err)
						return
					}
					log.Println("external cd workflow update sent")
				}
			},
			// When a wf gets deleted
			DeleteFunc: func(wf interface{}) {},
		})

		stopCh := make(chan struct{})
		defer close(stopCh)
		go informer.Run(stopCh)
	}

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	signal.Notify(sigterm, syscall.SIGINT)
	<-sigterm
}

type PublishRequest struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
}

func PublishEventsOnRest(jsonBody []byte, topic string, externalCdConfig *ExternalCdConfig) error {
	publishRequest := &PublishRequest{
		Topic:   topic,
		Payload: jsonBody,
	}
	client := resty.New().SetDebug(true)
	client.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true})
	resp, err := client.SetRetryCount(4).R().
		SetHeader("Content-Type", "application/json").
		SetBody(publishRequest).
		SetAuthToken(externalCdConfig.Token).
		//SetResult().    // or SetResult(AuthSuccess{}).
		Post(externalCdConfig.ListenerUrl)

	if err != nil {
		log.Println("err in publishing over rest", "token ", externalCdConfig.Token, "body", publishRequest, err)
		return err
	}
	log.Println("res ", string(resp.Body()))
	return nil
}

func FireDailyMinuteEvent() {
	correlationId, _ := uuid.GenerateUUID()
	event := CronEvent{
		EventName:     cronMinuteWiseEventName,
		EventTypeId:   int(Trigger),
		CorrelationId: fmt.Sprintf("%s", correlationId),
		EventTime:     time.Now().Format("2006-01-02 15:04:05"),
		Payload:       map[string]string{},
	}
	eventJson, err := json.Marshal(event)
	if err != nil {
		log.Println("err", err)
		return
	}
	log.Println("cron event", string(eventJson))
	var reqBody = []byte(eventJson)

	err = AddStream(client.JetStrCtxt, KUBEWATCH_STREAM)
	if err != nil {
		log.Fatal("Error while adding stream", "error", err)
	}

	//Generate random string for passing as Header Id in message
	randString := "MsgHeaderId-" + utils.Generate(10)
	_, err = client.JetStrCtxt.Publish(CRON_EVENTS, reqBody, nats.MsgId(randString))
	if err != nil {
		log.Println("Error while publishing Request", err)
		return
	}
	log.Println("cron event sent")
}

type ApplicationDetail struct {
	Application    *v1alpha12.Application `json:"application"`
	OldApplication *v1alpha12.Application `json:"oldApplication"`
}

func SendAppUpdate(app *v1alpha12.Application, client *PubSubClient, oldApp *v1alpha12.Application) {
	if client == nil {
		log.Println("client is nil, don't send update")
		return
	}
	var newAppCopy, oldAppCopy *v1alpha12.Application

	if app != nil {
		newAppCopy = app.DeepCopy()
		//nil check not required for `newAppCopy.Status` as its object
		newAppCopy.Status.Resources = nil
		if newAppCopy.Status.OperationState != nil {
			newAppCopy.Status.OperationState.SyncResult = nil
		}
	}
	if oldApp != nil {
		oldAppCopy = oldApp.DeepCopy()
		//nil check not required for `newAppCopy.Status` as its object
		oldAppCopy.Status.Resources = nil
		if oldAppCopy.Status.OperationState != nil {
			oldAppCopy.Status.OperationState.SyncResult = nil
		}
	}

	appDetail := ApplicationDetail{
		Application:    newAppCopy,
		OldApplication: oldAppCopy,
	}
	appJson, err := json.Marshal(appDetail)
	if err != nil {
		log.Println("marshal error on sending app update", err)
		return
	}
	log.Println("app update event for publish: ", string(appJson))
	var reqBody = []byte(appJson)

	err = AddStream(client.JetStrCtxt, KUBEWATCH_STREAM)
	if err != nil {
		log.Fatal("Error while adding stream", "error", err)
	}

	//Generate random string for passing as Header Id in message
	randString := "MsgHeaderId-" + utils.Generate(10)
	_, err = client.JetStrCtxt.Publish(APPLICATION_STATUS_UPDATE_TOPIC, reqBody, nats.MsgId(randString))
	if err != nil {
		log.Println("Error while publishing Request", err)
		return
	}
	log.Println("app update sent for app: " + app.Name)
}

func NewPubSubClient() (*PubSubClient, error) {
	cfg := &PubSubConfig{}
	err := env.Parse(cfg)
	if err != nil {
		return &PubSubClient{}, err
	}
	nc, err := nats.Connect(cfg.NatsServerHost,
		nats.ReconnectWait(10*time.Second), nats.MaxReconnects(100),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			log.Println("Nats Connection got disconnected!", "Reason", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Println("Nats Connection got reconnected", "url", nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			log.Println("Nats Client Connection closed!", "Reason", nc.LastError())
		}))
	if err != nil {
		log.Println("err", err)
		return &PubSubClient{}, err
	}
	//create a jetstream context
	js, err := nc.JetStream()

	if err != nil {
		log.Println("err while creating jetstream context", err)
	}

	natsClient := &PubSubClient{
		Conn:       nc,
		JetStrCtxt: js,
	}
	return natsClient, nil
}

func newResourceController(client kubernetes.Interface, eventHandler handlers.Handler, informer cache.SharedIndexInformer, resourceType string) *Controller {
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	var newEvent Event
	var err error
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			newEvent.key, err = cache.MetaNamespaceKeyFunc(obj)
			newEvent.eventType = "create"
			newEvent.resourceType = resourceType
			logrus.WithField("pkg", "kubewatch-"+resourceType).Infof("Processing add to %v: %s", resourceType, newEvent.key)
			if err == nil {
				queue.Add(newEvent)
			}
		},
		UpdateFunc: func(old, new interface{}) {
			newEvent.key, err = cache.MetaNamespaceKeyFunc(old)
			newEvent.eventType = "update"
			newEvent.resourceType = resourceType
			logrus.WithField("pkg", "kubewatch-"+resourceType).Infof("Processing update to %v: %s", resourceType, newEvent.key)
			if err == nil {
				queue.Add(newEvent)
			}
		},
		DeleteFunc: func(obj interface{}) {
			newEvent.key, err = cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			newEvent.eventType = "delete"
			newEvent.resourceType = resourceType
			newEvent.namespace = utils.GetObjectMetaData(obj).Namespace
			logrus.WithField("pkg", "kubewatch-"+resourceType).Infof("Processing delete to %v: %s", resourceType, newEvent.key)
			if err == nil {
				queue.Add(newEvent)
			}
		},
	})

	return &Controller{
		logger:       logrus.WithField("pkg", "kubewatch-"+resourceType),
		clientset:    client,
		informer:     informer,
		queue:        queue,
		eventHandler: eventHandler,
	}
}

// Run starts the kubewatch controller
func (c *Controller) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	c.logger.Info("Starting kubewatch controller")
	serverStartTime = time.Now().Local()

	go c.informer.Run(stopCh)

	if !cache.WaitForCacheSync(stopCh, c.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	c.logger.Info("Kubewatch controller synced and ready")

	wait.Until(c.runWorker, time.Second, stopCh)
}

// HasSynced is required for the cache.Controller interface.
func (c *Controller) HasSynced() bool {
	return c.informer.HasSynced()
}

// LastSyncResourceVersion is required for the cache.Controller interface.
func (c *Controller) LastSyncResourceVersion() string {
	return c.informer.LastSyncResourceVersion()
}

func (c *Controller) runWorker() {
	for c.processNextItem() {
		// continue looping
	}
}

func (c *Controller) processNextItem() bool {
	newEvent, quit := c.queue.Get()

	if quit {
		return false
	}
	defer c.queue.Done(newEvent)
	err := c.processItem(newEvent.(Event))
	if err == nil {
		// No error, reset the ratelimit counters
		c.queue.Forget(newEvent)
	} else if c.queue.NumRequeues(newEvent) < maxRetries {
		c.logger.Errorf("Error processing %s (will retry): %v", newEvent.(Event).key, err)
		c.queue.AddRateLimited(newEvent)
	} else {
		// err != nil and too many retries
		c.logger.Errorf("Error processing %s (giving up): %v", newEvent.(Event).key, err)
		c.queue.Forget(newEvent)
		utilruntime.HandleError(err)
	}

	return true
}

/* TODOs
- Enhance event creation using client-side cacheing machanisms - pending
- Enhance the processItem to classify events - done
- Send alerts correspoding to events - done
*/

func (c *Controller) processItem(newEvent Event) error {
	obj, _, err := c.informer.GetIndexer().GetByKey(newEvent.key)
	if err != nil {
		return fmt.Errorf("Error fetching object with key %s from store: %v", newEvent.key, err)
	}
	// get object's metedata
	objectMeta := utils.GetObjectMetaData(obj)
	c.logger.Errorf("Processing Item %+v\n", obj)
	fmt.Printf("Processing Item %+v\n", obj)
	// process events based on its type
	switch newEvent.eventType {
	case "create":
		// compare CreationTimestamp and serverStartTime and alert only on latest events
		// Could be Replaced by using Delta or DeltaFIFO
		if objectMeta.CreationTimestamp.Sub(serverStartTime).Seconds() > 0 {
			c.eventHandler.ObjectCreated(obj)
			return nil
		}
	case "update":
		/* TODOs
		- enahace update event processing in such a way that, it send alerts about what got changed.
		*/
		kbEvent := event.Event{
			Kind: newEvent.resourceType,
			Name: newEvent.key,
		}
		c.eventHandler.ObjectUpdated(obj, kbEvent)
		return nil
	case "delete":
		kbEvent := event.Event{
			Kind:      newEvent.resourceType,
			Name:      newEvent.key,
			Namespace: newEvent.namespace,
		}
		c.eventHandler.ObjectDeleted(kbEvent)
		return nil
	}
	return nil
}

func getDevConfig() (*rest.Config, error) {
	usr, err := user.Current()
	if err != nil {
		return nil, err
	}
	kubeconfig := flag.String("kubeconfig", filepath.Join(usr.HomeDir, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	flag.Parse()
	cfg, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}
