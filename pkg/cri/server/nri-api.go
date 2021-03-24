//go:build linux
// +build linux

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

package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/cri/annotations"
	cstore "github.com/containerd/containerd/pkg/cri/store/container"
	sstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	"github.com/containerd/typeurl"
	"github.com/opencontainers/runtime-spec/specs-go"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/nri"
	"github.com/containerd/containerd/pkg/cri/constants"

	"github.com/containerd/nri/v2alpha1/pkg/api"

	nrigen "github.com/containerd/nri/v2alpha1/pkg/runtime-tools/generate"
)

type nriAPI struct {
	cri *criService
	nri nri.API
}

func (a *nriAPI) register() {
	if !a.isEnabled() {
		return
	}

	nri.RegisterDomain(a)
}

func (a *nriAPI) isEnabled() bool {
	return a != nil && a.nri != nil && a.nri.IsEnabled()
}

//
// CRI 'downward' interface for NRI
//
// These functions are used in the CRI plugin to hook NRI processing into
// the corresponding CRI pod and container lifecycle events.
//

func (a *nriAPI) runPodSandbox(ctx context.Context, criPod *sstore.Sandbox) error {
	pod := &criPodSandbox{
		Sandbox: criPod,
	}

	err := a.nri.RunPodSandbox(ctx, pod)

	if err != nil {
		a.nri.StopPodSandbox(ctx, pod)
		a.nri.RemovePodSandbox(ctx, pod)
	}

	return err
}

func (a *nriAPI) stopPodSandbox(ctx context.Context, criPod *sstore.Sandbox) error {
	pod := &criPodSandbox{
		Sandbox: criPod,
	}

	err := a.nri.StopPodSandbox(ctx, pod)

	return err
}

func (a *nriAPI) removePodSandbox(ctx context.Context, criPod *sstore.Sandbox) error {
	pod := &criPodSandbox{
		Sandbox: criPod,
	}

	err := a.nri.RemovePodSandbox(ctx, pod)

	return err
}

func (a *nriAPI) createContainer(ctx context.Context, ctrs *containers.Container, spec *specs.Spec) (*api.ContainerAdjustment, error) {
	ctr := &criContainer{
		api:  a,
		ctrs: ctrs,
		spec: spec,
	}

	criPod, err := a.cri.sandboxStore.Get(ctr.GetPodSandboxID())
	if err != nil {
		return nil, err
	}

	pod := &criPodSandbox{
		Sandbox: &criPod,
	}

	adjust, err := a.nri.CreateContainer(ctx, pod, ctr)

	return adjust, err
}

func (a *nriAPI) postCreateContainer(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container) error {
	pod := &criPodSandbox{
		Sandbox: criPod,
	}
	ctr := &criContainer{
		api:  a,
		ctrd: criCtr.Container,
	}

	err := a.nri.PostCreateContainer(ctx, pod, ctr)

	return err
}

func (a *nriAPI) startContainer(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container) error {
	pod := &criPodSandbox{
		Sandbox: criPod,
	}
	ctr := &criContainer{
		api:  a,
		ctrd: criCtr.Container,
	}

	err := a.nri.StartContainer(ctx, pod, ctr)

	return err
}

func (a *nriAPI) postStartContainer(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container) error {
	pod := &criPodSandbox{
		Sandbox: criPod,
	}
	ctr := &criContainer{
		api:  a,
		ctrd: criCtr.Container,
	}

	err := a.nri.PostStartContainer(ctx, pod, ctr)

	return err
}

func (a *nriAPI) updateContainer(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container, req *cri.LinuxContainerResources) (*cri.LinuxContainerResources, error) {
	const noOomAdj = 0

	pod := &criPodSandbox{
		Sandbox: criPod,
	}
	ctr := &criContainer{
		api:  a,
		ctrd: criCtr.Container,
	}

	r, err := a.nri.UpdateContainer(ctx, pod, ctr, api.FromCRILinuxResources(req))
	if err != nil {
		return nil, err
	}

	return r.ToCRI(noOomAdj), nil
}

func (a *nriAPI) postUpdateContainer(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container) error {
	pod := &criPodSandbox{
		Sandbox: criPod,
	}
	ctr := &criContainer{
		api:  a,
		ctrd: criCtr.Container,
	}

	err := a.nri.PostUpdateContainer(ctx, pod, ctr)

	return err
}

