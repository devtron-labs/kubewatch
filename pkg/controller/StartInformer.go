package controller

import (
	"crypto/tls"
	"encoding/json"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
	v1alpha12 "github.com/argoproj/argo-cd/v2/pkg/client/informers/externalversions/application/v1alpha1"
	"github.com/argoproj/argo-workflows/v3/workflow/util"
	"github.com/caarlos0/env"
	pubsub "github.com/devtron-labs/common-lib/pubsub-lib"
	"github.com/devtron-labs/kubewatch/pkg/utils"
	"github.com/go-resty/resty/v2"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type CiConfig struct {
	DefaultNamespace string `env:"DEFAULT_NAMESPACE" envDefault:"devtron-ci"`
	CiInformer       bool   `env:"CI_INFORMER" envDefault:"true"`
}

type CdConfig struct {
	DefaultNamespace string `env:"CD_DEFAULT_NAMESPACE" envDefault:"devtron-cd"`
	CdInformer       bool   `env:"CD_INFORMER" envDefault:"true"`
}

// This is being used by CI as well as CD
type ExternalConfig struct {
	External    bool   `env:"CD_EXTERNAL_REST_LISTENER" envDefault:"false"`
	Token       string `env:"CD_EXTERNAL_ORCHESTRATOR_TOKEN" envDefault:""`
	ListenerUrl string `env:"CD_EXTERNAL_LISTENER_URL" envDefault:"http://devtroncd-orchestrator-service-prod.devtroncd:80"`
	Namespace   string `env:"CD_EXTERNAL_NAMESPACE" envDefault:""`
}

type AcdConfig struct {
	ACDNamespace string `env:"ACD_NAMESPACE" envDefault:"devtroncd"`
	ACDInformer  bool   `env:"ACD_INFORMER" envDefault:"true"`
}

type ClusterConfig struct {
	ClusterType string `env:"CLUSTER_TYPE" envDefault:"IN_CLUSTER"`
}

type EventType int

const Trigger EventType = 1
const Success EventType = 2
const Fail EventType = 3

const cronMinuteWiseEventName string = "minute-event"

const ClusterTypeAll string = "ALL_CLUSTER"

type StartInformer struct {
	logger *zap.SugaredLogger
	client *pubsub.PubSubClientServiceImpl
}

func NewStartController(logger *zap.SugaredLogger, client *pubsub.PubSubClientServiceImpl) *StartInformer {
	return &StartInformer{
		logger: logger,
		client: client,
	}
}

func (si *StartInformer) Start() {
	cfg, _ := utils.GetDefaultK8sConfig("kubeconfig")
	externalConfig := &ExternalConfig{}
	err := env.Parse(externalConfig)
	if err != nil {
		si.logger.Fatal("error occurred while parsing external cd config", err)
	}
	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		si.logger.Error("error occurred in rest HTTPClientFor", err)
		return
	}
	dynamicClient, err := dynamic.NewForConfigAndClient(cfg, httpClient)
	if err != nil {
		si.logger.Errorw("error in getting dynamic interface for resource", "err", err)
		return
	}
	ciCfg := &CiConfig{}
	err = env.Parse(ciCfg)
	if err != nil {
		si.logger.Fatal("error occurred while parsing ci config", err)
	}
	var namespace string
	clusterCfg := &ClusterConfig{}
	err = env.Parse(clusterCfg)
	if ciCfg.CiInformer {
		if externalConfig.External {
			namespace = externalConfig.Namespace
		} else {
			namespace = ciCfg.DefaultNamespace
		}
		stopCh := make(chan struct{})
		defer close(stopCh)
		si.startWorkflowInformer(namespace, pubsub.WORKFLOW_STATUS_UPDATE_TOPIC, stopCh, dynamicClient, externalConfig)
	}

	///-------------------
	cdCfg := &CdConfig{}
	err = env.Parse(cdCfg)
	if err != nil {
		si.logger.Fatal("error occurred while parsing cd config", err)
	}
	if cdCfg.CdInformer {
		if externalConfig.External {
			namespace = externalConfig.Namespace
		} else {
			namespace = cdCfg.DefaultNamespace
		}
		stopCh := make(chan struct{})
		defer close(stopCh)
		si.startWorkflowInformer(namespace, pubsub.CD_WORKFLOW_STATUS_UPDATE, stopCh, dynamicClient, externalConfig)
	}
	acdCfg := &AcdConfig{}
	err = env.Parse(acdCfg)
	if err != nil {
		return
	}

	if acdCfg.ACDInformer && !externalConfig.External {
		si.logger.Info("starting acd informer")
		clientset := versioned.NewForConfigOrDie(cfg)
		acdInformer := v1alpha12.NewApplicationInformer(clientset, acdCfg.ACDNamespace, 0, cache.Indexers{})

		acdInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				si.logger.Debug("app added")

				if app, ok := obj.(*v1alpha1.Application); ok {
					si.logger.Debugf("new app detected: %s, status:%s", app.Name, app.Status.Health.Status)
					//SendAppUpdate(app, client, nil)
				}
			},
			UpdateFunc: func(old interface{}, new interface{}) {
				si.logger.Debug("app update detected")
				statusTime := time.Now()
				if oldApp, ok := old.(*v1alpha1.Application); ok {
					if newApp, ok := new.(*v1alpha1.Application); ok {
						if newApp.Status.History != nil && len(newApp.Status.History) > 0 {
							if oldApp.Status.History == nil || len(oldApp.Status.History) == 0 {
								si.logger.Debug("new deployment detected")
								si.SendAppUpdate(newApp, statusTime)
							} else {
								si.logger.Debugf("old deployment detected for update: %s, status:%s", oldApp.Name, oldApp.Status.Health.Status)
								oldRevision := oldApp.Status.Sync.Revision
								newRevision := newApp.Status.Sync.Revision
								oldStatus := string(oldApp.Status.Health.Status)
								newStatus := string(newApp.Status.Health.Status)
								newSyncStatus := string(newApp.Status.Sync.Status)
								oldSyncStatus := string(oldApp.Status.Sync.Status)
								if (oldRevision != newRevision) || (oldStatus != newStatus) || (newSyncStatus != oldSyncStatus) {
									si.SendAppUpdate(newApp, statusTime)
									si.logger.Debug("send update app:" + oldApp.Name + ", oldRevision: " + oldRevision + ", newRevision:" +
										newRevision + ", oldStatus: " + oldStatus + ", newStatus: " + newStatus +
										", newSyncStatus: " + newSyncStatus + ", oldSyncStatus: " + oldSyncStatus)
								} else {
									si.logger.Debug("skip updating app:" + oldApp.Name + ", oldRevision: " + oldRevision + ", newRevision:" +
										newRevision + ", oldStatus: " + oldStatus + ", newStatus: " + newStatus +
										", newSyncStatus: " + newSyncStatus + ", oldSyncStatus: " + oldSyncStatus)
								}
							}
						}
					} else {
						log.Println("app update detected, but skip updating, there is no new app")
					}
				} else {
					log.Println("app update detected, but skip updating, there is no old app")
				}
			},
			DeleteFunc: func(obj interface{}) {
				if app, ok := obj.(*v1alpha1.Application); ok {
					statusTime := time.Now()
					si.logger.Debugf("app delete detected: %s, status:%s", app.Name, app.Status.Health.Status)
					si.SendAppDelete(app, statusTime)
				}
			},
		})

		appStopCh := make(chan struct{})
		defer close(appStopCh)
		go acdInformer.Run(appStopCh)
	}

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	signal.Notify(sigterm, syscall.SIGINT)
	<-sigterm
}

