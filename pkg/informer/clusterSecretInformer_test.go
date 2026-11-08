package informer

import (
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"testing"
	"time"
)

func TestClusterSecretInformerNamespace(t *testing.T) {
	for _, namespace := range []string{"", "control-plane"} {
		t.Run(namespace, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			factory := clusterSecretInformerFactory(client, namespace)
			factory.Core().V1().Secrets().Informer()
			stop := make(chan struct{})
			defer close(stop)
			factory.Start(stop)
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				listed, watched := false, false
				for _, action := range client.Actions() {
					if action.GetResource().Resource != "secrets" {
						continue
					}
					if action.GetNamespace() != namespace {
						t.Fatalf("namespace: got %q, want %q", action.GetNamespace(), namespace)
					}
					switch a := action.(type) {
					case clienttesting.ListAction:
						listed = true
						if !a.GetListRestrictions().Fields.Matches(fields.Set{"type": "cluster.request/modify"}) || a.GetListRestrictions().Fields.Matches(fields.Set{"type": "Opaque"}) {
							t.Fatal("list selector lost")
						}
					case clienttesting.WatchAction:
						watched = true
						if !a.GetWatchRestrictions().Fields.Matches(fields.Set{"type": "cluster.request/modify"}) || a.GetWatchRestrictions().Fields.Matches(fields.Set{"type": "Opaque"}) {
							t.Fatal("watch selector lost")
						}
					}
				}
				if listed && watched {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("secret list/watch were not started")
		})
	}
}