func (a *nriAPI) stopContainer(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container) error {
	ctr := &criContainer{
		api:  a,
		ctrd: criCtr.Container,
	}

	if criPod == nil || criPod.ID == "" {
		criPod = &sstore.Sandbox{
			Metadata: sstore.Metadata{
				ID: ctr.GetPodSandboxID(),
			},
		}
	}
	pod := &criPodSandbox{
		Sandbox: criPod,
	}

	err := a.nri.StopContainer(ctx, pod, ctr)

	return err
}

func (a *nriAPI) removeContainer(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container) error {
	pod := &criPodSandbox{
		Sandbox: criPod,
	}
	ctr := &criContainer{
		api:  a,
		ctrd: criCtr.Container,
	}

	err := a.nri.RemoveContainer(ctx, pod, ctr)

	return err
}

func (a *nriAPI) undoCreateContainer(ctx context.Context, criPod *sstore.Sandbox, id string, spec *specs.Spec) {
	deferCtx, deferCancel := ctrdutil.DeferContext()
	defer deferCancel()

	pod := &criPodSandbox{
		Sandbox: criPod,
	}
	ctr := &criContainer{
		api: a,
		ctrs: &containers.Container{
			ID: id,
		},
		spec: spec,
	}

	err := a.nri.StopContainer(deferCtx, pod, ctr)
	if err != nil {
		log.G(deferCtx).WithError(err).Error("container creation undo (stop) failed")
	}

	err = a.nri.RemoveContainer(deferCtx, pod, ctr)
	if err != nil {
		log.G(deferCtx).WithError(err).Error("container creation undo (remove) failed")
	}
}

func (a *nriAPI) WithContainerAdjustment() containerd.NewContainerOpts {
	resourceCheckOpt := nrigen.WithResourceChecker(
		func(r *runtimespec.LinuxResources) error {
			if r != nil {
				if a.cri.config.DisableHugetlbController {
					r.HugepageLimits = nil
				}
			}
			return nil
		},
	)

	return func(ctx context.Context, _ *containerd.Client, c *containers.Container) error {
		spec := &specs.Spec{}
		if err := json.Unmarshal(c.Spec.GetValue(), spec); err != nil {
			return fmt.Errorf("failed to unmarshal container OCI Spec for NRI: %w", err)
		}

		adjust, err := a.createContainer(ctx, c, spec)
		if err != nil {
			return fmt.Errorf("failed to get NRI-adjustment for container: %w", err)
		}

		sgen := generate.Generator{Config: spec}
		ngen := nrigen.SpecGenerator(&sgen, resourceCheckOpt)

		err = ngen.Adjust(adjust)
		if err != nil {
			return fmt.Errorf("failed to NRI-adjust container Spec: %w", err)
		}

		adjusted, err := typeurl.MarshalAny(spec)
		if err != nil {
			return fmt.Errorf("failed to marshal NRI-adjusted Spec: %w", err)
		}

		c.Spec = adjusted
		return nil
	}
}

func (a *nriAPI) WithPodSandboxStop(criPod *sstore.Sandbox) containerd.ProcessDeleteOpts {
	if !a.isEnabled() {
		return func(_ context.Context, _ containerd.Process) error {
			return nil
		}
	}

	return func(ctx context.Context, _ containerd.Process) error {
		return a.stopPodSandbox(ctx, criPod)
	}
}

func (a *nriAPI) WithContainerStop(criCtr *cstore.Container) containerd.ProcessDeleteOpts {
	if !a.isEnabled() {
		return func(_ context.Context, _ containerd.Process) error {
			return nil
		}
	}

	return func(ctx context.Context, _ containerd.Process) error {
		criPod, _ := a.cri.sandboxStore.Get(criCtr.SandboxID)
		return a.stopContainer(ctx, &criPod, criCtr)
	}
}

//
// CRI 'upward' interface for NRI
//
// This implements the 'CRI domain' for the common NRI interface plugin.
// It takes care of the CRI-specific details of interfacing from NRI to
// CRI (container and pod discovery, container adjustment and updates).
//

const (
	nriDomain = constants.K8sContainerdNamespace
)

func (a *nriAPI) GetName() string {
	return nriDomain
}

func (a *nriAPI) ListPodSandboxes() []nri.PodSandbox {
	pods := []nri.PodSandbox{}
	for _, pod := range a.cri.sandboxStore.List() {
		if pod.Status.Get().State != sstore.StateUnknown {
			pod := pod
			pods = append(pods, &criPodSandbox{
				Sandbox: &pod,
			})
		}
	}
	return pods
}

