package informer

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	"github.com/argoproj/argo-workflows/v3/workflow/common"
	"github.com/caarlos0/env"
	pubsub "github.com/devtron-labs/common-lib/pubsub-lib"
	repository "github.com/devtron-labs/kubewatch/pkg/cluster"
	"go.uber.org/zap"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/pointer"
	"log"
	"os/user"
	"path/filepath"
	"sort"
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
	startInformerForCluster(clusterId int) error
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
	logger            *zap.SugaredLogger
	mutex             sync.Mutex
	informerStopper   map[int]chan struct{}
	clusterRepository repository.ClusterRepository
	helmReleaseConfig *HelmReleaseConfig
	DevConfig         *rest.Config
	pubSubClient      *pubsub.PubSubClientServiceImpl
}

func Newk8sInformerImpl(logger *zap.SugaredLogger, clusterRepository repository.ClusterRepository, client *pubsub.PubSubClientServiceImpl) *K8sInformerImpl {
	informerFactory := &K8sInformerImpl{
		logger:            logger,
		clusterRepository: clusterRepository,
		pubSubClient:      client,
	}
	devConfig, _ := getDevConfig("kubeconfigK8s")
	informerFactory.DevConfig = devConfig
	//informerFactory.HelmListClusterMap = make(map[int]map[string]*pubSubClient.DeployedAppDetail)
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
		impl.logger.Error("error occurred while overriding k8s pubSubClient", "reason", err)
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
						err = impl.startInformerForCluster(id_int)
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
						err = impl.startInformerForCluster(clusterInfo.ClusterId)
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

	err = impl.startInformerForCluster(clusterInfo.ClusterId)
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
	impl.logger.Debugw("stopping informer for cluster - ", "cluster-name", clusterInfo.ClusterName, "cluster-id", clusterInfo.Id)
	impl.stopInformer(clusterInfo.ClusterName, clusterInfo.Id)
	impl.logger.Debugw("informer stopped", "cluster-name", clusterInfo.ClusterName, "cluster-id", clusterInfo.Id)
	//create new informer for cluster with new config
	err = impl.startInformerForCluster(clusterId)
	if err != nil {
		impl.logger.Errorw("error in starting informer for ", "cluster name", clusterInfo.ClusterName)
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

func (impl *K8sInformerImpl) startInformerForCluster(clusterId int) error {

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
		impl.logger.Error("error occurred while overriding k8s pubSubClient", "reason", err)
		return err
	}
	clusterClient, err := kubernetes.NewForConfigAndClient(restConfig, httpClientFor)
	if err != nil {
		impl.logger.Error("error in create k8s config", "err", err)
		return err
	}

	labelOptions := kubeinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
		//kubectl  get  secret --field-selector type==helm.sh/release.v1 -l status=deployed  --all-namespaces
		opts.LabelSelector = "devtron.ai/purpose==workflow"
		//opts.FieldSelector = "type==helm.sh/release.v1"
	})
	informerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(clusterClient, 15*time.Minute, labelOptions)
	stopper := make(chan struct{})
	jobsInformer := informerFactory.Core().V1().Pods()
	jobsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			//if podObj, ok := obj.(*coreV1.Pod); ok {
			//	impl.logger.Debugw("Event received in Pods add informer", "time", time.Now(), "podObjStatus", podObj.Status)
			//}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if podObj, ok := newObj.(*coreV1.Pod); ok {
				impl.logger.Debugw("Event received in Pods update informer", "time", time.Now(), "podObjStatus", podObj.Status)
				nodeStatus := impl.assessNodeStatus(podObj)
				workflowStatus := impl.getWorkflowStatus(podObj, nodeStatus)
				wfJson, err := json.Marshal(workflowStatus)
				if err != nil {
					impl.logger.Errorw("error occurred while marshalling workflowJson", "err", err)
					return
				}
				impl.logger.Debugw("sending system executor cd workflow update event", "workflow", string(wfJson))
				var reqBody = []byte(wfJson)
				client := impl.pubSubClient
				if client == nil {
					log.Println("dont't publish")
					return
				}

				err = client.Publish(pubsub.CD_WORKFLOW_STATUS_UPDATE, string(reqBody))
				if err != nil {
					impl.logger.Errorw("Error while publishing Request", "err", err)
					return
				}
				impl.logger.Debug("cd workflow update sent")
			}
			//TODO KB: Refer https://github.com/argoproj/argo-workflows/blob/master/workflow/controller/operator.go#LL1243C28-L1243C44
		},
		DeleteFunc: func(obj interface{}) {
			if podObj, ok := obj.(*coreV1.Pod); ok {
				impl.logger.Debugw("Event received in Pods delete informer", "time", time.Now(), "podObjStatus", podObj.Status)
			}
		},
	})
	informerFactory.Start(stopper)
	impl.logger.Info("informer started for cluster: ", "cluster_id", clusterInfo.Id, "cluster_name", clusterInfo.ClusterName)
	impl.informerStopper[clusterId] = stopper
	return nil
}

