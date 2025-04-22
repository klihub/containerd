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

package api

import (
	"maps"
	"slices"
)

// Equal tests if two ContainerAdjustments are equal.
func (a *ContainerAdjustment) Equal(o *ContainerAdjustment) bool {
	if a == nil && o == nil {
		return true
	}

	// Note: either a or o can be nil here

	if len(a.Annotations) != len(o.Annotations) {
		return false
	}
	for k, av := range a.Annotations {
		if av != o.Annotations[k] {
			return false
		}
	}

	if len(a.Mounts) != len(o.Mounts) {
		return false
	}
	for i := range a.Mounts {
		if !a.Mounts[i].Equal(o.Mounts[i]) {
			return false
		}
	}

	if len(a.Env) != len(o.Env) {
		return false
	}
	for i := range a.Env {
		if !a.Env[i].Equal(o.Env[i]) {
			return false
		}
	}

	if !a.Hooks.Equal(o.Hooks) {
		return false
	}

	if !a.Linux.Equal(o.Linux) {
		return false
	}

	if len(a.Rlimits) != len(o.Rlimits) {
		return false
	}
	for i := range a.Rlimits {
		if !a.Rlimits[i].Equal(o.Rlimits[i]) {
			return false
		}
	}

	if len(a.CDIDevices) != len(o.CDIDevices) {
		return false
	}
	for i := range a.CDIDevices {
		if !a.CDIDevices[i].Equal(o.CDIDevices[i]) {
			return false
		}
	}

	return slices.Equal(a.Args, o.Args)
}

// Equal checks if two LinuxContainerAdjustments are equal.
func (l *LinuxContainerAdjustment) Equal(o *LinuxContainerAdjustment) bool {
	if l == nil && o == nil {
		return true
	}

	// Note: either l or o can be nil here

	if len(l.GetDevices()) != len(o.GetDevices()) {
		return false
	}
	for i := range l.GetDevices() {
		if !l.Devices[i].Equal(o.Devices[i]) {
			return false
		}
	}
	if !l.GetResources().Equal(o.GetResources()) {
		return false
	}

	if l.GetCgroupsPath() != o.GetCgroupsPath() {
		return false
	}
	if l.GetOomScoreAdj() != o.GetOomScoreAdj() &&
		l.GetOomScoreAdj().GetValue() != o.GetOomScoreAdj().GetValue() {
		return false
	}

	return true
}

// Equal checks if two ContainerUpdates are equal.
func (u *ContainerUpdate) Equal(o *ContainerUpdate) bool {
	if u == nil && o == nil {
		return true
	}
	if u == nil || o == nil {
		return false
	}

	if u.ContainerId != o.ContainerId || u.IgnoreFailure != o.IgnoreFailure {
		return false
	}

	if !u.Linux.Equal(o.Linux) {
		return false
	}

	return true
}

// Equal checks if two LinuxContainerUpdates are equal.
func (u *LinuxContainerUpdate) Equal(o *LinuxContainerUpdate) bool {
	if u == nil && o == nil {
		return true
	}
	if u == nil || o == nil {
		return false
	}

	if !u.Resources.Equal(o.Resources) {
		return false
	}

	return true
}

// Equal checks if two LinuxResources are equal.
func (r *LinuxResources) Equal(o *LinuxResources) bool {
	if r == nil && o == nil {
		return true
	}

	// Note: either r or o can be nil here

	if !r.GetMemory().Equal(o.GetMemory()) {
		return false
	}

	if !r.GetCpu().Equal(o.GetCpu()) {
		return false
	}

	if len(r.GetHugepageLimits()) != len(o.GetHugepageLimits()) {
		return false
	}
	for i := range r.GetHugepageLimits() {
		if !r.HugepageLimits[i].Equal(o.HugepageLimits[i]) {
			return false
		}
	}

	if r.GetBlockioClass() != o.GetBlockioClass() &&
		r.GetBlockioClass().GetValue() != o.GetBlockioClass().GetValue() {
		return false
	}
	if r.GetRdtClass() != o.GetRdtClass() &&
		r.GetRdtClass().GetValue() != o.GetRdtClass().GetValue() {
		return false
	}

	if len(r.GetUnified()) != len(o.GetUnified()) {
		return false
	}

	if !maps.Equal(r.GetUnified(), o.GetUnified()) {
		return false
	}

	if len(r.GetDevices()) != len(o.GetDevices()) {
		return false
	}
	for i := range r.GetDevices() {
		if !r.Devices[i].Equal(o.Devices[i]) {
			return false
		}
	}

	if (r.GetPids() != nil && o.GetPids() == nil) || (r.GetPids() == nil && o.GetPids() != nil) {
		return false
	}
	if r.GetPids().GetLimit() != o.GetPids().GetLimit() {
		return false
	}

	return true
}

