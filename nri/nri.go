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
	"context"
	"fmt"
	"path"
	"sync"

	"github.com/containerd/containerd/log"
	"github.com/sirupsen/logrus"

	"github.com/containerd/containerd/version"
	nri "github.com/containerd/nri/v2alpha1/pkg/adaptation"
)

// Api implements a common API for interfacing NRI from containerd. It is
// agnostic to any internal containerd implementation details of pods and
// containers. It needs corresponding Domain interfaces for each containerd
// namespace it needs to handle. These domains take care of the namespace-
// specific details of providing pod and container metadata to NRI and of
// applying NRI-requested adjustments to the state of containers.
type Api interface {
	// IsEnabled returns true if the NRI interface is enabled and initialized.
	IsEnabled() bool

	// Start start the NRI interface, allowing external NRI plugins to
	// connect, register, and hook themselves into the lifecycle events
	// of pods and containers.
	Start() error

	// Stop stops the NRI interface.
	Stop()

	// RunPodSandbox relays pod creation events to NRI.
	RunPodSandbox(context.Context, PodSandbox) error

	// StopPodSandbox relays pod shutdown events to NRI.
	StopPodSandbox(context.Context, PodSandbox) error

	// RemovePodSandbox relays pod removal events to NRI.
	RemovePodSandbox(context.Context, PodSandbox) error

	// CreateContainer relays container creation requests to NRI.
	CreateContainer(context.Context, PodSandbox, Container) (*nri.ContainerAdjustment, error)

	// PostCreateContainer relays successful container creation events to NRI.
	PostCreateContainer(context.Context, PodSandbox, Container) error

	// StartContainer relays container start request notifications to NRI.
	StartContainer(context.Context, PodSandbox, Container) error

	// PostStartContainer relays successful container startup events to NRI.
	PostStartContainer(context.Context, PodSandbox, Container) error

	// UpdateContainer relays container update requests to NRI.
	UpdateContainer(context.Context, PodSandbox, Container, *nri.LinuxResources) (*nri.LinuxResources, error)

	// PostUpdateContainer relays successful container update events to NRI.
	PostUpdateContainer(context.Context, PodSandbox, Container) error

	// StopContainer relays container stop requests to NRI.
	StopContainer(context.Context, PodSandbox, Container) error

	// StopContainer relays container removal events to NRI.
	RemoveContainer(context.Context, PodSandbox, Container) error
}

type local struct {
	sync.Mutex
	cfg   *Config
	nri   *nri.Adaptation
	state map[string]nri.ContainerState
}

var _ Api = &local{}

// New creates an instance of the NRI interface with the given configuration.
func New(cfg *Config) (*local, error) {
	logrus.Info("Create NRI interface")

	l := &local{
		cfg: cfg,
	}

	if cfg.Disable {
		logrus.Info("NRI interface is disabled in the configuration.")
		return nil, nil
	}

	var (
		name     = path.Base(version.Package)
		version  = version.Version
		opts     = cfg.toOptions()
		syncFn   = l.syncPlugin
		updateFn = l.updateFromPlugin
		err      error
	)

	l.nri, err = nri.New(name, version, syncFn, updateFn, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize NRI interface: %w", err)
	}

	l.state = make(map[string]nri.ContainerState)

	return l, nil
}

func (l *local) IsEnabled() bool {
	return l != nil && !l.cfg.Disable
}

func (l *local) Start() error {
	if !l.IsEnabled() {
		return nil
	}

	err := l.nri.Start()
	if err != nil {
		return fmt.Errorf("failed to start NRI interface: %w", err)
	}

	return nil
}

func (l *local) Stop() {
	if !l.IsEnabled() {
		return
	}

	l.Lock()
	defer l.Unlock()

	l.nri.Stop()
	l.nri = nil
}

func (l *local) RunPodSandbox(ctx context.Context, pod PodSandbox) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.RunPodSandboxRequest{
		Pod: podSandboxToNRI(pod),
	}

	err := l.nri.RunPodSandbox(ctx, request)

	return err
}

func (l *local) StopPodSandbox(ctx context.Context, pod PodSandbox) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.StopPodSandboxRequest{
		Pod: podSandboxToNRI(pod),
	}

	err := l.nri.StopPodSandbox(ctx, request)

	return err
}

func (l *local) RemovePodSandbox(ctx context.Context, pod PodSandbox) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.RemovePodSandboxRequest{
		Pod: podSandboxToNRI(pod),
	}

	err := l.nri.RemovePodSandbox(ctx, request)

	return err
}

func (l *local) CreateContainer(ctx context.Context, pod PodSandbox, ctr Container) (*nri.ContainerAdjustment, error) {
	if !l.IsEnabled() {
		return nil, nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.CreateContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}

	response, err := l.nri.CreateContainer(ctx, request)
	if err != nil {
		return nil, err
	}

	domains.markPending(ctx, ctr)
	l.markCreated(ctr)

	if _, err := l.applyUpdates(ctx, response.Update); err != nil {
		return nil, err
	}

	return response.Adjust, nil
}

func (l *local) PostCreateContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	updates := domains.clearPending(ctx, ctr)
	_, err := l.applyUpdates(ctx, updates)
	if err != nil {
		log.G(ctx).Error("failed to apply pending updates to container %q", ctr.GetID())
	}

	request := &nri.PostCreateContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}

	err = l.nri.PostCreateContainer(ctx, request)

	return err
}