func (impl *K8sInformerImpl) assessNodeStatus(pod *coreV1.Pod) v1alpha1.NodeStatus {

	nodeStatus := v1alpha1.NodeStatus{}
	switch pod.Status.Phase {
	case coreV1.PodPending:
		nodeStatus.Phase = v1alpha1.NodePending
		nodeStatus.Message = getPendingReason(pod)
	case coreV1.PodSucceeded:
		nodeStatus.Phase = v1alpha1.NodeSucceeded
	case coreV1.PodFailed:
		nodeStatus.Phase, nodeStatus.Message = impl.inferFailedReason(pod)
		impl.logger.Infof("Pod %s failed: %s", pod.Name, nodeStatus.Message)
	case coreV1.PodRunning:
		nodeStatus.Phase = v1alpha1.NodeRunning
	default:
		nodeStatus.Phase = v1alpha1.NodeError
		nodeStatus.Message = fmt.Sprintf("Unexpected pod phase for %s: %s", pod.ObjectMeta.Name, pod.Status.Phase)
	}

	// if it's ContainerSetTemplate pod then the inner container names should match to some node names,
	// in this case need to update nodes according to container status
	//for _, c := range pod.Status.ContainerStatuses {
	//	ctrNodeName := fmt.Sprintf("%s.%s", oldPod.Name, c.Name)
	//	if woc.wf.GetNodeByName(ctrNodeName) == nil {
	//		continue
	//	}
	//	switch {
	//	case c.State.Waiting != nil:
	//		woc.markNodePhase(ctrNodeName, wfv1.NodePending)
	//	case c.State.Running != nil:
	//		woc.markNodePhase(ctrNodeName, wfv1.NodeRunning)
	//	case c.State.Terminated != nil:
	//		exitCode := int(c.State.Terminated.ExitCode)
	//		message := fmt.Sprintf("%s (exit code %d): %s", c.State.Terminated.Reason, exitCode, c.State.Terminated.Message)
	//		switch exitCode {
	//		case 0:
	//			woc.markNodePhase(ctrNodeName, wfv1.NodeSucceeded)
	//		case 64:
	//			// special emissary exit code indicating the emissary errors, rather than the sub-process failure,
	//			// (unless the sub-process coincidentally exits with code 64 of course)
	//			woc.markNodePhase(ctrNodeName, wfv1.NodeError, message)
	//		default:
	//			woc.markNodePhase(ctrNodeName, wfv1.NodeFailed, message)
	//		}
	//	}
	//}

	// only update Pod IP for daemoned nodes to reduce number of updates
	if !nodeStatus.Completed() && nodeStatus.IsDaemoned() {
		nodeStatus.PodIP = pod.Status.PodIP
	}

	//if x, ok := pod.Annotations[common.AnnotationKeyOutputs]; ok {
	//	woc.log.Warn("workflow uses legacy/insecure pod patch, see https://argoproj.github.io/argo-workflows/workflow-rbac/")
	//	if nodeStatus.Outputs == nil {
	//		nodeStatus.Outputs = &wfv1.Outputs{}
	//	}
	//	if err := json.Unmarshal([]byte(x), nodeStatus.Outputs); err != nil {
	//		nodeStatus.Phase = wfv1.NodeError
	//		nodeStatus.Message = err.Error()
	//	}
	//}

	nodeStatus.HostNodeName = pod.Spec.NodeName

	if !nodeStatus.Progress.IsValid() {
		nodeStatus.Progress = v1alpha1.ProgressDefault
	}

	if x, ok := pod.Annotations[common.AnnotationKeyProgress]; ok {
		impl.logger.Warn("workflow uses legacy/insecure pod patch, see https://argoproj.github.io/argo-workflows/workflow-rbac/")
		if p, ok := v1alpha1.ParseProgress(x); ok {
			nodeStatus.Progress = p
		}
	}

	// We capture the exit-code after we look for the task-result.
	// All other outputs are set by the executor, only the exit-code is set by the controller.
	// By waiting, we avoid breaking the race-condition check.
	if exitCode := getExitCode(pod); exitCode != nil {
		if nodeStatus.Outputs == nil {
			nodeStatus.Outputs = &v1alpha1.Outputs{}
		}
		nodeStatus.Outputs.ExitCode = pointer.StringPtr(fmt.Sprint(*exitCode))
	}

	// If the init container failed, we should mark the node as failed.
	//var initContainerFailed bool
	//for _, c := range pod.Status.InitContainerStatuses {
	//	if c.State.Terminated != nil && int(c.State.Terminated.ExitCode) != 0 {
	//		nodeStatus.Phase = v1alpha1.NodeFailed
	//		//initContainerFailed = true
	//		impl.logger.Infow("marking node as failed since init container has non-zero exit code","newPhase", nodeStatus.Phase)
	//		break
	//	}
	//}

	// We cannot fail the node until the wait container is finished (unless any init container has failed) because it may be busy saving outputs, and these
	// would not get captured successfully.
	//for _, c := range pod.Status.ContainerStatuses {
	//	if (c.Name == common.WaitContainerName && c.State.Terminated == nil && nodeStatus.Phase.Completed()) && !initContainerFailed {
	//		impl.logger.Infow("leaving phase un-changed: wait container is not yet terminated","newPhase", nodeStatus.Phase)
	//		nodeStatus.Phase = oldPod.Phase
	//	}
	//}

	// if we are transitioning from Pending to a different state, clear out unchanged message
	//if old.Phase == wfv1.NodePending && nodeStatus.Phase != wfv1.NodePending && old.Message == nodeStatus.Message {
	//	nodeStatus.Message = ""
	//}

	if nodeStatus.Fulfilled() && nodeStatus.FinishedAt.IsZero() {
		nodeStatus.FinishedAt = getLatestFinishedAt(pod)
		//nodeStatus.ResourcesDuration = durationForPod(pod)
	}

	return nodeStatus
}

