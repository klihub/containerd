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
	"encoding/json"
	"sync"

	"github.com/pkg/errors"
	"golang.org/x/net/context"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/cri/annotations"
	"github.com/containerd/containerd/pkg/cri/constants"
	cstore "github.com/containerd/containerd/pkg/cri/store/container"
	sstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	"github.com/containerd/typeurl"

	cri "k8s.io/cri-api/pkg/apis/runtime/v1"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"

	nrilog "github.com/containerd/nri/v2alpha1/pkg/log"
	nri "github.com/containerd/nri/v2alpha1/pkg/runtime"
	"github.com/containerd/nri/v2alpha1/pkg/runtime/adapt"
)

// nriRuntime is our containerd/NRI adaptation.
type nriRuntime struct {
	lock sync.Mutex   // serialize request processing
	cri  *criService  // associated CRI service
	nri  *nri.Runtime // NRI runtime interface

	// pending updates for containers being created
	pending map[string][]*nri.ContainerUpdate
}

// setupNRI sets up NRI adaptation for the CRI service.
func (s *criService) setupNRI(name, version string) error {
	if !s.config.NRI.Enabled {
		log.L.Info("NRI is disabled")
		return nil
	}

	n := &nriRuntime{
		cri:     s,
		pending: make(map[string][]*nri.ContainerUpdate),
	}

	runtime, err := nri.New(name, version, n.synchronize, n.updateContainers,
		nri.WithConfig(s.config.NRI.Config()),
		nri.WithPluginPath(s.config.NRI.PluginPath),
		nri.WithSocketPath(s.config.NRI.SocketPath))

	if err != nil {
		return errors.Wrap(err, "failed to setup NRI adaptation")
	}

	n.nri = runtime
	s.nri = n

	return nil
}

// isEnabled returns true if NRI is enabled.
func (n *nriRuntime) isEnabled() bool {
	return n != nil
}

// Start the NRI client/plugins.
func (n *nriRuntime) start() error {
	if !n.isEnabled() {
		return nil
	}

	if err := n.nri.Start(); err != nil {
		return errors.Wrap(err, "failed to start NRI runtime")
	}

	return nil
}

// Stop the NRI client/plugins.
func (n *nriRuntime) stop() {
	if !n.isEnabled() {
		return
	}

	n.lock.Lock()
	defer n.lock.Unlock()

	n.nri.Stop()
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

	pods := podSandboxSliceToNRI(n.cri.sandboxStore.List())
	containers := containerSliceToNRI(n.cri.containerStore.List())
	updates, err := cb(ctx, pods, containers)

	if err != nil {
		return err
	}

	if _, ok := namespaces.Namespace(ctx); !ok {
		ctx = namespaces.WithNamespace(ctx, constants.K8sContainerdNamespace)
	}

	_, err = n.applyUpdates(ctx, updates, false)
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
func (n *nriRuntime) updateContainers(ctx context.Context, req []*nri.ContainerUpdate) ([]*nri.ContainerUpdate, error) {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:updateContainers()")

	failed, err := n.applyUpdates(ctx, req, true)

	return failed, err
}

// RunPodSandbox relays pod creation requests to NRI/plugins.
func (n *nriRuntime) RunPodSandbox(ctx context.Context, pod sstore.Sandbox) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:RunPodSandbox(%s)", pod.ID)

	request := &nri.RunPodSandboxRequest{
		Pod: podSandboxToNRI(&pod),
	}

	err := n.nri.RunPodSandbox(ctx, request)

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
		Pod: podSandboxToNRI(&pod),
	}

	err = n.nri.StopPodSandbox(ctx, request)

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
		Pod: podSandboxToNRI(&pod),
	}

	err = n.nri.RemovePodSandbox(ctx, request)

	return err
}

// CreateContainer relays container creation requests to NRI/plugins.
func (n *nriRuntime) CreateContainer(ctx context.Context, c *containers.Container) (*nri.ContainerAdjustment, error) {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:CreateContainer(%s)", c.ID)

	ctr := containerToNRI(c)

	pod, err := n.cri.sandboxStore.Get(ctr.PodSandboxId)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to look up pod for container %s", c.ID)
	}

	request := &nri.CreateContainerRequest{
		Pod:       podSandboxToNRI(&pod),
		Container: ctr,
	}

	response, err := n.nri.CreateContainer(ctx, request)
	if err != nil {
		return nil, err
	}

	_, err = n.applyUpdates(ctx, response.Update, false)
	if err != nil {
		return nil, err
	}

	return response.Adjust, nil
}