func (l *local) StartContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.StartContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}

	err := l.nri.StartContainer(ctx, request)

	return err
}

func (l *local) PostStartContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.PostStartContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}

	err := l.nri.PostStartContainer(ctx, request)

	return err
}

func (l *local) UpdateContainer(ctx context.Context, pod PodSandbox, ctr Container, req *nri.LinuxResources) (*nri.LinuxResources, error) {
	if !l.IsEnabled() {
		return nil, nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.UpdateContainerRequest{
		Pod:            podSandboxToNRI(pod),
		Container:      containerToNRI(ctr),
		LinuxResources: req,
	}

	response, err := l.nri.UpdateContainer(ctx, request)
	if err != nil {
		return nil, err
	}

	_, err = l.evictContainers(ctx, response.Evict)
	if err != nil {
		return nil, err
	}

	cnt := len(response.Update)
	if cnt == 0 {
		return nil, nil
	}

	if cnt > 1 {
		_, err = l.applyUpdates(ctx, response.Update[0:cnt-1])
		if err != nil {
			return nil, err
		}
	}

	return response.Update[cnt-1].GetLinux().GetResources(), nil
}

func (l *local) PostUpdateContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.PostUpdateContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}

	err := l.nri.PostUpdateContainer(ctx, request)

	return err
}

func (l *local) StopContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	domains.clearPending(ctx, ctr)

	err := l.stopContainer(ctx, pod, ctr)

	return err
}

func (l *local) stopContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if l.alreadyStopped(ctr) {
		return nil
	}

	request := &nri.StopContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}

	l.markStopped(ctr)

	response, err := l.nri.StopContainer(ctx, request)
	if err != nil {
		return err
	}

	_, err = l.applyUpdates(ctx, response.Update)

	return err
}

func (l *local) forceStopIfNeeded(ctx context.Context, pod PodSandbox, ctr Container) {
	if l.alreadyStopped(ctr) {
		return
	}

	err := l.stopContainer(ctx, pod, ctr)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("NRI force-stop failed for container %q",
			ctr.GetID())
	}
}

func (l *local) RemoveContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	domains.clearPending(ctx, ctr)

	if l.alreadyRemoved(ctr) {
		return nil
	}

	l.forceStopIfNeeded(ctx, pod, ctr)

	request := &nri.RemoveContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}

	l.markRemoved(ctr)

	err := l.nri.RemoveContainer(ctx, request)

	return err
}

func (l *local) syncPlugin(ctx context.Context, syncFn nri.SyncCB) error {
	l.Lock()
	defer l.Unlock()

	log.G(ctx).Info("Synchronizing NRI (plugin) with current runtime state")

	pods := podSandboxesToNRI(domains.listPodSandboxes())
	containers := containersToNRI(domains.listContainers())

	updates, err := syncFn(ctx, pods, containers)
	if err != nil {
		return err
	}

	_, err = l.applyUpdates(ctx, updates)
	if err != nil {
		return err
	}

	return nil
}

func (l *local) updateFromPlugin(ctx context.Context, req []*nri.ContainerUpdate) ([]*nri.ContainerUpdate, error) {
	l.Lock()
	defer l.Unlock()

	log.G(ctx).Info("Unsolicited container update from NRI")

	failed, err := l.applyUpdates(ctx, req)
	return failed, err
}

func (l *local) applyUpdates(ctx context.Context, updates []*nri.ContainerUpdate) ([]*nri.ContainerUpdate, error) {
	// XXX TODO: should we pre-save state and attempt a rollback on failure ?
	failed, err := domains.updateContainers(ctx, updates)
	return failed, err
}

func (l *local) evictContainers(ctx context.Context, evict []*nri.ContainerEviction) ([]*nri.ContainerEviction, error) {
	failed, err := domains.evictContainers(ctx, evict)
	return failed, err
}

func (l *local) markState(ctr Container, state nri.ContainerState) {
	l.state[ctr.GetID()] = state
}

func (l *local) getState(ctr Container) nri.ContainerState {
	state, ok := l.state[ctr.GetID()]
	if !ok {
		return nri.ContainerState_CONTAINER_UNKNOWN
	}

	return state
}

func (l *local) markCreated(ctr Container) {
	l.markState(ctr, nri.ContainerState_CONTAINER_CREATED)
}

func (l *local) markRunning(ctr Container) {
	l.markState(ctr, nri.ContainerState_CONTAINER_RUNNING)
}

func (l *local) markStopped(ctr Container) {
	l.markState(ctr, nri.ContainerState_CONTAINER_STOPPED)
}

func (l *local) markRemoved(ctr Container) {
	delete(l.state, ctr.GetID())
}

func (l *local) alreadyStopped(ctr Container) bool {
	s := l.getState(ctr)
	return s != nri.ContainerState_CONTAINER_CREATED && s != nri.ContainerState_CONTAINER_RUNNING
}

func (l *local) alreadyRemoved(ctr Container) bool {
	s := l.getState(ctr)
	return s == nri.ContainerState_CONTAINER_UNKNOWN
}
