package informer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	"github.com/argoproj/argo-workflows/v3/workflow/common"
	pubsub "github.com/devtron-labs/common-lib/pubsub-lib"
	repository "github.com/devtron-labs/kubewatch/pkg/cluster"
	"github.com/devtron-labs/kubewatch/pkg/utils"
	"go.uber.org/zap"
	coreV1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/pointer"
	"log"
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
	POD_DELETED_MESSAGE              = "pod deleted"
	EXIT_CODE_143_ERROR              = "Error (exit code 143)"
	CI_WORKFLOW_NAME                 = "ci"
	CD_WORKFLOW_NAME                 = "cd"
	WORKFLOW_TYPE_LABEL_KEY          = "workflowType"
	JobKind                          = "Job"
)

type K8sInformer interface {
	BuildInformerForAllClusters() error
}

type K8sInformerImpl struct {
	logger            *zap.SugaredLogger
	mutex             sync.Mutex
	informerStopper   map[int]chan struct{}
	clusterRepository repository.ClusterRepository
	DefaultK8sConfig  *rest.Config
	pubSubClient      *pubsub.PubSubClientServiceImpl
}

func NewK8sInformerImpl(logger *zap.SugaredLogger, clusterRepository repository.ClusterRepository, client *pubsub.PubSubClientServiceImpl) *K8sInformerImpl {
	informerFactory := &K8sInformerImpl{
		logger:            logger,
		clusterRepository: clusterRepository,
		pubSubClient:      client,
	}
	defaultK8sConfig, _ := utils.GetDefaultK8sConfig("kubeconfigK8s")
	informerFactory.DefaultK8sConfig = defaultK8sConfig
	informerFactory.informerStopper = make(map[int]chan struct{})
	return informerFactory
}

func (impl *K8sInformerImpl) BuildInformerForAllClusters() error {

	impl.startClusterInformer()
	models, err := impl.clusterRepository.FindAllActive()
	if err != nil {
		impl.logger.Error("error in fetching clusters", "err", err)
		return err
	}
	for _, model := range models {
		impl.startSystemWorkflowInformer(model.Id)
	}
	return nil
}

func (impl *K8sInformerImpl) startSystemWorkflowInformerForCluster(clusterInfo ClusterInfo) error {

	restConfig := &rest.Config{}
	if clusterInfo.ClusterName == DEFAULT_CLUSTER {
		config, err := rest.InClusterConfig()
		if err != nil {
			//config = impl.DefaultK8sConfig
			impl.logger.Error("error in fetch default cluster config", "err", err, "servername", restConfig.ServerName)
			return err //TODO KB: remove this
		}
		restConfig = config
	} else {
		restConfig.BearerToken = clusterInfo.BearerToken
		restConfig.Host = clusterInfo.ServerUrl
		restConfig.Insecure = true
	}

	err := impl.startSystemWorkflowInformer(clusterInfo.ClusterId)
	if err != nil && err != errors.New(INFORMER_ALREADY_EXIST_MESSAGE) {
		impl.logger.Error("error in creating informer for new cluster", "err", err)
		return err
	}

	return nil
}

func (impl *K8sInformerImpl) startClusterInformer() {
	impl.logger.Debug("Starting informer, reading new cluster request for default cluster")
	//config, err := rest.InClusterConfig()
	//if err != nil {
	//	impl.logger.Errorw("error occurred while extracting cluster config", "err", err)
	//	return
	//}
	config := impl.DefaultK8sConfig
	clusterClient, err := impl.getK8sClientForConfig(config)
	if err != nil {
		return
	}

	labelOptions := kubeinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
		opts.FieldSelector = "type==cluster.request/modify"
	})
	informerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(clusterClient, 15*time.Minute, labelOptions)
	stopper := make(chan struct{})
	secretInformer := informerFactory.Core().V1().Secrets()
	secretInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(newObj interface{}) {
			impl.logger.Debug("Event received in cluster secret Add informer", "time", time.Now())
			if secretObject, ok := newObj.(*coreV1.Secret); ok {
				impl.handleClusterChangeEvent(secretObject)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			impl.logger.Debug("Event received in cluster secret update informer", "time", time.Now())
			if secretObject, ok := newObj.(*coreV1.Secret); ok {
				impl.handleClusterChangeEvent(secretObject)
			}
		},
		DeleteFunc: func(obj interface{}) {
			impl.logger.Debugw("Event received in secret delete informer", "time", time.Now())
			if secretObject, ok := obj.(*coreV1.Secret); ok {
				if impl.handleClusterDeleteEvent(secretObject) {
					return
				}
			}
		},
	})
	informerFactory.Start(stopper)
}

