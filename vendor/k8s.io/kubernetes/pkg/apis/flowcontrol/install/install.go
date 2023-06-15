/*
Copyright 2015 The Kubernetes Authors.

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

// Package install installs the experimental API group, making it available as
// an option to all of the API encoding/decoding machinery.
package install

import (
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	"k8s.io/kubernetes/pkg/apis/flowcontrol"
	flowcontrolv1alpha1 "k8s.io/kubernetes/pkg/apis/flowcontrol/v1alpha1"
	flowcontrolv1beta1 "k8s.io/kubernetes/pkg/apis/flowcontrol/v1beta1"
	flowcontrolv1beta2 "k8s.io/kubernetes/pkg/apis/flowcontrol/v1beta2"
)

func init() {
	Install(legacyscheme.Scheme)
}

// Install registers the API group and adds types to a scheme
func Install(scheme *runtime.Scheme) {
	utilruntime.Must(flowcontrol.AddToScheme(scheme))
	utilruntime.Must(flowcontrolv1alpha1.AddToScheme(scheme))
	utilruntime.Must(flowcontrolv1beta1.AddToScheme(scheme))
	utilruntime.Must(flowcontrolv1beta2.AddToScheme(scheme))
	utilruntime.Must(scheme.SetVersionPriority(flowcontrolv1beta1.SchemeGroupVersion, flowcontrolv1beta2.SchemeGroupVersion, flowcontrolv1alpha1.SchemeGroupVersion))
}
