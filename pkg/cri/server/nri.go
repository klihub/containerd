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
	"sync"

	"github.com/pkg/errors"
	"golang.org/x/net/context"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/cri/constants"
	sstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	cstore "github.com/containerd/containerd/pkg/cri/store/container"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"

	nri "github.com/containerd/nri/v2alpha1/pkg/runtime"
	nrilog "github.com/containerd/nri/v2alpha1/pkg/log"
)

// nriRuntime is our adaptation for NRI.
type nriRuntime struct {
	lock sync.Mutex   // serialize request processing
	cri  *criService  // associated CRI service
	r    *nri.Runtime // NRI runtime interface

	// pending updates for containers being created
	pending map[string][]*nri.ContainerAdjustment
}

// newNRIRuntime instantiates our NRI adaptation.
func (c *criService) newNRIRuntime() error {
	n := &nriRuntime{
		cri:     c,
		pending: make(map[string][]*nri.ContainerAdjustment),
	}

	r, err := nri.New(n.synchronize, n.adjustContainers)
	if err != nil {
		return errors.Wrap(err, "failed to create NRI client")
	}

	c.nri = n
	n.cri = c
	n.r = r

	return nil
}

// Start the NRI client/plugins.
func (n *nriRuntime) Start() error {
	log.L.Info("NRI-Runtime:Start()")
	n.r.Start()
	return nil
}

// Stop the NRI client/plugins.
func (n *nriRuntime) Stop() {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.L.Info("NRI-Runtime:Stop()")
	n.r.Stop()
}

// WithPodSandboxExit returns a ProcessDeleteOpts to handle pod exit.
func (n *nriRuntime) WithPodSandboxExit(ctx context.Context, id string) containerd.ProcessDeleteOpts {
	return func(ctx context.Context, p containerd.Process) error {
		err := n.StopPodSandbox(ctx, id)
		return err
	}
}

// WithContainerExit returns a ProcessDeleteOption to handle container exit.
func (n *nriRuntime) WithContainerExit(ctx context.Context, id string) containerd.ProcessDeleteOpts {
	return func(ctx context.Context, p containerd.Process) error {
		err := n.StopContainer(ctx, id)
		return err
	}
}

// synchronize a plugin with the state of the runtime state.
//
// Notes:
//   It is implicitly assumed here that the result of synchronization
//   is of no concern to any other existing plugins. Generally this
//   should be true, since during synchronisation only native/compute
//   resources (CPU, memory mostly) can be updated and there should be
//   at most 1 plugin assigning those resources to a container.
func (n *nriRuntime) synchronize(ctx context.Context, cb nri.SyncCB) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Info("NRI-Runtime:synchronize()")

	pods := podSandboxListToNRI(n.cri.sandboxStore.List())
	containers := containerListToNRI(n.cri.containerStore.List())
	updates, err := cb(ctx, pods, containers)

	if err != nil {
		return err
	}

	if _, ok := namespaces.Namespace(ctx); !ok {
		ctx = namespaces.WithNamespace(ctx, constants.K8sContainerdNamespace)
	}

	err = n.applyUpdates(ctx, updates)
	if err != nil {
		return err
	}

	return nil
}

// adjust a set of containers requested by a plugin.
//
// Notes:
//   It is implicitly assumed here that the result of unsolicited
//   container adjustment by any plugin is of no concern to any
//   other plugins.
func (n *nriRuntime) adjustContainers(ctx context.Context, req []*nri.ContainerAdjustment) ([]*nri.ContainerAdjustment, error) {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:adjustContainers()")

	failed, err := n.applyAdjustments(ctx, req)

	return failed, err
}

// RunPodSandbox relays pod creation requests to NRI/plugins.
func (n *nriRuntime) RunPodSandbox(ctx context.Context, pod sstore.Sandbox) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:RunPodSandbox(%s)", pod.ID)

	request := &nri.RunPodSandboxRequest{
		Pod: podSandboxToNRI(pod),
	}

	err := n.r.RunPodSandbox(ctx, request)

	return err
}