// PostCreateContainer relays container post-create events to NRI/plugins.
func (n *nriRuntime) PostCreateContainer(ctx context.Context, ctr *cstore.Container) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:PostCreateContainer(%s)", ctr.ID)

	pod, err := n.cri.sandboxStore.Get(ctr.SandboxID)
	if err != nil {
		return err
	}

	request := &nri.PostCreateContainerRequest{
		Pod:       podSandboxToNRI(&pod),
		Container: containerToNRI(ctr),
	}

	err = n.nri.PostCreateContainer(ctx, request)

	return err
}

// StartContainer relays container start requests to NRI/plugins.
func (n *nriRuntime) StartContainer(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:StartContainer(%s)", id)

	ctr, err := n.cri.containerStore.Get(id)
	if err != nil {
		return err
	}

	pod, err := n.cri.sandboxStore.Get(ctr.SandboxID)
	if err != nil {
		return err
	}

	request := &nri.StartContainerRequest{
		Pod:       podSandboxToNRI(&pod),
		Container: containerToNRI(&ctr),
	}

	err = n.nri.StartContainer(ctx, request)

	return err
}

// PostStartContainer relays container start events to NRI/plugins.
func (n *nriRuntime) PostStartContainer(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:PostStartContainer(%s)", id)

	ctr, err := n.cri.containerStore.Get(id)
	if err != nil {
		return err
	}

	pod, err := n.cri.sandboxStore.Get(ctr.SandboxID)
	if err != nil {
		return err
	}

	request := &nri.PostStartContainerRequest{
		Pod:       podSandboxToNRI(&pod),
		Container: containerToNRI(&ctr),
	}

	err = n.nri.PostStartContainer(ctx, request)

	return err
}

// UpdateContainer relays container update requests to NRI/plugins.
func (n *nriRuntime) UpdateContainer(ctx context.Context, id string, r *cri.LinuxContainerResources, annotations map[string]string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:UpdateContainer(%s)", id)

	ctr, err := n.cri.containerStore.Get(id)
	if err != nil {
		return err
	}

	pod, err := n.cri.sandboxStore.Get(ctr.SandboxID)
	if err != nil {
		return err
	}

	request := &nri.UpdateContainerRequest{
		Pod:            podSandboxToNRI(&pod),
		Container:      containerToNRI(&ctr),
		LinuxResources: linuxResourcesToNRI(r),
		//Annotations:    annotations,
	}

	response, err := n.nri.UpdateContainer(ctx, request)
	if err != nil {
		return err
	}

	if cnt := len(response.Update); cnt > 0 {
		dep := response.Update[0 : cnt-1]
		req := response.Update[cnt-1]

		_, err = n.applyUpdates(ctx, dep, false)
		if err != nil {
			// TODO: We don't do a rollback here. We probably should...
			return err
		}

		if req != nil {
			*r = *req.GetLinux().GetResources().ToCRI(0)
		}
	}

	return nil
}

// StopContainer relays container stop requests to NRI/plugins.
func (n *nriRuntime) StopContainer(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:StopContainer(%s)", id)

	ctr, err := n.cri.containerStore.Get(id)
	if err != nil {
		return err
	}

	pod, err := n.cri.sandboxStore.Get(ctr.SandboxID)
	if err != nil {
		return err
	}

	request := &nri.StopContainerRequest{
		Pod:       podSandboxToNRI(&pod),
		Container: containerToNRI(&ctr),
	}

	response, err := n.nri.StopContainer(ctx, request)
	if err != nil {
		return err
	}

	_, err = n.applyUpdates(ctx, response.Update, false)

	return err
}

// RemoveContainer relays container removal requests to NRI/plugins.
func (n *nriRuntime) RemoveContainer(ctx context.Context, id string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:RemoveContainer(%s)", id)

	delete(n.pending, id)

	ctr, err := n.cri.containerStore.Get(id)
	if err != nil {
		return err
	}

	pod, err := n.cri.sandboxStore.Get(ctr.SandboxID)
	if err != nil {
		return err
	}

	request := &nri.RemoveContainerRequest{
		Pod:       podSandboxToNRI(&pod),
		Container: containerToNRI(&ctr),
	}

	err = n.nri.RemoveContainer(ctx, request)

	return err
}

