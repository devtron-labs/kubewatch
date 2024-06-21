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

type Informer struct {
	logger         *zap.SugaredLogger
	client         *pubsub.PubSubClientServiceImpl
	externalConfig *ExternalConfig
}

func NewStartController(logger *zap.SugaredLogger, client *pubsub.PubSubClientServiceImpl, externalConfig *ExternalConfig) *Informer {
	return &Informer{
		logger:         logger,
		client:         client,
		externalConfig: externalConfig,
	}
}

func (impl *Informer) Start(stopChan <-chan int) {
	cfg, _ := utils.GetDefaultK8sConfig("kubeconfig")
	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		impl.logger.Error("error occurred in rest HTTPClientFor", err)
		os.Exit(2)
		return
	}
	dynamicClient, err := dynamic.NewForConfigAndClient(cfg, httpClient)
	if err != nil {
		impl.logger.Errorw("error in getting dynamic interface for resource", "err", err)
		os.Exit(2)
		return
	}
	ciCfg := &CiConfig{}
	err = env.Parse(ciCfg)
	if err != nil {
		impl.logger.Errorw("error occurred while parsing ci config", err)
		os.Exit(2)
		return
	}
	var namespace string
	clusterCfg := &ClusterConfig{}
	err = env.Parse(clusterCfg)
	if ciCfg.CiInformer {
		if impl.externalConfig.External {
			namespace = impl.externalConfig.Namespace
		} else {
			namespace = ciCfg.DefaultNamespace
		}
		stopCh := make(chan struct{})
		defer close(stopCh)
		impl.startWorkflowInformer(namespace, pubsub.WORKFLOW_STATUS_UPDATE_TOPIC, stopCh, dynamicClient, impl.externalConfig)
	}

	///-------------------
	cdCfg := &CdConfig{}
	err = env.Parse(cdCfg)
	if err != nil {
		impl.logger.Errorw("error occurred while parsing cd config", err)
		os.Exit(2)
		return
	}
	if cdCfg.CdInformer {
		if impl.externalConfig.External {
			namespace = impl.externalConfig.Namespace
		} else {
			namespace = cdCfg.DefaultNamespace
		}
		stopCh := make(chan struct{})
		defer close(stopCh)
		impl.startWorkflowInformer(namespace, pubsub.CD_WORKFLOW_STATUS_UPDATE, stopCh, dynamicClient, impl.externalConfig)
	}
	acdCfg := &AcdConfig{}
	err = env.Parse(acdCfg)
	if err != nil {
		os.Exit(2)
		return
	}

	if acdCfg.ACDInformer && !impl.externalConfig.External {
		impl.logger.Info("starting acd informer")
		clientset := versioned.NewForConfigOrDie(cfg)
		acdInformer := v1alpha12.NewApplicationInformer(clientset, acdCfg.ACDNamespace, 0, cache.Indexers{})

		acdInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				impl.logger.Debug("app added")

				if app, ok := obj.(*v1alpha1.Application); ok {
					impl.logger.Debugf("new app detected: %s, status:%s", app.Name, app.Status.Health.Status)
					//SendAppUpdate(app, client, nil)
				}
			},
			UpdateFunc: func(old interface{}, new interface{}) {
				impl.logger.Debug("app update detected")
				statusTime := time.Now()
				if oldApp, ok := old.(*v1alpha1.Application); ok {
					if newApp, ok := new.(*v1alpha1.Application); ok {
						if newApp.Status.History != nil && len(newApp.Status.History) > 0 {
							if oldApp.Status.History == nil || len(oldApp.Status.History) == 0 {
								impl.logger.Debug("new deployment detected")
								impl.SendAppUpdate(newApp, statusTime)
							} else {
								impl.logger.Debugf("old deployment detected for update: %s, status:%s", oldApp.Name, oldApp.Status.Health.Status)
								oldRevision := oldApp.Status.Sync.Revision
								newRevision := newApp.Status.Sync.Revision
								oldStatus := string(oldApp.Status.Health.Status)
								newStatus := string(newApp.Status.Health.Status)
								newSyncStatus := string(newApp.Status.Sync.Status)
								oldSyncStatus := string(oldApp.Status.Sync.Status)
								if (oldRevision != newRevision) || (oldStatus != newStatus) || (newSyncStatus != oldSyncStatus) {
									impl.SendAppUpdate(newApp, statusTime)
									impl.logger.Debug("send update app:" + oldApp.Name + ", oldRevision: " + oldRevision + ", newRevision:" +
										newRevision + ", oldStatus: " + oldStatus + ", newStatus: " + newStatus +
										", newSyncStatus: " + newSyncStatus + ", oldSyncStatus: " + oldSyncStatus)
								} else {
									impl.logger.Debug("skip updating app:" + oldApp.Name + ", oldRevision: " + oldRevision + ", newRevision:" +
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
					impl.logger.Debugf("app delete detected: %s, status:%s", app.Name, app.Status.Health.Status)
					impl.SendAppDelete(app, statusTime)
				}
			},
		})

		appStopCh := make(chan struct{})
		defer close(appStopCh)
		go acdInformer.Run(appStopCh)
	}
	<-stopChan
}

func (impl *Informer) startWorkflowInformer(namespace string, eventName string, stopCh chan struct{}, dynamicClient dynamic.Interface, externalCD *ExternalConfig) {

	workflowInformer := util.NewWorkflowInformer(dynamicClient, namespace, 0, nil, cache.Indexers{})
	impl.logger.Debugw("NewWorkflowInformer", "workflowInformer", workflowInformer)
	workflowInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {},
		UpdateFunc: func(oldWf, newWf interface{}) {
			impl.logger.Info("workflow update detected")
			if workflow, ok := newWf.(*unstructured.Unstructured).Object["status"]; ok {
				wfJson, err := json.Marshal(workflow)
				if err != nil {
					impl.logger.Errorw("error occurred while marshalling workflow", "err", err)
					return
				}
				impl.logger.Debugw("sending workflow update event ", "wfJson", string(wfJson))
				var reqBody = []byte(wfJson)
				if externalCD.External {
					err = PublishEventsOnRest(reqBody, eventName, externalCD)
				} else {
					if impl.client == nil {
						impl.logger.Warn("don't publish")
						return
					}
					err = impl.client.Publish(eventName, string(reqBody))
				}
				if err != nil {
					impl.logger.Errorw("Error while publishing Request", "err ", err)
					return
				}
				impl.logger.Debug("workflow update sent")
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

func (impl *Informer) SendAppUpdate(app *v1alpha1.Application, statusTime time.Time) {
	if impl.client == nil {
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

	err = impl.client.Publish(pubsub.APPLICATION_STATUS_UPDATE_TOPIC, string(reqBody))
	if err != nil {
		log.Println("Error while publishing Request", err)
		return
	}
	log.Println("app update sent for app: " + app.Name)
}

func (impl *Informer) SendAppDelete(app *v1alpha1.Application, statusTime time.Time) {
	if impl.client == nil {
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

	err = impl.client.Publish(pubsub.APPLICATION_STATUS_DELETE_TOPIC, string(reqBody))
	if err != nil {
		log.Println("Error while publishing Request", err)
		return
	}
	log.Println("app update sent for app: " + app.Name)
}