// Equal checks if two LinuxMemories are equal.
func (m *LinuxMemory) Equal(o *LinuxMemory) bool {
	if m == nil && o == nil {
		return true
	}

	// Note: either m or o can be nil here

	if m == nil && o != nil {
		if o.GetLimit() != nil {
			return false
		}
		if o.GetReservation() != nil {
			return false
		}
		if o.GetSwap() != nil {
			return false
		}
		if o.GetKernel() != nil {
			return false
		}
		if o.GetKernelTcp() != nil {
			return false
		}
		if o.GetSwappiness() != nil {
			return false
		}
		if o.GetDisableOomKiller() != nil {
			return false
		}
		if o.GetUseHierarchy() != nil {
			return false
		}

		return true
	}

	if m != nil && o == nil {
		if m.GetLimit() != nil {
			return false
		}
		if m.GetReservation() != nil {
			return false
		}
		if m.GetSwap() != nil {
			return false
		}
		if m.GetKernel() != nil {
			return false
		}
		if m.GetKernelTcp() != nil {
			return false
		}
		if m.GetSwappiness() != nil {
			return false
		}
		if m.GetDisableOomKiller() != nil {
			return false
		}
		if m.GetUseHierarchy() != nil {
			return false
		}

		return true
	}

	if m.GetLimit().GetValue() != o.GetLimit().GetValue() {
		return false
	}
	if m.GetReservation().GetValue() != o.GetReservation().GetValue() {
		return false
	}
	if m.GetSwap().GetValue() != o.GetSwap().GetValue() {
		return false
	}
	if m.GetKernel().GetValue() != o.GetKernel().GetValue() {
		return false
	}
	if m.GetKernelTcp().GetValue() != o.GetKernelTcp().GetValue() {
		return false
	}
	if m.GetSwappiness().GetValue() != o.GetSwappiness().GetValue() {
		return false
	}
	if m.GetDisableOomKiller().GetValue() != o.GetDisableOomKiller().GetValue() {
		return false
	}
	if m.GetUseHierarchy().GetValue() != o.GetUseHierarchy().GetValue() {
		return false
	}

	return true
}

// Equal check if two LinuxCPUs are equal.
func (c *LinuxCPU) Equal(o *LinuxCPU) bool {
	if c == nil && o == nil {
		return true
	}

	// Note: either c or o can be nil here

	if c == nil && o != nil {
		if o.GetShares() != nil {
			return false
		}
		if o.GetQuota() != nil {
			return false
		}
		if o.GetPeriod() != nil {
			return false
		}
		if o.GetRealtimeRuntime() != nil {
			return false
		}
		if o.GetRealtimePeriod() != nil {
			return false
		}
		if o.GetCpus() != "" {
			return false
		}
		if o.GetMems() != "" {
			return false
		}
		return true
	}

	if c != nil && o == nil {
		if c.GetShares() != nil {
			return false
		}
		if c.GetQuota() != nil {
			return false
		}
		if c.GetPeriod() != nil {
			return false
		}
		if c.GetRealtimeRuntime() != nil {
			return false
		}
		if c.GetRealtimePeriod() != nil {
			return false
		}
		if c.GetCpus() != "" {
			return false
		}
		if c.GetMems() != "" {
			return false
		}
		return true
	}

	if c.GetShares().GetValue() != o.GetShares().GetValue() {
		return false
	}
	if c.GetQuota().GetValue() != o.GetQuota().GetValue() {
		return false
	}
	if c.GetPeriod().GetValue() != o.GetPeriod().GetValue() {
		return false
	}
	if c.GetRealtimeRuntime().GetValue() != o.GetRealtimeRuntime().GetValue() {
		return false
	}
	if c.GetRealtimePeriod().GetValue() != o.GetRealtimePeriod().GetValue() {
		return false
	}
	if c.GetCpus() != o.GetCpus() {
		return false
	}
	if c.GetMems() != o.GetMems() {
		return false
	}

	return true
}

// Equal checks if two HugepageLimits are equal.
func (h *HugepageLimit) Equal(o *HugepageLimit) bool {
	if h == nil && o == nil {
		return true
	}
	if h == nil || o == nil {
		return false
	}
	if h.PageSize != o.PageSize || h.Limit != o.Limit {
		return false
	}

	return true
}