// StopPodSandbox relays pod stop requests to NRI/plugins.
func (n *nriRuntime) StopPodSandbox(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:StopPodSandbox(%s)", id)

	pod, err := n.cri.sandboxStore.Get(id)
	if err != nil {
		return err
	}

	request := &nri.StopPodSandboxRequest{
		Pod: podSandboxToNRI(pod),
	}

	err = n.r.StopPodSandbox(ctx, request)

	return err
}

// RemovePodSandbox relays pod removal requests to NRI/plugins.
func (n *nriRuntime) RemovePodSandbox(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:RemovePodSandbox(%s)", id)

	pod, err := n.cri.sandboxStore.Get(id)
	if err != nil {
		return err
	}

	request := &nri.RemovePodSandboxRequest{
		Pod: podSandboxToNRI(pod),
	}

	err = n.r.RemovePodSandbox(ctx, request)

	return err
}

// CreateContainer relays container creation requests to NRI/plugins.
func (n *nriRuntime) CreateContainer(ctx context.Context, meta *cstore.Metadata) (*specs.Hooks, error) {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:CreateContainer(%s)", meta.ID)

	pod, err := n.cri.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return nil, err
	}

	request := &nri.CreateContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(meta),
	}

	response, err := n.r.CreateContainer(ctx, request)
	if err != nil {
		return nil, err
	}

	err = n.applyUpdates(ctx, response.Adjust)
	if err != nil {
		return nil, err
	}

	hooks, err := n.applyCreateChanges(meta, request, response)

	return hooks, err
}

// PostCreateContainer relays container post-create events to NRI/plugins.
func (n *nriRuntime) PostCreateContainer(ctx context.Context, meta *cstore.Metadata) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:PostCreateContainer(%s)", meta.ID)

	pod, err := n.cri.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return err
	}

	request := &nri.PostCreateContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(meta),
	}

	err = n.r.PostCreateContainer(ctx, request)

	return err
}

// StartContainer relays container start requests to NRI/plugins.
func (n *nriRuntime) StartContainer(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:StartContainer(%s)", id)

	container, err := n.cri.containerStore.Get(id)
	if err != nil {
		return err
	}
	meta := &container.Metadata

	pod, err := n.cri.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return err
	}

	request := &nri.StartContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(meta),
	}

	err = n.r.StartContainer(ctx, request)

	return err
}

// PostStartContainer relays container start events to NRI/plugins.
func (n *nriRuntime) PostStartContainer(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:PostStartContainer(%s)", id)

	container, err := n.cri.containerStore.Get(id)
	if err != nil {
		return err
	}
	meta := &container.Metadata

	pod, err := n.cri.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return err
	}

	request := &nri.PostStartContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(meta),
	}

	err = n.r.PostStartContainer(ctx, request)

	return err
}


// UpdateContainer relays container update requests to NRI/plugins.
func (n *nriRuntime) UpdateContainer(ctx context.Context, id string, r *cri.LinuxContainerResources, annotations map[string]string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:UpdateContainer(%s)", id)

	container, err := n.cri.containerStore.Get(id)
	if err != nil {
		return err
	}
	meta := &container.Metadata

	pod, err := n.cri.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return err
	}

	request := &nri.UpdateContainerRequest{
		Pod:            podSandboxToNRI(pod),
		Container:      containerToNRI(meta),
		LinuxResources: linuxResourcesToNRI(r),
		Annotations:    annotations,
	}

	response, err := n.r.UpdateContainer(ctx, request)
	if err != nil {
		return err
	}

	err = n.applyUpdates(ctx, response.Adjust)

	return err
}

// StopContainer relays container stop requests to NRI/plugins.
func (n *nriRuntime) StopContainer(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:StopContainer(%s)", id)

	container, err := n.cri.containerStore.Get(id)
	if err != nil {
		return err
	}
	meta := &container.Metadata

	pod, err := n.cri.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return err
	}

	request := &nri.StopContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(meta),
	}

	response, err := n.r.StopContainer(ctx, request)
	if err != nil {
		return err
	}

	err = n.applyUpdates(ctx, response.Adjust)

	return err
}