func (impl *K8sInformerImpl) getK8sClientForConfig(config *rest.Config) (*kubernetes.Clientset, error) {
	httpClientFor, err := rest.HTTPClientFor(config)
	if err != nil {
		impl.logger.Errorw("error occurred while overriding k8s pubSubClient", "reason", err)
		return nil, err
	}
	clusterClient, err := kubernetes.NewForConfigAndClient(config, httpClientFor)
	if err != nil {
		impl.logger.Errorw("error in create k8s config", "err", err)
		return nil, err
	}
	return clusterClient, nil
}

func (impl *K8sInformerImpl) handleClusterDeleteEvent(secretObject *coreV1.Secret) bool {
	if secretObject.Type != CLUSTER_MODIFY_EVENT_SECRET_TYPE {
		return true
	}
	data := secretObject.Data
	action := data["action"]
	id := string(data["cluster_id"])
	clusterId, _ := strconv.Atoi(id)

	if string(action) == "delete" {
		if impl.handleClusterDelete(clusterId) {
			return true
		}
	}
	return false
}

func (impl *K8sInformerImpl) handleClusterDelete(clusterId int) bool {
	deleteClusterInfo, err := impl.clusterRepository.FindByIdWithActiveFalse(clusterId)
	if err != nil {
		impl.logger.Errorw("Error in fetching cluster by id", "cluster-id ", clusterId, "err", err)
		return true
	}
	impl.stopSystemWorkflowInformer(deleteClusterInfo.Id)
	if err != nil {
		impl.logger.Errorw("error in updating informer for cluster", "id", clusterId, "err", err)
		return true
	}
	return false
}

func (impl *K8sInformerImpl) handleClusterChangeEvent(secretObject *coreV1.Secret) {
	if secretObject.Type != CLUSTER_MODIFY_EVENT_SECRET_TYPE {
		return
	}
	data := secretObject.Data
	action := data["action"]
	id := string(data["cluster_id"])
	clusterId, _ := strconv.Atoi(id)
	var err error

	if string(action) == ADD {
		err = impl.startSystemWorkflowInformer(clusterId)
		if err != nil && err != errors.New(INFORMER_ALREADY_EXIST_MESSAGE) {
			impl.logger.Error("error in adding informer for cluster", "id", clusterId, "err", err)
			return
		}
	} else if string(action) == UPDATE {
		err = impl.syncSystemWorkflowInformer(clusterId)
		if err != nil && err != errors.New(INFORMER_ALREADY_EXIST_MESSAGE) {
			impl.logger.Errorw("error in updating informer for cluster", "id", clusterId, "err", err)
			return
		}
	}
	return
}

func (impl *K8sInformerImpl) syncSystemWorkflowInformer(clusterId int) error {

	clusterInfo, err := impl.clusterRepository.FindById(clusterId)
	if err != nil {
		impl.logger.Error("error in fetching cluster info by id", "err", err)
		return err
	}
	//before creating new informer for cluster, close existing one
	impl.logger.Debugw("stopping informer for cluster - ", "cluster-name", clusterInfo.ClusterName, "cluster-id", clusterInfo.Id)
	impl.stopSystemWorkflowInformer(clusterInfo.Id)
	impl.logger.Debugw("informer stopped", "cluster-name", clusterInfo.ClusterName, "cluster-id", clusterInfo.Id)
	//create new informer for cluster with new config
	err = impl.startSystemWorkflowInformer(clusterId)
	if err != nil {
		impl.logger.Errorw("error in starting informer for ", "cluster name", clusterInfo.ClusterName)
		return err
	}
	return nil
}

