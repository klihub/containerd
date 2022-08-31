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
	"errors"
	"sync"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	nri "github.com/containerd/nri/v2alpha1/pkg/adaptation"
	"github.com/sirupsen/logrus"
)

// Domain implements the functions the generic NRI interface needs to
// deal with pods and containers from a particular containerd namespace.
type Domain interface {
	// GetName() returns the containerd namespace for this domain.
	GetName() string

	// ListPodSandboxes list all pods in this namespace.
	ListPodSandboxes() []PodSandbox

	// ListContainer list all containers in this namespace.
	ListContainers() []Container

	// GetPodSandbox returns the pod for the given ID.
	GetPodSandbox(string) (PodSandbox, bool)

	// GetContainer returns the container for the given ID.
	GetContainer(string) (Container, bool)

	// UpdateContainers applies all NRI container update requests in the namespace.
	// It collects and returns any failed updates.
	UpdateContainers(context.Context, []*nri.ContainerUpdate) ([]*nri.ContainerUpdate, error)

	// EvictContainers evicts all requested containers in the namespace. It collects and
	// returns any failed evictions.
	EvictContainers(context.Context, []*nri.ContainerEviction) ([]*nri.ContainerEviction, error)
}

// RegisterDomain registers a domain for a containerd namespace.
func RegisterDomain(d Domain) {
	logrus.Infof("Registering containerd namespace %q with NRI", d.GetName())
	domains.add(d)
}

type domainTable struct {
	sync.Mutex
	domains map[string]Domain
	pending map[string]map[string][]*nri.ContainerUpdate
}

func (t *domainTable) add(d Domain) {
	t.Lock()
	defer t.Unlock()

	ns := d.GetName()
	t.domains[ns] = d
	t.pending[ns] = make(map[string][]*nri.ContainerUpdate)
}

func (t *domainTable) get(name string) (Domain, bool) {
	t.Lock()
	defer t.Unlock()

	d, ok := t.domains[name]
	return d, ok
}

func (t *domainTable) listPodSandboxes() []PodSandbox {
	var pods []PodSandbox

	t.Lock()
	defer t.Unlock()

	for _, d := range t.domains {
		pods = append(pods, d.ListPodSandboxes()...)
	}
	return pods
}

func (t *domainTable) listContainers() []Container {
	var ctrs []Container

	t.Lock()
	defer t.Unlock()

	for _, d := range t.domains {
		ctrs = append(ctrs, d.ListContainers()...)
	}
	return ctrs
}

func (t *domainTable) getPodSandbox(id string) (PodSandbox, bool) {
	t.Lock()
	defer t.Unlock()

	// XXX TODO: Are ID conflicts across namespaces possible ? Probably...

	for _, d := range t.domains {
		if pod, ok := d.GetPodSandbox(id); ok {
			return pod, true
		}
	}
	return nil, false
}

func (t *domainTable) getContainer(id string) (Container, bool) {
	t.Lock()
	defer t.Unlock()

	// XXX TODO: Are ID conflicts across namespaces possible ? Probably...

	for _, d := range t.domains {
		if ctr, ok := d.GetContainer(id); ok {
			return ctr, true
		}
	}
	return nil, false
}

func (t *domainTable) updateContainers(ctx context.Context, updates []*nri.ContainerUpdate) ([]*nri.ContainerUpdate, error) {
	var (
		perDomain = map[string][]*nri.ContainerUpdate{}
		failed    []*nri.ContainerUpdate
	)

	for _, u := range updates {
		ctr, ok := t.getContainer(u.ContainerId)
		if !ok {
			failed = append(failed, u)
			continue
		}

		ns := ctr.GetDomain()
		perDomain[ns] = append(perDomain[ns], u)
	}

	for ns, u := range perDomain {
		nsCtx := namespaces.WithNamespace(ctx, ns)
		d, _ := t.get(ns)
		f, err := d.UpdateContainers(nsCtx, u)
		if err != nil {
			log.G(ctx).WithError(err).Error("NRI container update failed in namespace %q", ns)
			failed = append(failed, f...)
		}
	}

	if len(failed) > 0 {
		return failed, errors.New("container updates failed")
	}

	return nil, nil
}

func (t *domainTable) evictContainers(ctx context.Context, evict []*nri.ContainerEviction) ([]*nri.ContainerEviction, error) {
	var (
		perDomain = map[string][]*nri.ContainerEviction{}
		failed    []*nri.ContainerEviction
	)

	for _, e := range evict {
		ctr, ok := t.getContainer(e.ContainerId)
		if !ok {
			failed = append(failed, e)
			continue
		}

		ns := ctr.GetDomain()
		perDomain[ns] = append(perDomain[ns], e)
	}

	for ns, e := range perDomain {
		nsCtx := namespaces.WithNamespace(ctx, ns)
		d, _ := t.get(ns)
		f, err := d.EvictContainers(nsCtx, e)
		if err != nil {
			log.G(ctx).WithError(err).Error("NRI container eviction failed in namespace %q", ns)
			failed = append(failed, f...)
		}
	}

	if len(failed) > 0 {
		return failed, errors.New("container eviction failed")
	}

	return nil, nil
}

func (t *domainTable) markPending(ctx context.Context, ctr Container) {
	p, ok := t.pending[ctr.GetDomain()]
	if !ok {
		log.G(ctx).Error("cannot mark pending container %q (unknown namespace %q)",
			ctr.GetID(), ctr.GetDomain())
		return
	}
	p[ctr.GetID()] = []*nri.ContainerUpdate{}
}

func (t *domainTable) clearPending(ctx context.Context, ctr Container) []*nri.ContainerUpdate {
	p, ok := t.pending[ctr.GetDomain()]
	if !ok {
		log.G(ctx).Error("cannot clear pending container %q (unknown namespace %q)",
			ctr.GetID(), ctr.GetDomain())
		return nil
	}

	id := ctr.GetID()
	updates, _ := p[id]
	delete(p, id)

	return updates
}

func (t *domainTable) isPending(ctx context.Context, ctr Container) bool {
	p, ok := t.pending[ctr.GetDomain()]
	if !ok {
		log.G(ctx).Error("cannot check pending container %q (unknown namespace %q)",
			ctr.GetID(), ctr.GetDomain())
		return false
	}
	_, pending := p[ctr.GetID()]
	return pending
}

var domains = &domainTable{
	domains: make(map[string]Domain),
	pending: make(map[string]map[string][]*nri.ContainerUpdate),
}