func (a *nriAPI) ListContainers() []nri.Container {
	containers := []nri.Container{}
	for _, ctr := range a.cri.containerStore.List() {
		switch ctr.Status.Get().State() {
		case cri.ContainerState_CONTAINER_EXITED:
			continue
		case cri.ContainerState_CONTAINER_UNKNOWN:
			continue
		}
		containers = append(containers, &criContainer{
			api:  a,
			ctrd: ctr.Container,
		})
	}
	return containers
}

func (a *nriAPI) GetPodSandbox(id string) (nri.PodSandbox, bool) {
	pod, err := a.cri.sandboxStore.Get(id)
	if err != nil {
		return nil, false
	}

	return &criPodSandbox{
		Sandbox: &pod,
	}, true
}

func (a *nriAPI) GetContainer(id string) (nri.Container, bool) {
	ctr, err := a.cri.containerStore.Get(id)
	if err != nil {
		return nil, false
	}

	return &criContainer{
		api:  a,
		ctrd: ctr.Container,
	}, true
}

func (a *nriAPI) UpdateContainer(ctx context.Context, u *api.ContainerUpdate) error {
	ctr, err := a.cri.containerStore.Get(u.ContainerId)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to update CRI container %q", u.ContainerId)
		if !errdefs.IsNotFound(err) && !u.IgnoreFailure {
			return err
		}
		return nil
	}
	err = ctr.Status.Update(
		func(status cstore.Status) (cstore.Status, error) {
			criReq := &cri.UpdateContainerResourcesRequest{
				ContainerId: u.ContainerId,
				Linux:       u.GetLinux().GetResources().ToCRI(0),
			}
			newStatus, err := a.cri.updateContainerResources(ctx, ctr, criReq, status)
			return newStatus, err
		},
	)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to update CRI container %q", u.ContainerId)
		if !u.IgnoreFailure {
			return err
		}
	}

	return nil
}

func (a *nriAPI) EvictContainer(ctx context.Context, e *api.ContainerEviction) error {
	ctr, err := a.cri.containerStore.Get(e.ContainerId)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to evict CRI container %q", e.ContainerId)
		if !errdefs.IsNotFound(err) {
			return err
		}
		return nil
	}
	err = a.cri.stopContainer(ctx, ctr, 0)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to evict CRI container %q", e.ContainerId)
		return err
	}

	return nil
}

//
// NRI integration wrapper for CRI Pods
//

type criPodSandbox struct {
	*sstore.Sandbox
}

func (p *criPodSandbox) GetDomain() string {
	return nriDomain
}

func (p *criPodSandbox) GetID() string {
	if p.Sandbox == nil {
		return ""
	}
	return p.ID
}

func (p *criPodSandbox) GetName() string {
	if p.Sandbox == nil {
		return ""
	}
	return p.Config.GetMetadata().GetName()
}

func (p *criPodSandbox) GetUID() string {
	if p.Sandbox == nil {
		return ""
	}
	return p.Config.GetMetadata().GetUid()
}

func (p *criPodSandbox) GetNamespace() string {
	if p.Sandbox == nil {
		return ""
	}
	return p.Config.GetMetadata().GetNamespace()
}

func (p *criPodSandbox) GetAnnotations() map[string]string {
	if p.Sandbox == nil {
		return nil
	}
	return p.Config.GetAnnotations()
}

func (p *criPodSandbox) GetLabels() map[string]string {
	if p.Sandbox == nil {
		return nil
	}
	return p.Config.GetLabels()
}

func (p *criPodSandbox) GetCgroupParent() string {
	if p.Sandbox == nil {
		return ""
	}
	return p.Config.GetLinux().GetCgroupParent()
}

func (p *criPodSandbox) GetRuntimeHandler() string {
	if p.Sandbox == nil {
		return ""
	}
	return p.RuntimeHandler
}

//
// NRI integration wrapper for CRI Containers
//

type criContainer struct {
	api  *nriAPI
	ctrs *containers.Container
	ctrd containerd.Container
	spec *specs.Spec
}

func (c *criContainer) GetDomain() string {
	return nriDomain
}

