package informer

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"time"
)

// Empty namespace preserves the existing all-namespace cluster discovery.
func clusterSecretInformerFactory(client kubernetes.Interface, namespace string) kubeinformers.SharedInformerFactory {
	return kubeinformers.NewSharedInformerFactoryWithOptions(client, 15*time.Minute,
		kubeinformers.WithNamespace(namespace),
		kubeinformers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = "type==cluster.request/modify"
		}),
	)
}