// RemoveContainer relays container removal requests to NRI/plugins.
func (n *nriRuntime) RemoveContainer(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:RemoveContainer(%s)", id)

	delete(n.pending, id)

	container, err := n.cri.containerStore.Get(id)
	if err != nil {
		return err
	}
	meta := &container.Metadata

	pod, err := n.cri.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return err
	}

	request := &nri.RemoveContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(meta),
	}

	err = n.r.RemoveContainer(ctx, request)

	return err
}

// RollbackCreateContainer relays synthetic container removal requests to NRI/plugins.
func (n *nriRuntime) RollbackCreateContainer(ctx context.Context, meta *cstore.Metadata) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:RollbackCreateContainer(%s)", meta.ID)

	delete(n.pending, meta.ID)

	pod, err := n.cri.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return nil
	}

	request := &nri.StopContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(meta),
	}

	_, err = n.r.StopContainer(ctx, request)

	_ = n.r.RemoveContainer(ctx, &nri.RemoveContainerRequest{
		Pod:       request.Pod,
		Container: request.Container,
	})

	return err
}

// ApplyPendingUpdates applies any pending updates to the given container.
func (n *nriRuntime) ApplyPendingUpdates(ctx context.Context, ContainerId string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:ApplyPendingUpdates(%s)", ContainerId)

	updates, ok := n.pending[ContainerId]
	if !ok {
		return nil
	}

	delete(n.pending, ContainerId)
	return n.applyUpdates(ctx, updates)
}

func podSandboxToNRI(pod sstore.Sandbox) *nri.PodSandbox {
	return &nri.PodSandbox{
		Id:             pod.ID,
		Name:           pod.Config.GetMetadata().GetName(),
		Uid:            pod.Config.GetMetadata().GetUid(),
		Namespace:      pod.Config.GetMetadata().GetNamespace(),
		Annotations:    pod.Config.GetAnnotations(),
		Labels:         pod.Config.GetLabels(),
		CgroupParent:   pod.Config.GetLinux().GetCgroupParent(),
		RuntimeHandler: pod.RuntimeHandler,
	}
}

func podSandboxListToNRI(podList []sstore.Sandbox) []*nri.PodSandbox {
	pods := []*nri.PodSandbox{}
	for _, pod := range podList {
		if pod.Status.Get().State != sstore.StateUnknown {
			pods = append(pods, podSandboxToNRI(pod))
		}
	}
	return pods
}

func containerToNRI(meta *cstore.Metadata) *nri.Container {
	c := &nri.Container{
		Id:             meta.ID,
		PodSandboxId:   meta.SandboxID,
		Name:           meta.Config.GetMetadata().GetName(),
		Labels:         meta.Config.GetLabels(),
		Annotations:    meta.Config.GetAnnotations(),
		Command:        meta.Config.GetCommand(),
		Args:           meta.Config.GetArgs(),
	}

	for _, e := range meta.Config.GetEnvs() {
		c.Envs = append(c.Envs, &nri.KeyValue{
			Key:   e.Key,
			Value: e.Value,
		})
	}
	for _, m := range meta.Config.GetMounts() {
		c.Mounts = append(c.Mounts, &nri.Mount{
			ContainerPath:  m.ContainerPath,
			HostPath:       m.HostPath,
			Readonly:       m.Readonly,
			SelinuxRelabel: m.SelinuxRelabel,
			Propagation:    nri.MountPropagation(int32(m.Propagation)),
		})
	}
	for _, d := range meta.Config.GetDevices() {
		c.Devices = append(c.Devices, &nri.Device{
			ContainerPath: d.ContainerPath,
			HostPath:      d.HostPath,
			Permissions:   d.Permissions,
		})
	}
	if r := meta.Config.GetLinux().GetResources(); r != nil {
		c.LinuxResources = linuxResourcesToNRI(r)
	}

	return c
}