// todo
func (impl *K8sInformerImpl) stopSystemWorkflowInformer(clusterId int) {
	stopper := impl.informerStopper[clusterId]
	if stopper != nil {
		close(stopper)
		delete(impl.informerStopper, clusterId)
	}
	return
}

func (impl *K8sInformerImpl) startSystemWorkflowInformer(clusterId int) error {

	clusterInfo, err := impl.clusterRepository.FindById(clusterId)
	if err != nil {
		impl.logger.Errorw("error in fetching cluster", "clusterId", clusterId, "err", err)
		return err
	}

	if _, ok := impl.informerStopper[clusterId]; ok {
		impl.logger.Debug(fmt.Sprintf("informer for %s already exist", clusterInfo.ClusterName))
		return errors.New(INFORMER_ALREADY_EXIST_MESSAGE)
	}
	impl.logger.Infow("starting informer for cluster", "clusterId", clusterInfo.Id, "clusterName", clusterInfo.ClusterName)
	clusterClient, err := impl.getK8sClientForCluster(clusterInfo)
	if err != nil {
		return err
	}

	labelOptions := kubeinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
		opts.LabelSelector = "devtron.ai/purpose==workflow"
	})
	informerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(clusterClient, 15*time.Minute, labelOptions)
	stopper := make(chan struct{})
	podInformer := informerFactory.Core().V1().Pods()
	podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			if podObj, ok := newObj.(*coreV1.Pod); ok {
				var workflowType string
				if podObj.Labels != nil {
					if val, ok := podObj.Labels[WORKFLOW_TYPE_LABEL_KEY]; ok {
						workflowType = val
					}
				}
				impl.logger.Debugw("Event received in Pods update informer", "time", time.Now(), "podObjStatus", podObj.Status)
				nodeStatus := impl.assessNodeStatus(podObj)
				workflowStatus := impl.getWorkflowStatus(podObj, nodeStatus, workflowType)
				wfJson, err := json.Marshal(workflowStatus)
				if err != nil {
					impl.logger.Errorw("error occurred while marshalling workflowJson", "err", err)
					return
				}
				impl.logger.Debugw("sending system executor workflow update event", "workflow", string(wfJson))
				if impl.pubSubClient == nil {
					log.Println("don't publish")
					return
				}
				topic, err := getTopic(workflowType)
				if err != nil {
					impl.logger.Errorw("Error while getting Topic")
					return
				}
				err = impl.pubSubClient.Publish(topic, string(wfJson))
				if err != nil {
					impl.logger.Errorw("Error while publishing Request", "err", err)
					return
				}

				impl.logger.Debug("cd workflow update sent")
			}
		},

		DeleteFunc: func(newObj interface{}) {
			if podObj, ok := newObj.(*coreV1.Pod); ok {
				var workflowType string
				if podObj.Labels != nil {
					if val, ok := podObj.Labels[WORKFLOW_TYPE_LABEL_KEY]; ok {
						workflowType = val
					}
				}
				impl.logger.Debugw("Event received in Pods delete informer", "time", time.Now(), "podObjStatus", podObj.Status)
				nodeStatus := impl.assessNodeStatus(podObj)
				nodeStatus, reTriggerRequired := impl.checkIfPodDeletedAndUpdateMessage(podObj.Name, podObj.Namespace, nodeStatus, clusterClient)
				if !reTriggerRequired {
					//not sending this deleted event if it's not a re-trigger case
					return
				}
				workflowStatus := impl.getWorkflowStatus(podObj, nodeStatus, workflowType)
				wfJson, err := json.Marshal(workflowStatus)
				if err != nil {
					impl.logger.Errorw("error occurred while marshalling workflowJson", "err", err)
					return
				}
				impl.logger.Debugw("sending system executor cd workflow delete event", "workflow", string(wfJson))
				if impl.pubSubClient == nil {
					log.Println("don't publish")
					return
				}
				topic, err := getTopic(workflowType)
				if err != nil {
					impl.logger.Errorw("Error while getting Topic")
					return
				}

				err = impl.pubSubClient.Publish(topic, string(wfJson))
				if err != nil {
					impl.logger.Errorw("Error while publishing Request", "err", err)
					return
				}
				impl.logger.Debug("cd workflow update sent")
			}
		},
	})
	informerFactory.Start(stopper)
	impl.logger.Infow("informer started for cluster", "clusterId", clusterInfo.Id, "clusterName", clusterInfo.ClusterName)
	impl.informerStopper[clusterId] = stopper
	return nil
}