// RollbackCreateContainer relays synthetic container removal requests to NRI/plugins.
func (n *nriRuntime) RollbackCreateContainer(ctx context.Context, c containerd.Container) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:RollbackCreateContainer(%s)", c.ID())

	delete(n.pending, c.ID())

	ctr := containerToNRI(c)

	pod, err := n.cri.sandboxStore.Get(ctr.PodSandboxId)
	if err != nil {
		return errors.Wrapf(err, "failed to look up pod for container %s", c.ID())
	}

	request := &nri.StopContainerRequest{
		Pod:       podSandboxToNRI(&pod),
		Container: ctr,
	}

	response, err := n.nri.StopContainer(ctx, request)
	if err == nil {
		_, err = n.applyUpdates(ctx, response.Update, false)
	}

	_ = n.nri.RemoveContainer(ctx, &nri.RemoveContainerRequest{
		Pod:       request.Pod,
		Container: request.Container,
	})

	return err
}

// ApplyPendingUpdates applies any pending updates to the given container.
func (n *nriRuntime) ApplyPendingUpdates(ctx context.Context, ContainerID string) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.G(ctx).Infof("NRI-Runtime:ApplyPendingUpdates(%s)", ContainerID)

	updates, ok := n.pending[ContainerID]
	if !ok {
		return nil
	}

	delete(n.pending, ContainerID)
	_, err := n.applyUpdates(ctx, updates, false)
	return err
}

func linuxResourcesToNRI(crir *cri.LinuxContainerResources) *nri.LinuxResources {
	if crir == nil {
		return nil
	}
	shares, quota, period := uint64(crir.CpuShares), crir.CpuQuota, uint64(crir.CpuPeriod)
	r := &nri.LinuxResources{
		Cpu: &nri.LinuxCPU{
			Shares: nri.UInt64(&shares),
			Quota:  nri.Int64(&quota),
			Period: nri.UInt64(&period),
			Cpus:   crir.CpusetCpus,
			Mems:   crir.CpusetMems,
		},
		Memory: &nri.LinuxMemory{
			Limit: nri.Int64(&crir.MemoryLimitInBytes),
		},
	}
	for _, l := range crir.HugepageLimits {
		r.HugepageLimits = append(r.HugepageLimits,
			&nri.HugepageLimit{
				PageSize: l.PageSize,
				Limit:    l.Limit,
			})
	}
	return r
}

func (n *nriRuntime) applyUpdates(ctx context.Context, updates []*nri.ContainerUpdate, collectFailed bool) ([]*nri.ContainerUpdate, error) {
	var failed []*nri.ContainerUpdate

	for _, u := range updates {
		if pending, ok := n.pending[u.ContainerId]; ok {
			n.pending[u.ContainerId] = append(pending, u)
			continue
		}

		container, err := n.cri.containerStore.Get(u.ContainerId)
		if err != nil {
			continue
		}

		if u.Linux == nil && u.Linux.Resources == nil {
			continue
		}

		err = container.Status.Update(func(status cstore.Status) (cstore.Status, error) {
			criReq := &cri.UpdateContainerResourcesRequest{
				ContainerId: u.ContainerId,
				Linux:       u.GetLinux().GetResources().ToCRI(0),
			}
			return status, n.cri.updateContainerResources(ctx, container, criReq, status)
		})

		if err == nil || u.IgnoreFailure {
			continue
		}

		if !collectFailed {
			return nil, errors.Wrapf(err, "failed to update resource of container %s",
				u.ContainerId)
		}

		failed = append(failed, u)
	}

	if len(failed) > 0 {
		return failed, errors.New("some containers failed to udpate")
	}

	return nil, nil
}

// WithPodSandboxExit returns ProcessDeleteOpts to handle a pod exit.
func (n *nriRuntime) WithPodSandboxExit(ctx context.Context, id string) containerd.ProcessDeleteOpts {
	return func(ctx context.Context, p containerd.Process) error {
		if n.isEnabled() {
			err := n.StopPodSandbox(ctx, id)
			return err
		}
		return nil
	}
}