func getLatestFinishedAt(pod *coreV1.Pod) metav1.Time {
	var latest metav1.Time
	for _, ctr := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
		if r := ctr.State.Running; r != nil { // if we are running, then the finished at time must be now or after
			latest = metav1.Now()
		} else if t := ctr.State.Terminated; t != nil && t.FinishedAt.After(latest.Time) {
			latest = t.FinishedAt
		}
	}
	return latest
}

func getExitCode(pod *coreV1.Pod) *int32 {
	for _, c := range pod.Status.ContainerStatuses {
		if c.Name == common.MainContainerName && c.State.Terminated != nil {
			return pointer.Int32Ptr(c.State.Terminated.ExitCode)
		}
	}
	return nil
}

func getPendingReason(pod *coreV1.Pod) string {
	for _, ctrStatus := range pod.Status.ContainerStatuses {
		if ctrStatus.State.Waiting != nil {
			if ctrStatus.State.Waiting.Message != "" {
				return fmt.Sprintf("%s: %s", ctrStatus.State.Waiting.Reason, ctrStatus.State.Waiting.Message)
			}
			return ctrStatus.State.Waiting.Reason
		}
	}
	// Example:
	// - lastProbeTime: null
	//   lastTransitionTime: 2018-08-29T06:38:36Z
	//   message: '0/3 nodes are available: 2 Insufficient cpu, 3 MatchNodeSelector.'
	//   reason: Unschedulable
	//   status: "False"
	//   type: PodScheduled
	for _, cond := range pod.Status.Conditions {
		if cond.Reason == coreV1.PodReasonUnschedulable {
			if cond.Message != "" {
				return fmt.Sprintf("%s: %s", cond.Reason, cond.Message)
			}
			return cond.Reason
		}
	}
	return ""
}