func getTopic(workflowType string) (string, error) {
	switch workflowType {
	case CD_WORKFLOW_NAME:
		return pubsub.CD_WORKFLOW_STATUS_UPDATE, nil
	case CI_WORKFLOW_NAME:
		return pubsub.WORKFLOW_STATUS_UPDATE_TOPIC, nil
	}
	return "", fmt.Errorf("no topic mapped to workflow type %s", workflowType)
}

func (impl *K8sInformerImpl) checkIfPodDeletedAndUpdateMessage(podName, namespace string, nodeStatus v1alpha1.NodeStatus, clusterClient *kubernetes.Clientset) (v1alpha1.NodeStatus, bool) {
	if (nodeStatus.Phase == v1alpha1.NodeFailed || nodeStatus.Phase == v1alpha1.NodeError) && nodeStatus.Message == EXIT_CODE_143_ERROR {
		pod, err := clusterClient.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
		if err != nil {
			impl.logger.Errorw("error in getting pod from clusterClient", "podName", podName, "namespace", namespace, "err", err)
			if isResourceNotFoundErr(err) {
				nodeStatus.Message = POD_DELETED_MESSAGE
				return nodeStatus, true
			}
			return nodeStatus, false
		}
		if pod.DeletionTimestamp != nil {
			nodeStatus.Message = POD_DELETED_MESSAGE
			return nodeStatus, true
		}
	}
	return nodeStatus, false
}

func (impl *K8sInformerImpl) getK8sClientForCluster(clusterInfo *repository.Cluster) (*kubernetes.Clientset, error) {
	restConfig := &rest.Config{}
	if clusterInfo.ClusterName == DEFAULT_CLUSTER {
		restConfig = impl.DefaultK8sConfig
	} else {
		restConfig = &rest.Config{
			Host:            clusterInfo.ServerUrl,
			BearerToken:     clusterInfo.Config["bearer_token"],
			TLSClientConfig: rest.TLSClientConfig{Insecure: true},
		}
	}
	return impl.getK8sClientForConfig(restConfig)
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

	// only update Pod IP for daemoned nodes to reduce number of updates
	if !nodeStatus.Completed() && nodeStatus.IsDaemoned() {
		nodeStatus.PodIP = pod.Status.PodIP
	}
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

func (impl *K8sInformerImpl) getWorkflowStatus(podObj *coreV1.Pod, nodeStatus v1alpha1.NodeStatus, templateName string) *v1alpha1.WorkflowStatus {
	workflowStatus := &v1alpha1.WorkflowStatus{}
	workflowPhase := v1alpha1.WorkflowPhase(nodeStatus.Phase)
	if workflowPhase == v1alpha1.WorkflowPending {
		workflowPhase = v1alpha1.WorkflowRunning
	}
	if workflowPhase.Completed() {
		workflowStatus.FinishedAt = nodeStatus.FinishedAt
	}
	workflowStatus.Phase = workflowPhase
	nodeNameVsStatus := make(map[string]v1alpha1.NodeStatus, 1)
	nodeStatus.ID = podObj.Name
	nodeStatus.TemplateName = templateName
	nodeStatus.Name = nodeStatus.ID
	nodeStatus.BoundaryID = impl.getPodOwnerName(podObj)
	nodeNameVsStatus[podObj.Name] = nodeStatus
	workflowStatus.Nodes = nodeNameVsStatus
	workflowStatus.Message = nodeStatus.Message
	return workflowStatus
}

func (impl *K8sInformerImpl) getPodOwnerName(podObj *coreV1.Pod) string {
	ownerReferences := podObj.OwnerReferences
	for _, ownerReference := range ownerReferences {
		if ownerReference.Kind == JobKind {
			return ownerReference.Name
		}
	}
	return podObj.Name
}

func isResourceNotFoundErr(err error) bool {
	if errStatus, ok := err.(*k8sErrors.StatusError); ok && errStatus.Status().Reason == metav1.StatusReasonNotFound {
		return true
	}
	return false
}
