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
	nri "github.com/containerd/nri/pkg/adaptation"
)

// Container interface for interacting with NRI.
type Container interface {
	GetDomain() string

	GetPodSandboxID() string
	GetID() string
	GetName() string
	GetStatus() *ContainerStatus
	GetLabels() map[string]string
	GetAnnotations() map[string]string
	GetArgs() []string
	GetEnv() []string
	GetMounts() []*nri.Mount
	GetHooks() *nri.Hooks
	GetLinuxContainer() LinuxContainer
}

type LinuxContainer interface {
	GetLinuxNamespaces() []*nri.LinuxNamespace
	GetLinuxDevices() []*nri.LinuxDevice
	GetLinuxResources() *nri.LinuxResources
	GetOOMScoreAdj() *int
	GetCgroupsPath() string
}

type ContainerStatus struct {
	State      nri.ContainerState
	Reason     string
	Message    string
	Pid        uint32
	CreatedAt  int64
	StartedAt  int64
	FinishedAt int64
	ExitCode   int32
}

func commonContainerToNRI(ctr Container) *nri.Container {
	status := ctr.GetStatus()
	return &nri.Container{
		Id:            ctr.GetID(),
		PodSandboxId:  ctr.GetPodSandboxID(),
		Name:          ctr.GetName(),
		State:         status.State,
		StatusReason:  status.Reason,
		StatusMessage: status.Message,
		Pid:           status.Pid,
		CreatedAt:     status.CreatedAt,
		StartedAt:     status.StartedAt,
		FinishedAt:    status.FinishedAt,
		ExitCode:      status.ExitCode,
		Labels:        ctr.GetLabels(),
		Annotations:   ctr.GetAnnotations(),
		Args:          ctr.GetArgs(),
		Env:           ctr.GetEnv(),
		Mounts:        ctr.GetMounts(),
		Hooks:         ctr.GetHooks(),
	}
}

func containersToNRI(ctrList []Container) []*nri.Container {
	ctrs := make([]*nri.Container, len(ctrList))
	for i, ctr := range ctrList {
		ctrs[i] = containerToNRI(ctr)
	}
	return ctrs
}