func containerListToNRI(containerList []cstore.Container) []*nri.Container {
	containers := []*nri.Container{}
	for _, container := range containerList {
		switch container.Status.Get().State() {
		case cri.ContainerState_CONTAINER_EXITED:
			continue
		case cri.ContainerState_CONTAINER_UNKNOWN:
			continue
		default:
			containers = append(containers, containerToNRI(&container.Metadata))
		}
	}
	return containers
}

func linuxResourcesToNRI(cr *cri.LinuxContainerResources) *nri.LinuxContainerResources {
	if cr == nil {
		return nil
	}
	r := &nri.LinuxContainerResources{
		CpuPeriod:          cr.CpuPeriod,
		CpuQuota:           cr.CpuQuota,
		CpuShares:          cr.CpuShares,
		MemoryLimitInBytes: cr.MemoryLimitInBytes,
		OomScoreAdj:        cr.OomScoreAdj,
		CpusetCpus:         cr.CpusetCpus,
		CpusetMems:         cr.CpusetMems,
	}
	for _, l := range cr.HugepageLimits {
		r.HugepageLimits = append(r.HugepageLimits,
			&nri.HugepageLimit{
				PageSize: l.PageSize,
				Limit:    l.Limit,
			})
	}
	return r
}

func linuxResourcesToCRI(r *nri.LinuxContainerResources) *cri.LinuxContainerResources {
	if r == nil {
		return nil
	}
	crir := &cri.LinuxContainerResources{
		CpuPeriod:          r.CpuPeriod,
		CpuQuota:           r.CpuQuota,
		CpuShares:          r.CpuShares,
		MemoryLimitInBytes: r.MemoryLimitInBytes,
		OomScoreAdj:        r.OomScoreAdj,
		CpusetCpus:         r.CpusetCpus,
		CpusetMems:         r.CpusetMems,
	}
	for _, l := range r.HugepageLimits {
		crir.HugepageLimits = append(crir.HugepageLimits,
			&cri.HugepageLimit{
				PageSize: l.PageSize,
				Limit:    l.Limit,
			})
	}
	return crir
}


func (n *nriRuntime) applyCreateChanges(meta *cstore.Metadata, request *nri.CreateContainerRequest, response *nri.CreateContainerResponse) (*specs.Hooks, error) {
	meta.Config.Envs = nil
	for _, e := range response.Create.Envs {
		meta.Config.Envs = append(meta.Config.Envs, &cri.KeyValue{
			Key:   e.Key,
			Value: e.Value,
		})
	}
	meta.Config.Mounts = nil
	for _, m := range response.Create.Mounts {
		meta.Config.Mounts = append(meta.Config.Mounts, &cri.Mount{
			ContainerPath:  m.ContainerPath,
			HostPath:       m.HostPath,
			Readonly:       m.Readonly,
			SelinuxRelabel: m.SelinuxRelabel,
			Propagation:    cri.MountPropagation(int32(m.Propagation)),
		})
	}
	meta.Config.Devices = nil
	for _, d := range response.Create.Devices {
		meta.Config.Devices = append(meta.Config.Devices, &cri.Device{
			ContainerPath: d.ContainerPath,
			HostPath:      d.HostPath,
			Permissions:   d.Permissions,
		})
	}
	if l := response.Create.LinuxResources; l != nil {
		if meta.Config.Linux == nil {
			meta.Config.Linux = &cri.LinuxContainerConfig{}
		}
		meta.Config.Linux.Resources = linuxResourcesToCRI(l)
	}

	return hooksToOCI(response.Create.Hooks), nil
}

func (n *nriRuntime) applyUpdates(ctx context.Context, updates []*nri.ContainerAdjustment) error {
	for _, u := range updates {
		if pending, ok := n.pending[u.ContainerId]; ok {
			n.pending[u.ContainerId] = append(pending, u)
			continue
		}

		container, err := n.cri.containerStore.Get(u.ContainerId)
		if err != nil {
			continue
		}

		err = container.Status.Update(func (status cstore.Status) (cstore.Status, error) {
			resources := linuxResourcesToCRI(u.LinuxResources)
			return status, n.cri.updateContainerResources(ctx, container, resources, status)
		})
		if err != nil {
			return errors.Wrapf(err, "failed to update resources of container %s",
				u.ContainerId)
		}
	}

	return nil
}

