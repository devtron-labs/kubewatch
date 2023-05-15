package informer

import (
	"errors"
	"flag"
	"fmt"
	"github.com/caarlos0/env"
	repository "github.com/devtron-labs/kubewatch/pkg/cluster"
	"go.uber.org/zap"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type ClusterInfo struct {
	ClusterId   int    `json:"clusterId"`
	ClusterName string `json:"clusterName"`
	BearerToken string `json:"bearerToken"`
	ServerUrl   string `json:"serverUrl"`
}


const (
	CLUSTER_MODIFY_EVENT_SECRET_TYPE = "cluster.request/modify"
	DEFAULT_CLUSTER                  = "default_cluster"
	INFORMER_ALREADY_EXIST_MESSAGE   = "INFORMER_ALREADY_EXIST"
	ADD                              = "add"
	UPDATE                           = "update"
)

type K8sInformer interface {
	startInformer(clusterInfo ClusterInfo) error
	syncInformer(clusterId int) error
	stopInformer(clusterName string, clusterId int)
	startInformerAndPopulateCache(clusterId int) error
}

type HelmReleaseConfig struct {
	EnableHelmReleaseCache bool `env:"ENABLE_HELM_RELEASE_CACHE" envDefault:"true"`
}

func GetHelmReleaseConfig() (*HelmReleaseConfig, error) {
	cfg := &HelmReleaseConfig{}
	err := env.Parse(cfg)
	return cfg, err
}

type K8sInformerImpl struct {
	logger             *zap.SugaredLogger
	mutex              sync.Mutex
	informerStopper    map[int]chan struct{}
	clusterRepository  repository.ClusterRepository
	helmReleaseConfig  *HelmReleaseConfig
	DevConfig *rest.Config
}

func Newk8sInformerImpl(logger *zap.SugaredLogger, clusterRepository repository.ClusterRepository) *K8sInformerImpl {
	informerFactory := &K8sInformerImpl{
		logger:            logger,
		clusterRepository: clusterRepository,
	}
	devConfig,_ := getDevConfig("kubeconfigK8s")
	informerFactory.DevConfig = devConfig
	//informerFactory.HelmListClusterMap = make(map[int]map[string]*client.DeployedAppDetail)
	informerFactory.informerStopper = make(map[int]chan struct{})
	//if helmReleaseConfig.EnableHelmReleaseCache {
	//	go informerFactory.BuildInformerForAllClusters()
	//}
	return informerFactory
}

//func decodeRelease(data string) (*release.Release, error) {
//	// base64 decode string
//	b64 := base64.StdEncoding
//	b, err := b64.DecodeString(data)
//	if err != nil {
//		return nil, err
//	}
//
//	var magicGzip = []byte{0x1f, 0x8b, 0x08}
//
//	// For backwards compatibility with releases that were stored before
//	// compression was introduced we skip decompression if the
//	// gzip magic header is not found
//	if len(b) > 3 && bytes.Equal(b[0:3], magicGzip) {
//		r, err := gzip.NewReader(bytes.NewReader(b))
//		if err != nil {
//			return nil, err
//		}
//		defer r.Close()
//		b2, err := ioutil.ReadAll(r)
//		if err != nil {
//			return nil, err
//		}
//		b = b2
//	}
//
//	var rls release.Release
//	// unmarshal release object bytes
//	if err := json.Unmarshal(b, &rls); err != nil {
//		return nil, err
//	}
//	return &rls, nil
//}

func (impl *K8sInformerImpl) BuildInformerForAllClusters() error {
	models, err := impl.clusterRepository.FindAllActive()
	if err != nil {
		impl.logger.Error("error in fetching clusters", "err", err)
		return err
	}
	for _, model := range models {

		bearerToken := model.Config["bearer_token"]

		clusterInfo := &ClusterInfo{
			ClusterId:   model.Id,
			ClusterName: model.ClusterName,
			BearerToken: bearerToken,
			ServerUrl:   model.ServerUrl,
		}
		err := impl.startInformer(*clusterInfo)
		if err != nil {
			impl.logger.Error("error in starting informer for cluster ", "cluster-name ", clusterInfo.ClusterName, "err", err)
			return err
		}

	}

	return nil
}

func getDevConfig(configName string) (*rest.Config, error) {
	usr, err := user.Current()
	if err != nil {
		return nil, err
	}
	kubeconfig := flag.String(configName, filepath.Join(usr.HomeDir, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	flag.Parse()
	cfg, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func (impl *K8sInformerImpl) startInformer(clusterInfo ClusterInfo) error {

	restConfig := &rest.Config{}
	if clusterInfo.ClusterName == DEFAULT_CLUSTER {
		config, err := rest.InClusterConfig()
		if err != nil {
			config = impl.DevConfig
			//impl.logger.Error("error in fetch default cluster config", "err", err, "servername", restConfig.ServerName)
			//return err //TODO KB: remove this
		}
		restConfig = config
	} else {
		restConfig.BearerToken = clusterInfo.BearerToken
		restConfig.Host = clusterInfo.ServerUrl
		restConfig.Insecure = true
	}

	httpClientFor, err := rest.HTTPClientFor(restConfig)
	if err != nil {
		impl.logger.Error("error occurred while overriding k8s client", "reason", err)
		return err
	}
	clusterClient, err := kubernetes.NewForConfigAndClient(restConfig, httpClientFor)
	if err != nil {
		impl.logger.Error("error in create k8s config", "err", err)
		return err
	}

	// for default cluster adding an extra informer, this informer will add informer on new clusters
	if clusterInfo.ClusterName == DEFAULT_CLUSTER {
		impl.logger.Debug("Starting informer, reading new cluster request for default cluster")
		labelOptions := kubeinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			//kubectl  get  secret --field-selector type==cluster.request/modify --all-namespaces
			opts.FieldSelector = "type==cluster.request/modify"
		})
		informerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(clusterClient, 15*time.Minute, labelOptions)
		stopper := make(chan struct{})
		secretInformer := informerFactory.Core().V1().Secrets()
		secretInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				impl.logger.Debug("Event received in cluster secret Add informer", "time", time.Now())
				if secretObject, ok := obj.(*coreV1.Secret); ok {
					if secretObject.Type != CLUSTER_MODIFY_EVENT_SECRET_TYPE {
						return
					}
					data := secretObject.Data
					action := data["action"]
					id := string(data["cluster_id"])
					id_int, _ := strconv.Atoi(id)

					if string(action) == ADD {
						err = impl.startInformerAndPopulateCache(id_int)
						if err != nil && err != errors.New(INFORMER_ALREADY_EXIST_MESSAGE) {
							impl.logger.Debug("error in adding informer for cluster", "id", id_int, "err", err)
							return
						}
					}
					if string(action) == UPDATE {
						err = impl.syncInformer(id_int)
						if err != nil && err != errors.New(INFORMER_ALREADY_EXIST_MESSAGE) {
							impl.logger.Debug("error in updating informer for cluster", "id", clusterInfo.ClusterId, "name", clusterInfo.ClusterName, "err", err)
							return
						}
					}
				}
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				impl.logger.Debug("Event received in cluster secret update informer", "time", time.Now())
				if secretObject, ok := newObj.(*coreV1.Secret); ok {
					if secretObject.Type != CLUSTER_MODIFY_EVENT_SECRET_TYPE {
						return
					}
					data := secretObject.Data
					action := data["action"]
					id := string(data["cluster_id"])
					id_int, _ := strconv.Atoi(id)

					if string(action) == ADD {
						err = impl.startInformerAndPopulateCache(clusterInfo.ClusterId)
						if err != nil && err != errors.New(INFORMER_ALREADY_EXIST_MESSAGE) {
							impl.logger.Error("error in adding informer for cluster", "id", id_int, "err", err)
							return
						}
					}
					if string(action) == UPDATE {
						err := impl.syncInformer(id_int)
						if err != nil && err != errors.New(INFORMER_ALREADY_EXIST_MESSAGE) {
							impl.logger.Error("error in updating informer for cluster", "id", clusterInfo.ClusterId, "name", clusterInfo.ClusterName, "err", err)
							return
						}
					}
				}
			},
			DeleteFunc: func(obj interface{}) {
				impl.logger.Debug("Event received in secret delete informer", "time", time.Now())
				if secretObject, ok := obj.(*coreV1.Secret); ok {
					if secretObject.Type != CLUSTER_MODIFY_EVENT_SECRET_TYPE {
						return
					}
					data := secretObject.Data
					action := data["action"]
					id := string(data["cluster_id"])
					id_int, _ := strconv.Atoi(id)

					if string(action) == "delete" {
						deleteClusterInfo, err := impl.clusterRepository.FindByIdWithActiveFalse(id_int)
						if err != nil {
							impl.logger.Error("Error in fetching cluster by id", "cluster-id ", id_int)
							return
						}
						impl.stopInformer(deleteClusterInfo.ClusterName, deleteClusterInfo.Id)
						if err != nil {
							impl.logger.Error("error in updating informer for cluster", "id", clusterInfo.ClusterId, "name", clusterInfo.ClusterName, "err", err)
							return
						}
					}
				}
			},
		})
		informerFactory.Start(stopper)
		//impl.informerStopper[clusterInfo.ClusterName+"_second_informer"] = stopper

	}
	// these informers will be used to populate helm release cache

	err = impl.startInformerAndPopulateCache(clusterInfo.ClusterId)
	if err != nil && err != errors.New(INFORMER_ALREADY_EXIST_MESSAGE) {
		impl.logger.Error("error in creating informer for new cluster", "err", err)
		return err
	}

	return nil
}