func (si *StartInformer) startWorkflowInformer(namespace string, eventName string, stopCh chan struct{}, dynamicClient dynamic.Interface, externalCD *ExternalConfig) {

	workflowInformer := util.NewWorkflowInformer(dynamicClient, namespace, 0, nil, cache.Indexers{})
	si.logger.Debugw("NewWorkflowInformer", "workflowInformer", workflowInformer)
	workflowInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {},
		UpdateFunc: func(oldWf, newWf interface{}) {
			si.logger.Info("workflow update detected")
			if workflow, ok := newWf.(*unstructured.Unstructured).Object["status"]; ok {
				wfJson, err := json.Marshal(workflow)
				if err != nil {
					si.logger.Errorw("error occurred while marshalling workflow", "err", err)
					return
				}
				si.logger.Debugw("sending workflow update event ", "wfJson", string(wfJson))
				var reqBody = []byte(wfJson)
				if externalCD.External {
					err = PublishEventsOnRest(reqBody, eventName, externalCD)
				} else {
					if si.client == nil {
						si.logger.Warn("don't publish")
						return
					}
					err = si.client.Publish(eventName, string(reqBody))
				}
				if err != nil {
					si.logger.Errorw("Error while publishing Request", "err ", err)
					return
				}
				si.logger.Debug("workflow update sent")
			}
		},
		DeleteFunc: func(wf interface{}) {},
	})

	go workflowInformer.Run(stopCh)

}

type PublishRequest struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
}

func PublishEventsOnRest(jsonBody []byte, topic string, externalCdConfig *ExternalConfig) error {
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

type ApplicationDetail struct {
	Application *v1alpha1.Application `json:"application"`
	StatusTime  time.Time             `json:"statusTime"`
}

func (si *StartInformer) SendAppUpdate(app *v1alpha1.Application, statusTime time.Time) {
	if si.client == nil {
		log.Println("client is nil, don't send update")
		return
	}
	appDetail := ApplicationDetail{
		Application: app,
		StatusTime:  statusTime,
	}
	appJson, err := json.Marshal(appDetail)
	if err != nil {
		log.Println("marshal error on sending app update", err)
		return
	}
	log.Println("app update event for publish: ", string(appJson))
	var reqBody = []byte(appJson)

	err = si.client.Publish(pubsub.APPLICATION_STATUS_UPDATE_TOPIC, string(reqBody))
	if err != nil {
		log.Println("Error while publishing Request", err)
		return
	}
	log.Println("app update sent for app: " + app.Name)
}

func (si *StartInformer) SendAppDelete(app *v1alpha1.Application, statusTime time.Time) {
	if si.client == nil {
		log.Println("client is nil, don't send delete update")
		return
	}
	appDetail := ApplicationDetail{
		Application: app,
		StatusTime:  statusTime,
	}
	appJson, err := json.Marshal(appDetail)
	if err != nil {
		log.Println("marshal error on sending app delete update", err)
		return
	}
	log.Println("app delete event for publish: ", string(appJson))
	var reqBody = []byte(appJson)

	err = si.client.Publish(pubsub.APPLICATION_STATUS_DELETE_TOPIC, string(reqBody))
	if err != nil {
		log.Println("Error while publishing Request", err)
		return
	}
	log.Println("app update sent for app: " + app.Name)
}