func (n *nriRuntime) applyAdjustments(ctx context.Context, updates []*nri.ContainerAdjustment) ([]*nri.ContainerAdjustment, error) {
	var failed []*nri.ContainerAdjustment
	var err error

	for _, u := range updates {
		if u.LinuxResources == nil {
			continue
		}

		if pending, ok := n.pending[u.ContainerId]; ok {
			n.pending[u.ContainerId] = append(pending, u)
			continue
		}

		container, err := n.cri.containerStore.Get(u.ContainerId)
		if err != nil {
			continue
		}

		err = container.Status.Update(func (status cstore.Status) (cstore.Status, error) {
			resources := linuxResourcesToCRI(u.LinuxResources)
			return status, n.cri.updateContainerResources(ctx, container, resources, status)
		})
		if err != nil {
			if u.IgnoreFailure {
				log.G(ctx).Warnf("failed to update container %s", u.ContainerId)
			} else {
				failed = append(failed, u)
				log.G(ctx).Errorf("failed to update container %s", u.ContainerId)
			}
		} else {
			log.G(ctx).Infof("updated container %s", u.ContainerId)
		}
	}

	if len(failed) == 0 {
		failed = nil
		err = nil
	} else {
		err = errors.Errorf("some of the container updates failed")
	}

	return failed, err
}

func hooksToOCI(nriHooks *nri.Hooks) *specs.Hooks {
	if nriHooks == nil {
		return nil
	}
	return &specs.Hooks{
		Prestart:        hookSliceToOCI(nriHooks.Prestart),
		CreateRuntime:   hookSliceToOCI(nriHooks.CreateRuntime),
		CreateContainer: hookSliceToOCI(nriHooks.CreateContainer),
		StartContainer:  hookSliceToOCI(nriHooks.StartContainer),
		Poststart:       hookSliceToOCI(nriHooks.Poststart),
		Poststop:        hookSliceToOCI(nriHooks.Poststop),
	}
}

func hookSliceToOCI(nriHooks []*nri.Hook) []specs.Hook {
	var hooks []specs.Hook
	for _, h := range nriHooks {
		var timeout *int
		if h.Timeout != 0 {
			timeout = new(int)
			*timeout = int(h.Timeout)
		}
		hooks = append(hooks, specs.Hook{
			Path:    h.Path,
			Args:    h.Args,
			Env:     h.Env,
			Timeout: timeout,
		})
	}
	return hooks
}

func keyValueToOCI(keyValue []*nri.KeyValue) []string {
	var env []string
	for _, kv := range keyValue {
		env = append(env, kv.Key+"="+kv.Value)
	}
	return env
}

type nriLogger struct {}

func (*nriLogger) Debugf(ctx context.Context, format string, args ...interface{}) {
	if ctx != nil {
		log.G(ctx).Debugf(format, args...)
	} else {
		log.L.Debugf(format, args...)
	}
}

func (*nriLogger) Infof(ctx context.Context, format string, args ...interface{}) {
	if ctx != nil {
		log.G(ctx).Infof(format, args...)
	} else {
		log.L.Infof(format, args...)
	}
}

func (*nriLogger) Warnf(ctx context.Context, format string, args ...interface{}) {
	if ctx != nil {
		log.G(ctx).Warnf(format, args...)
	} else {
		log.L.Warnf(format, args...)
	}
}

func (*nriLogger) Errorf(ctx context.Context, format string, args ...interface{}) {
	if ctx != nil {
		log.G(ctx).Errorf(format, args...)
	} else {
		log.L.Errorf(format, args...)
	}
}

func init() {
	nrilog.Set(&nriLogger{})
}

