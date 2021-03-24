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
	cri "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"

	nri "github.com/containerd/nri/client/vproto"
	api "github.com/containerd/nri/api/plugin/vproto"
)

// nriClient is our adaptation for NRI.
type nriClient struct {
	lock   sync.Mutex  // serialize request processing
	cri    *criService // associated CRI service
	client *nri.Client // NRI client for interacting with plugins

	// pending updates for containers being created
	pending map[string][]*api.ContainerUpdate
}

// newNRIClient instantiates our NRI adaptation.
func newNRIClient(cri *criService) (*nriClient, error) {
	n := &nriClient{
		cri:     cri,
		pending: make(map[string][]*api.ContainerUpdate),
	}

	client, err := nri.NewClient(n.synchronize)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create NRI client")
	}
	n.client = client

	return n, nil
}

// Start the NRI client/plugins.
func (n *nriClient) Start() error {
	log.L.Info("NRI-Client:Start()")
	n.client.Start()
	return nil
}

// Stop the NRI client/plugins.
func (n *nriClient) Stop() {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.L.Info("NRI-Client:Stop()")
	n.client.Stop()
}

// WithPodSandboxExit returns a ProcessDeleteOpts to handle pod exit.
func (n *nriClient) WithPodSandboxExit(ctx context.Context, id string) containerd.ProcessDeleteOpts {
	return func(ctx context.Context, p containerd.Process) error {
		err := n.StopPodSandbox(ctx, id)
		return err
	}
}

// WithContainerExit returns a ProcessDeleteOption to handle container exit.
func (n *nriClient) WithContainerExit(ctx context.Context, id string) containerd.ProcessDeleteOpts {
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
func (n *nriClient) synchronize(ctx context.Context, cb nri.SyncCB) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Info("NRI-Client:synchronize()")

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

// RunPodSandbox relays pod creation requests to NRI/plugins.
func (n *nriClient) RunPodSandbox(ctx context.Context, pod sstore.Sandbox) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Client:RunPodSandbox(%s)", pod.ID)

	request := &api.RunPodSandboxRequest{
		Pod: podSandboxToNRI(pod),
	}

	err := n.client.RunPodSandbox(ctx, request)

	return err
}

// StopPodSandbox relays pod stop requests to NRI/plugins.
func (n *nriClient) StopPodSandbox(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Client:StopPodSandbox(%s)", id)

	pod, err := n.cri.sandboxStore.Get(id)
	if err != nil {
		return err
	}

	request := &api.StopPodSandboxRequest{
		Pod: podSandboxToNRI(pod),
	}

	err = n.client.StopPodSandbox(ctx, request)

	return err
}

// RemovePodSandbox relays pod removal requests to NRI/plugins.
func (n *nriClient) RemovePodSandbox(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Client:RemovePodSandbox(%s)", id)

	pod, err := n.cri.sandboxStore.Get(id)
	if err != nil {
		return err
	}

	request := &api.RemovePodSandboxRequest{
		Pod: podSandboxToNRI(pod),
	}

	err = n.client.RemovePodSandbox(ctx, request)

	return err
}

// CreateContainer relays container creation requests to NRI/plugins.
func (n *nriClient) CreateContainer(ctx context.Context, meta *cstore.Metadata) (*specs.Hooks, error) {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Client:CreateContainer(%s)", meta.ID)

/*
	name := meta.Config.Metadata.Name
	logDumpObject("original "+name+": ", meta)
*/

	pod, err := n.cri.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return nil, err
	}

	request := &api.CreateContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(meta),
	}

	response, err := n.client.CreateContainer(ctx, request)
	if err != nil {
		return nil, err
	}

	err = n.applyUpdates(ctx, response.Updates)
	if err != nil {
		return nil, err
	}

	hooks, err := n.applyCreateChanges(meta, request, response)

/*
	if err == nil {
		logDumpObject("updated "+name+": ", meta)
	}
*/

	return hooks, err
}

// StartContainer relays container start requests to NRI/plugins.
func (n *nriClient) StartContainer(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Client:StartContainer(%s)", id)

	container, err := n.cri.containerStore.Get(id)
	if err != nil {
		return err
	}
	meta := &container.Metadata

	pod, err := n.cri.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return err
	}

	request := &api.StartContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(meta),
	}

	err = n.client.StartContainer(ctx, request)

	return err
}

// UpdateContainer relays container update requests to NRI/plugins.
func (n *nriClient) UpdateContainer(ctx context.Context, id string, r *cri.LinuxContainerResources, annotations map[string]string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Client:UpdateContainer(%s)", id)

	container, err := n.cri.containerStore.Get(id)
	if err != nil {
		return err
	}
	meta := &container.Metadata

	pod, err := n.cri.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return err
	}

	request := &api.UpdateContainerRequest{
		Pod:            podSandboxToNRI(pod),
		Container:      containerToNRI(meta),
		LinuxResources: r,
		Annotations:    annotations,
	}

	response, err := n.client.UpdateContainer(ctx, request)
	if err != nil {
		return err
	}

	err = n.applyUpdates(ctx, response.Updates)

	return err
}