// WithContainerExit returns ProcessDeleteOption to handle a container exit.
func (n *nriRuntime) WithContainerExit(ctx context.Context, id string) containerd.ProcessDeleteOpts {
	return func(ctx context.Context, p containerd.Process) error {
		if n.isEnabled() {
			err := n.StopContainer(ctx, id)
			return err
		}
		return nil
	}
}

// WithNRIAdjustment returns NewContainerOpts for adjusting a container prior to creation.
func (n *nriRuntime) WithNRIAdjustments(opts ...nri.GeneratorOption) containerd.NewContainerOpts {
	return func(ctx context.Context, _ *containerd.Client, c *containers.Container) error {
		spec := &specs.Spec{}
		if err := json.Unmarshal(c.Spec.Value, spec); err != nil {
			return errors.Wrap(err, "failed to unmarshal container OCI Spec")
		}

		adjust, err := n.CreateContainer(ctx, c)
		if err != nil {
			return errors.Wrap(err, "NRI container creation failed")
		}

		specgen := generate.Generator{Config: spec}
		g := nri.SpecGenerator(&specgen, opts...)

		err = g.Adjust(adjust)
		if err != nil {
			return errors.Wrapf(err, "failed to adjust container %s", c.ID)
		}

		cSpec, err := typeurl.MarshalAny(spec)
		if err != nil {
			return errors.Wrap(err, "failed to marshal NRI-adjusted container OCI Spec")
		}
		c.Spec = cSpec

		return nil
	}
}

//
// Runtime/NRI type adaptation
//

func podSandboxToNRI(pod *sstore.Sandbox) *nri.PodSandbox {
	return adapt.PodToNRI(&ctrdPodSandbox{
		Sandbox: pod,
	})
}

func podSandboxSliceToNRI(podList []sstore.Sandbox) []*nri.PodSandbox {
	pods := []*nri.PodSandbox{}
	for _, pod := range podList {
		if pod.Status.Get().State != sstore.StateUnknown {
			pods = append(pods, podSandboxToNRI(&pod))
		}
	}
	return pods
}

func containerToNRI(c interface{}) *nri.Container {
	switch ctr := c.(type) {
	case containerd.Container:
		return adapt.ContainerToNRI(&ctrdContainer{
			ctrd: ctr,
		})
	case *cstore.Container:
		return adapt.ContainerToNRI(&ctrdContainer{
			ctrd: ctr.Container,
		})
	case *containers.Container:
		return adapt.ContainerToNRI(&ctrdContainer{
			ctrs: ctr,
		})
	}
	return nil
}

func containerSliceToNRI(containerList []cstore.Container) []*nri.Container {
	containers := []*nri.Container{}
	for _, container := range containerList {
		switch container.Status.Get().State() {
		case cri.ContainerState_CONTAINER_EXITED:
			continue
		case cri.ContainerState_CONTAINER_UNKNOWN:
			continue
		default:
			containers = append(containers, containerToNRI(&container))
		}
	}
	return containers
}

type ctrdPodSandbox struct {
	*sstore.Sandbox
}

func (p *ctrdPodSandbox) GetID() string {
	return p.ID
}

func (p *ctrdPodSandbox) GetName() string {
	return p.Config.GetMetadata().GetName()
}

func (p *ctrdPodSandbox) GetUID() string {
	return p.Config.GetMetadata().GetUid()
}

func (p *ctrdPodSandbox) GetNamespace() string {
	return p.Config.GetMetadata().GetNamespace()
}

func (p *ctrdPodSandbox) GetAnnotations() map[string]string {
	return p.Config.GetAnnotations()
}

func (p *ctrdPodSandbox) GetLabels() map[string]string {
	return p.Config.GetLabels()
}

func (p *ctrdPodSandbox) GetCgroupParent() string {
	return p.Config.GetLinux().GetCgroupParent()
}

func (p *ctrdPodSandbox) GetRuntimeHandler() string {
	return p.RuntimeHandler
}

type ctrdContainer struct {
	ctrs *containers.Container
	ctrd containerd.Container
	spec *specs.Spec
}

func (c *ctrdContainer) GetSpec() *specs.Spec {
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
		if err := json.Unmarshal(c.ctrs.Spec.Value, spec); err != nil {
			log.L.Errorf("failed to unmarshal OCI Spec for container %s: %v",
				c.ctrs.ID, err)
			return &specs.Spec{}
		}
		c.spec = spec
		return c.spec
	}

	return &specs.Spec{}
}