func (impl *K8sInformerImpl) syncInformer(clusterId int) error {

	clusterInfo, err := impl.clusterRepository.FindById(clusterId)
	if err != nil {
		impl.logger.Error("error in fetching cluster info by id", "err", err)
		return err
	}
	//before creating new informer for cluster, close existing one
	impl.logger.Debug("stopping informer for cluster - ", "cluster-name", clusterInfo.ClusterName, "cluster-id", clusterInfo.Id)
	impl.stopInformer(clusterInfo.ClusterName, clusterInfo.Id)
	impl.logger.Debug("informer stopped", "cluster-name", clusterInfo.ClusterName, "cluster-id", clusterInfo.Id)
	//create new informer for cluster with new config
	err = impl.startInformerAndPopulateCache(clusterId)
	if err != nil {
		impl.logger.Error("error in starting informer for ", "cluster name", clusterInfo.ClusterName)
		return err
	}
	return nil
}

func (impl *K8sInformerImpl) stopInformer(clusterName string, clusterId int) {
	stopper := impl.informerStopper[clusterId]
	if stopper != nil {
		close(stopper)
		delete(impl.informerStopper, clusterId)
	}
	return
}

func (impl *K8sInformerImpl) startInformerAndPopulateCache(clusterId int) error {

	clusterInfo, err := impl.clusterRepository.FindById(clusterId)
	if err != nil {
		impl.logger.Error("error in fetching cluster by cluster ids")
		return err
	}

	if _, ok := impl.informerStopper[clusterId]; ok {
		impl.logger.Debug(fmt.Sprintf("informer for %s already exist", clusterInfo.ClusterName))
		return errors.New(INFORMER_ALREADY_EXIST_MESSAGE)
	}

	impl.logger.Info("starting informer for cluster - ", "cluster-id ", clusterInfo.Id, "cluster-name ", clusterInfo.ClusterName)

	restConfig := &rest.Config{}

	if clusterInfo.ClusterName == DEFAULT_CLUSTER {
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			restConfig = impl.DevConfig
			//impl.logger.Error("error in fetch default cluster config", "err", err, "clusterName", clusterInfo.ClusterName)
			//return err //TODO KB: remove this
		}
	} else {
		restConfig = &rest.Config{
			Host:            clusterInfo.ServerUrl,
			BearerToken:     clusterInfo.Config["bearer_token"],
			TLSClientConfig: rest.TLSClientConfig{Insecure: true},
		}
	}

	httpClientFor, err := rest.HTTPClientFor(restConfig)
	if err != nil {
		impl.logger.Error("error occurred while overriding k8s client", "reason", err)
		return err
	}
	clusterClient, err := kubernetes.NewForConfigAndClient(restConfig, httpClientFor)
	if err != nil {
		impl.logger.Error("error in create k8s config", "err", err)
		return err
	}

	//impl.mutex.Lock()
	//impl.HelmListClusterMap[clusterId] = make(map[string]*client.DeployedAppDetail)
	//impl.mutex.Unlock()

	labelOptions := kubeinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
		//kubectl  get  secret --field-selector type==helm.sh/release.v1 -l status=deployed  --all-namespaces
		opts.LabelSelector = "status!=superseded"
		opts.FieldSelector = "type==helm.sh/release.v1"
	})
	informerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(clusterClient, 15*time.Minute, labelOptions)
	stopper := make(chan struct{})
	secretInformer := informerFactory.Core().V1().Secrets()
	secretInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			impl.logger.Debug("Event received in Helm secret add informer", "time", time.Now())
			if _, ok := obj.(*coreV1.Secret); ok {
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			impl.logger.Debug("Event received in Helm secret update informer", "time", time.Now())
			if _, ok := oldObj.(*coreV1.Secret); ok {
			}
		},
		DeleteFunc: func(obj interface{}) {
			impl.logger.Debug("Event received in Helm secret delete informer", "time", time.Now())
			if _, ok := obj.(*coreV1.Secret); ok {
			}
		},
	})
	informerFactory.Start(stopper)
	impl.logger.Info("informer started for cluster: ", "cluster_id", clusterInfo.Id, "cluster_name", clusterInfo.ClusterName)
	impl.informerStopper[clusterId] = stopper
	return nil
}