// StopContainer relays container stop requests to NRI/plugins.
func (n *nriClient) StopContainer(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Client:StopContainer(%s)", id)

	container, err := n.cri.containerStore.Get(id)
	if err != nil {
		return err
	}
	meta := &container.Metadata

	pod, err := n.cri.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return err
	}

	request := &api.StopContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(meta),
	}

	response, err := n.client.StopContainer(ctx, request)
	if err != nil {
		return err
	}

	err = n.applyUpdates(ctx, response.Updates)

	return err
}

// RemoveContainer relays container removal requests to NRI/plugins.
func (n *nriClient) RemoveContainer(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Client:RemoveContainer(%s)", id)

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

	request := &api.RemoveContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(meta),
	}

	err = n.client.RemoveContainer(ctx, request)

	return err
}

// RollbackCreateContainer relays synthetic container removal requests to NRI/plugins.
func (n *nriClient) RollbackCreateContainer(ctx context.Context, meta *cstore.Metadata) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Client:RollbackCreateContainer(%s)", meta.ID)

	delete(n.pending, meta.ID)

	pod, err := n.cri.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return nil
	}

	request := &api.StopContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(meta),
	}

	_, err = n.client.StopContainer(ctx, request)

	_ = n.client.RemoveContainer(ctx, &api.RemoveContainerRequest{
		Pod:       request.Pod,
		Container: request.Container,
	})

	return err
}

// ApplyPendingUpdates applies any pending updates to the given container.
func (n *nriClient) ApplyPendingUpdates(ctx context.Context, ContainerId string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Client:ApplyPendingUpdates(%s)", ContainerId)

	updates, ok := n.pending[ContainerId]
	if !ok {
		return nil
	}

	delete(n.pending, ContainerId)
	return n.applyUpdates(ctx, updates)
}

func podSandboxToNRI(pod sstore.Sandbox) *api.PodSandbox {
	return &api.PodSandbox{
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

func podSandboxListToNRI(podList []sstore.Sandbox) []*api.PodSandbox {
	pods := []*api.PodSandbox{}
	for _, pod := range podList {
		if pod.Status.Get().State != sstore.StateUnknown {
			pods = append(pods, podSandboxToNRI(pod))
		}
	}
	return pods
}

func containerToNRI(meta *cstore.Metadata) *api.Container {
	return &api.Container{
		Id:             meta.ID,
		PodSandboxId:   meta.SandboxID,
		Name:           meta.Config.GetMetadata().GetName(),
		Labels:         meta.Config.GetLabels(),
		Annotations:    meta.Config.GetAnnotations(),
		Command:        meta.Config.GetCommand(),
		Args:           meta.Config.GetArgs(),
		Envs:           meta.Config.GetEnvs(),
		Mounts:         meta.Config.GetMounts(),
		Devices:        meta.Config.GetDevices(),
		LinuxResources: meta.Config.GetLinux().GetResources(),
	}
}

func containerListToNRI(containerList []cstore.Container) []*api.Container {
	containers := []*api.Container{}
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

func (n *nriClient) applyCreateChanges(meta *cstore.Metadata, request *api.CreateContainerRequest, response *api.CreateContainerResponse) (*specs.Hooks, error) {
	meta.Config.Labels = request.Container.Labels
	meta.Config.Annotations = request.Container.Annotations
	meta.Config.Envs = request.Container.GetEnvs()
	meta.Config.Mounts = request.Container.GetMounts()
	meta.Config.Devices = request.Container.GetDevices()
	meta.Config.Linux.Resources = request.Container.LinuxResources

	return hooksToOCI(response.Create.Hooks), nil
}

func (n *nriClient) applyUpdates(ctx context.Context, updates []*api.ContainerUpdate) error {
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
			resources := u.LinuxResources
			return status, n.cri.updateContainerResources(ctx, container, resources, status)
		})
		if err != nil {
			return errors.Wrapf(err, "failed to update resources of container %s",
				u.ContainerId)
		}
	}

	return nil
}

func hooksToOCI(nriHooks []*api.Hooks) *specs.Hooks {
	hooks := &specs.Hooks{}
	for _, h := range nriHooks {
		hooks.Prestart = append(hooks.Prestart,
			hookSliceToOCI(h.Prestart)...)
		hooks.CreateRuntime = append(hooks.CreateRuntime,
			hookSliceToOCI(h.CreateRuntime)...)
		hooks.CreateContainer = append(hooks.CreateContainer,
			hookSliceToOCI(h.CreateContainer)...)
	}
	return hooks
}

func hookSliceToOCI(nriHooks []*api.Hook) []specs.Hook {
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
			Env:     keyValueToOCI(h.Env),
			Timeout: timeout,
		})
	}
	return hooks
}

func keyValueToOCI(keyValue []*api.KeyValue) []string {
	var env []string
	for _, kv := range keyValue {
		env = append(env, kv.Key+"="+kv.Value)
	}
	return env
}

type nriLogging struct {}

func (*nriLogging) Context(ctx context.Context) nri.Logger {
	return log.G(ctx)
}

func (*nriLogging) Default() nri.Logger {
	return log.L
}

func init() {
	nri.SetLogging(&nriLogging{})
}
