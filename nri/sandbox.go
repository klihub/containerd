/*
   Copyright The containerd Authors.

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

package nri

import (
	nri "github.com/containerd/nri/v2alpha1/pkg/adaptation"
)

// PodSandbox interface for interacting with NRI.
type PodSandbox interface {
	GetDomain() string

	GetID() string
	GetName() string
	GetUID() string
	GetNamespace() string
	GetLabels() map[string]string
	GetAnnotations() map[string]string
	GetCgroupParent() string
	GetRuntimeHandler() string
}

func podSandboxToNRI(pod PodSandbox) *nri.PodSandbox {
	return &nri.PodSandbox{
		Id:             pod.GetID(),
		Name:           pod.GetName(),
		Uid:            pod.GetUID(),
		Namespace:      pod.GetNamespace(),
		Labels:         pod.GetLabels(),
		Annotations:    pod.GetAnnotations(),
		CgroupParent:   pod.GetCgroupParent(),
		RuntimeHandler: pod.GetRuntimeHandler(),
	}
}

func podSandboxesToNRI(podList []PodSandbox) []*nri.PodSandbox {
	pods := []*nri.PodSandbox{}
	for _, pod := range podList {
		pods = append(pods, podSandboxToNRI(pod))
	}
	return pods
}