func (c *criContainer) GetID() string {
	if c.ctrs != nil {
		return c.ctrs.ID
	}
	if c.ctrd != nil {
		return c.ctrd.ID()
	}
	return ""
}

func (c *criContainer) GetPodSandboxID() string {
	return c.GetSpec().Annotations[annotations.SandboxID]
}

func (c *criContainer) GetName() string {
	return c.GetSpec().Annotations[annotations.ContainerName]
}

func (c *criContainer) GetState() api.ContainerState {
	criCtr, err := c.api.cri.containerStore.Get(c.GetID())
	if err != nil {
		return api.ContainerState_CONTAINER_UNKNOWN
	}
	switch criCtr.Status.Get().State() {
	case cri.ContainerState_CONTAINER_CREATED:
		return api.ContainerState_CONTAINER_CREATED
	case cri.ContainerState_CONTAINER_RUNNING:
		return api.ContainerState_CONTAINER_RUNNING
	case cri.ContainerState_CONTAINER_EXITED:
		return api.ContainerState_CONTAINER_STOPPED
	}

	return api.ContainerState_CONTAINER_UNKNOWN
}

func (c *criContainer) GetLabels() map[string]string {
	switch {
	case c.ctrs != nil:
		return c.ctrs.Labels
	case c.ctrd != nil:
		noRefresh := containerd.WithoutRefreshedMetadata
		ctrs, err := c.ctrd.Info(context.TODO(), noRefresh)
		if err != nil {
			log.L.Errorf("failed to get info for container %s: %v", c.ctrd.ID(), err)
			return nil
		}
		c.ctrs = &ctrs
		return ctrs.Labels
	}

	return nil
}

func (c *criContainer) GetAnnotations() map[string]string {
	return c.GetSpec().Annotations
}

func (c *criContainer) GetArgs() []string {
	if p := c.GetSpec().Process; p != nil {
		return api.DupStringSlice(p.Args)
	}
	return nil
}

func (c *criContainer) GetEnv() []string {
	if p := c.GetSpec().Process; p != nil {
		return api.DupStringSlice(p.Env)
	}
	return nil
}

func (c *criContainer) GetMounts() []*api.Mount {
	return api.FromOCIMounts(c.GetSpec().Mounts)
}

func (c *criContainer) GetHooks() *api.Hooks {
	return api.FromOCIHooks(c.GetSpec().Hooks)
}

func (c *criContainer) GetLinuxContainer() nri.LinuxContainer {
	return c
}

func (c *criContainer) GetLinuxNamespaces() []*api.LinuxNamespace {
	spec := c.GetSpec()
	if spec.Linux != nil {
		return api.FromOCILinuxNamespaces(spec.Linux.Namespaces)
	}
	return nil
}

func (c *criContainer) GetLinuxDevices() []*api.LinuxDevice {
	spec := c.GetSpec()
	if spec.Linux != nil {
		return api.FromOCILinuxDevices(spec.Linux.Devices)
	}
	return nil
}

func (c *criContainer) GetLinuxResources() *api.LinuxResources {
	spec := c.GetSpec()
	if spec.Linux == nil {
		return nil
	}
	return api.FromOCILinuxResources(spec.Linux.Resources, spec.Annotations)
}

func (c *criContainer) GetOOMScoreAdj() *int {
	if c.GetSpec().Process != nil {
		return c.GetSpec().Process.OOMScoreAdj
	}
	return nil
}

func (c *criContainer) GetCgroupsPath() string {
	if c.GetSpec().Linux == nil {
		return ""
	}
	return c.GetSpec().Linux.CgroupsPath
}

func (c *criContainer) GetSpec() *specs.Spec {
	if c.spec != nil {
		return c.spec
	}

	if c.ctrd != nil {
		spec, err := c.ctrd.Spec(ctrdutil.NamespacedContext())
		if err != nil {
			log.L.Errorf("failed to fetch OCI Spec for container %s: %v",
				c.ctrd.ID(), err)
			return &specs.Spec{}
		}
		c.spec = spec
		return c.spec
	}

	if c.ctrs != nil {
		spec := &specs.Spec{}
		if err := json.Unmarshal(c.ctrs.Spec.GetValue(), spec); err != nil {
			log.L.Errorf("failed to unmarshal OCI Spec for container %s: %v",
				c.ctrs.ID, err)
			return &specs.Spec{}
		}
		c.spec = spec
		return c.spec
	}

	return &specs.Spec{}
}