// Equal checks two mounts for equality (assuming same orders for slices).
func (m *Mount) Equal(o *Mount) bool {
	if m == nil && o == nil {
		return true
	}
	if m == nil || o == nil {
		return false
	}

	if m.Destination != o.Destination {
		return false
	}
	if m.Type != o.Type {
		return false
	}
	if m.Source != o.Source {
		return false
	}
	if !slices.Equal(m.Options, o.Options) {
		return false
	}

	return true
}

// Equal checks if two LinuxDevices are equal.
func (d *LinuxDevice) Equal(o *LinuxDevice) bool {
	if d == nil && o == nil {
		return true
	}
	if d == nil || o == nil {
		return false
	}

	if d.Path != o.Path || d.Type != o.Type || d.Major != o.Major || d.Minor != o.Minor ||
		d.FileMode.GetValue() != o.FileMode.GetValue() || d.Uid.GetValue() != o.Uid.GetValue() ||
		d.Gid.GetValue() != o.Gid.GetValue() {
		return false
	}

	return true
}

// Equal checks if two LinuxDeviceCgroups are equal.
func (d *LinuxDeviceCgroup) Equal(o *LinuxDeviceCgroup) bool {
	if d == nil && o == nil {
		return true
	}
	if d == nil || o == nil {
		return false
	}

	if d.Allow != o.Allow || d.Type != o.Type || d.Major.GetValue() != o.Major.GetValue() ||
		d.Minor.GetValue() != o.Minor.GetValue() || d.Access != o.Access {
		return false
	}

	return true
}

// Equal checks if two CDIDevices are equal.
func (d *CDIDevice) Equal(o *CDIDevice) bool {
	if d == nil && o == nil {
		return true
	}
	if d == nil || o == nil {
		return false
	}

	if d.Name != o.Name {
		return false
	}

	return true
}

// Equal check if two sets of Hooks are equal.
func (hooks *Hooks) Equal(o *Hooks) bool {
	if hooks == nil && o == nil {
		return true
	}

	if len(hooks.GetPrestart()) != len(o.GetPrestart()) {
		return false
	}
	for i := range hooks.Prestart {
		if !hooks.Prestart[i].Equal(o.Prestart[i]) {
			return false
		}
	}

	if len(hooks.GetCreateRuntime()) != len(o.GetCreateRuntime()) {
		return false
	}
	for i := range hooks.CreateRuntime {
		if !hooks.CreateRuntime[i].Equal(o.CreateRuntime[i]) {
			return false
		}
	}

	if len(hooks.GetCreateContainer()) != len(o.GetCreateContainer()) {
		return false
	}
	for i := range hooks.CreateContainer {
		if !hooks.GetCreateContainer()[i].Equal(o.GetCreateContainer()[i]) {
			return false
		}
	}

	if len(hooks.GetStartContainer()) != len(o.GetStartContainer()) {
		return false
	}
	for i := range hooks.StartContainer {
		if !hooks.StartContainer[i].Equal(o.StartContainer[i]) {
			return false
		}
	}

	if len(hooks.GetPoststart()) != len(o.GetPoststart()) {
		return false
	}
	for i := range hooks.Poststart {
		if !hooks.Poststart[i].Equal(o.Poststart[i]) {
			return false
		}
	}

	if len(hooks.GetPoststop()) != len(o.GetPoststop()) {
		return false
	}
	for i := range hooks.Poststop {
		if !hooks.Poststop[i].Equal(o.Poststop[i]) {
			return false
		}
	}

	return true

}

// Equal checks if two Hook's are equal (assuming same orders for slices).
func (h *Hook) Equal(o *Hook) bool {
	if h == nil && o == nil {
		return true
	}
	if h == nil || o == nil {
		return false
	}

	if h.Path != o.Path {
		return false
	}
	if !slices.Equal(h.Args, o.Args) {
		return false
	}
	if !slices.Equal(h.Env, o.Env) {
		return false
	}
	if h.Timeout.Get() != o.Timeout.Get() {
		return false
	}

	return true
}

// Equal checks if two KeyValues are equal.
func (e *KeyValue) Equal(o *KeyValue) bool {
	if e == nil && o == nil {
		return true
	}
	if e == nil || o == nil {
		return false
	}

	if e.Key != o.Key || e.Value != o.Value {
		return false
	}

	return true
}

// Equal checks two POSIXRlimits for equality.
func (r *POSIXRlimit) Equal(o *POSIXRlimit) bool {
	if r == nil && o == nil {
		return true
	}
	if r == nil || o == nil {
		return false
	}

	if r.Type != o.Type || r.Hard != o.Hard || r.Soft != o.Soft {
		return false
	}

	return true
}