func (impl *K8sInformerImpl) inferFailedReason(pod *coreV1.Pod) (v1alpha1.NodePhase, string) {
	if pod.Status.Message != "" {
		// Pod has a nice error message. Use that.
		return v1alpha1.NodeFailed, pod.Status.Message
	}

	// We only get one message to set for the overall node status.
	// If multiple containers failed, in order of preference:
	// init containers (will be appended later), main (annotated), main (exit code), wait, sidecars.
	order := func(n string) int {
		switch {
		case n == common.MainContainerName:
			return 1
		case n == common.WaitContainerName:
			return 2
		default:
			return 3
		}
	}
	ctrs := pod.Status.ContainerStatuses
	sort.Slice(ctrs, func(i, j int) bool { return order(ctrs[i].Name) < order(ctrs[j].Name) })
	// Init containers have the highest preferences over other containers.
	ctrs = append(pod.Status.InitContainerStatuses, ctrs...)

	for _, ctr := range ctrs {

		// Virtual Kubelet environment will not set the terminate on waiting container
		// https://github.com/argoproj/argo-workflows/issues/3879
		// https://github.com/virtual-kubelet/virtual-kubelet/blob/7f2a02291530d2df14905702e6d51500dd57640a/node/sync.go#L195-L208

		if ctr.State.Waiting != nil {
			return v1alpha1.NodeError, fmt.Sprintf("Pod failed before %s container starts due to %s: %s", ctr.Name, ctr.State.Waiting.Reason, ctr.State.Waiting.Message)
		}
		t := ctr.State.Terminated
		if t == nil {
			// We should never get here
			impl.logger.Warnf("Pod %s phase was Failed but %s did not have terminated state", pod.Name, ctr.Name)
			continue
		}
		if t.ExitCode == 0 {
			continue
		}

		msg := fmt.Sprintf("%s (exit code %d)", t.Reason, t.ExitCode)
		if t.Message != "" {
			msg = fmt.Sprintf("%s: %s", msg, t.Message)
		}

		switch {
		case ctr.Name == common.InitContainerName:
			return v1alpha1.NodeError, msg
		case ctr.Name == common.MainContainerName:
			return v1alpha1.NodeFailed, msg
		case ctr.Name == common.WaitContainerName:
			return v1alpha1.NodeError, msg
		default:
			if t.ExitCode == 137 || t.ExitCode == 143 {
				// if the sidecar was SIGKILL'd (exit code 137) assume it was because argoexec
				// forcibly killed the container, which we ignore the error for.
				// Java code 143 is a normal exit 128 + 15 https://github.com/elastic/elasticsearch/issues/31847
				impl.logger.Infof("Ignoring %d exit code of container '%s'", t.ExitCode, ctr.Name)
			} else {
				return v1alpha1.NodeFailed, msg
			}
		}
	}

	// If we get here, we have detected that the main/wait containers succeed but the sidecar(s)
	// were  SIGKILL'd. The executor may have had to forcefully terminate the sidecar (kill -9),
	// resulting in a 137 exit code (which we had ignored earlier). If failMessages is empty, it
	// indicates that this is the case and we return Success instead of Failure.
	return v1alpha1.NodeSucceeded, ""
}

func (impl *K8sInformerImpl) getWorkflowStatus(podObj *coreV1.Pod, nodeStatus v1alpha1.NodeStatus) *v1alpha1.WorkflowStatus {
	workflowStatus := &v1alpha1.WorkflowStatus{}
	workflowStatus.Phase = v1alpha1.WorkflowPhase(nodeStatus.Phase)
	nodeNameVsStatus := make(map[string]v1alpha1.NodeStatus,1)
	nodeStatus.ID = podObj.Name
	nodeStatus.TemplateName = "cd"
	nodeStatus.Name = nodeStatus.ID
	nodeNameVsStatus[podObj.Name] = nodeStatus
	workflowStatus.Nodes = nodeNameVsStatus
	return workflowStatus
}