func (c *ctrdContainer) GetID() string {
	if c.ctrd != nil {
		return c.ctrd.ID()
	}
	if c.ctrs != nil {
		return c.ctrs.ID
	}
	return ""
}

func (c *ctrdContainer) GetPodSandboxID() string {
	return c.GetSpec().Annotations[annotations.SandboxID]
}

func (c *ctrdContainer) GetName() string {
	return c.GetSpec().Annotations[annotations.ContainerName]
}

func (c *ctrdContainer) GetState() nri.ContainerState {
	return nri.ContainerStateUnknown
}

func (c *ctrdContainer) GetLabels() map[string]string {
	switch {
	case c.ctrs != nil:
	case c.ctrd != nil:
		noRefresh := containerd.WithoutRefreshedMetadata
		ctrs, err := c.ctrd.Info(context.TODO(), noRefresh)
		if err != nil {
			log.L.Errorf("failed to get info for container %s: %v", c.ctrd.ID(), err)
			return nil
		}
		c.ctrs = &ctrs
	}

	if c.ctrs != nil {
		return c.ctrs.Labels
	}
	return nil
}

func (c *ctrdContainer) GetAnnotations() map[string]string {
	return c.GetSpec().Annotations
}

func (c *ctrdContainer) GetArgs() []string {
	if p := c.GetSpec().Process; p != nil {
		return nri.DupStringSlice(p.Args)
	}
	return nil
}

func (c *ctrdContainer) GetEnv() []string {
	if p := c.GetSpec().Process; p != nil {
		return nri.DupStringSlice(p.Env)
	}
	return nil
}

func (c *ctrdContainer) GetMounts() []*nri.Mount {
	return nri.FromOCIMounts(c.GetSpec().Mounts)
}

func (c *ctrdContainer) GetHooks() *nri.Hooks {
	return nri.FromOCIHooks(c.GetSpec().Hooks)
}

func (c *ctrdContainer) GetLinuxNamespaces() []*nri.LinuxNamespace {
	spec := c.GetSpec()
	if spec.Linux != nil {
		return nri.FromOCILinuxNamespaces(spec.Linux.Namespaces)
	}
	return nil
}

func (c *ctrdContainer) GetLinuxDevices() []*nri.LinuxDevice {
	spec := c.GetSpec()
	if spec.Linux != nil {
		return nri.FromOCILinuxDevices(spec.Linux.Devices)
	}
	return nil
}

func (c *ctrdContainer) GetLinuxResources() *nri.LinuxResources {
	spec := c.GetSpec()
	if spec.Linux == nil {
		return nil
	}
	return nri.FromOCILinuxResources(spec.Linux.Resources, spec.Annotations)
}

func (c *ctrdContainer) GetOOMScoreAdj() *int {
	if c.GetSpec().Process != nil {
		return c.GetSpec().Process.OOMScoreAdj
	}
	return nil
}

func (c *ctrdContainer) GetCgroupsPath() string {
	if c.GetSpec().Linux == nil {
		return ""
	}
	return c.GetSpec().Linux.CgroupsPath
}

//
// NRI/Runtime logging adaptation
//

const (
	nriLogPrefix = "NRI: "
)

type nriLogger struct{}

func (*nriLogger) Debugf(ctx context.Context, format string, args ...interface{}) {
	if ctx != nil {
		log.G(ctx).Debugf(nriLogPrefix+format, args...)
	} else {
		log.L.Debugf(nriLogPrefix+format, args...)
	}
}

func (*nriLogger) Infof(ctx context.Context, format string, args ...interface{}) {
	if ctx != nil {
		log.G(ctx).Infof(nriLogPrefix+format, args...)
	} else {
		log.L.Infof(nriLogPrefix+format, args...)
	}
}

func (*nriLogger) Warnf(ctx context.Context, format string, args ...interface{}) {
	if ctx != nil {
		log.G(ctx).Warnf(nriLogPrefix+format, args...)
	} else {
		log.L.Warnf(nriLogPrefix+format, args...)
	}
}

func (*nriLogger) Errorf(ctx context.Context, format string, args ...interface{}) {
	if ctx != nil {
		log.G(ctx).Errorf(nriLogPrefix+format, args...)
	} else {
		log.L.Errorf(nriLogPrefix+format, args...)
	}
}

func init() {
	nrilog.Set(&nriLogger{})
}
