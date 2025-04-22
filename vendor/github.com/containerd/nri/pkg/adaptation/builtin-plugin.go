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

package adaptation

import (
	"context"

	"github.com/containerd/nri/pkg/api"
)

type BuiltinPlugin struct {
	Base     string
	Index    string
	Handlers BuiltinHandlers
}

type BuiltinHandlers struct {
	Configure            func(context.Context, *ConfigureRequest) (*ConfigureResponse, error)
	Synchronize          func(context.Context, *SynchronizeRequest) (*SynchronizeResponse, error)
	RunPodSandbox        func(context.Context, *RunPodSandboxRequest) error
	StopPodSandbox       func(context.Context, *StopPodSandboxRequest) error
	RemovePodSandbox     func(context.Context, *RemovePodSandboxRequest) error
	UpdatePodSandbox     func(context.Context, *UpdatePodSandboxRequest) (*UpdatePodSandboxResponse, error)
	PostUpdatePodSandbox func(context.Context, *PostUpdatePodSandboxRequest) error

	CreateContainer             func(context.Context, *CreateContainerRequest) (*CreateContainerResponse, error)
	PostCreateContainer         func(context.Context, *PostCreateContainerRequest) error
	StartContainer              func(context.Context, *StartContainerRequest) error
	PostStartContainer          func(context.Context, *PostStartContainerRequest) error
	UpdateContainer             func(context.Context, *UpdateContainerRequest) (*UpdateContainerResponse, error)
	PostUpdateContainer         func(context.Context, *PostUpdateContainerRequest) error
	StopContainer               func(context.Context, *StopContainerRequest) (*StopContainerResponse, error)
	RemoveContainer             func(context.Context, *RemoveContainerRequest) error
	ValidateContainerAdjustment func(context.Context, *ValidateContainerAdjustmentRequest) (*ValidateContainerAdjustmentResponse, error)
}

func (b *BuiltinPlugin) Configure(ctx context.Context, req *ConfigureRequest) (*ConfigureResponse, error) {
	var (
		rpl = &ConfigureResponse{}
		err error
	)

	if b.Handlers.Configure != nil {
		rpl, err = b.Handlers.Configure(ctx, req)
	}

	if rpl.Events == 0 {
		var events api.EventMask

		if b.Handlers.RunPodSandbox != nil {
			events.Set(api.Event_RUN_POD_SANDBOX)
		}
		if b.Handlers.StopPodSandbox != nil {
			events.Set(api.Event_STOP_POD_SANDBOX)
		}
		if b.Handlers.RemovePodSandbox != nil {
			events.Set(api.Event_REMOVE_POD_SANDBOX)
		}
		if b.Handlers.UpdatePodSandbox != nil {
			events.Set(api.Event_UPDATE_POD_SANDBOX)
		}
		if b.Handlers.PostUpdatePodSandbox != nil {
			events.Set(api.Event_POST_UPDATE_POD_SANDBOX)
		}
		if b.Handlers.CreateContainer != nil {
			events.Set(api.Event_CREATE_CONTAINER)
		}
		if b.Handlers.PostCreateContainer != nil {
			events.Set(api.Event_POST_CREATE_CONTAINER)
		}
		if b.Handlers.StartContainer != nil {
			events.Set(api.Event_START_CONTAINER)
		}
		if b.Handlers.PostStartContainer != nil {
			events.Set(api.Event_POST_START_CONTAINER)
		}
		if b.Handlers.UpdateContainer != nil {
			events.Set(api.Event_UPDATE_CONTAINER)
		}
		if b.Handlers.PostUpdateContainer != nil {
			events.Set(api.Event_POST_UPDATE_CONTAINER)
		}
		if b.Handlers.StopContainer != nil {
			events.Set(api.Event_STOP_CONTAINER)
		}
		if b.Handlers.RemoveContainer != nil {
			events.Set(api.Event_REMOVE_CONTAINER)
		}
		if b.Handlers.ValidateContainerAdjustment != nil {
			events.Set(api.Event_VALIDATE_CONTAINER_ADJUSTMENT)
		}

		rpl.Events = int32(events)
	}

	return rpl, err
}

func (b *BuiltinPlugin) Synchronize(ctx context.Context, req *SynchronizeRequest) (*SynchronizeResponse, error) {
	if b.Handlers.Synchronize != nil {
		return b.Handlers.Synchronize(ctx, req)
	}
	return &SynchronizeResponse{}, nil
}

func (b *BuiltinPlugin) Shutdown(context.Context, *ShutdownRequest) (*ShutdownResponse, error) {
	return &ShutdownResponse{}, nil
}

func (b *BuiltinPlugin) CreateContainer(ctx context.Context, req *CreateContainerRequest) (*CreateContainerResponse, error) {
	if b.Handlers.CreateContainer != nil {
		return b.Handlers.CreateContainer(ctx, req)
	}
	return &CreateContainerResponse{}, nil
}

func (b *BuiltinPlugin) UpdateContainer(ctx context.Context, req *UpdateContainerRequest) (*UpdateContainerResponse, error) {
	if b.Handlers.UpdateContainer != nil {
		return b.Handlers.UpdateContainer(ctx, req)
	}
	return &UpdateContainerResponse{}, nil
}

func (b *BuiltinPlugin) StopContainer(ctx context.Context, req *StopContainerRequest) (*StopContainerResponse, error) {
	if b.Handlers.StopContainer != nil {
		return b.Handlers.StopContainer(ctx, req)
	}
	return &StopContainerResponse{}, nil
}

func (b *BuiltinPlugin) StateChange(ctx context.Context, evt *StateChangeEvent) (*StateChangeResponse, error) {
	var err error
	switch evt.Event {
	case api.Event_RUN_POD_SANDBOX:
		if b.Handlers.RunPodSandbox != nil {
			err = b.Handlers.RunPodSandbox(ctx, evt)
		}
	case api.Event_STOP_POD_SANDBOX:
		if b.Handlers.StopPodSandbox != nil {
			err = b.Handlers.StopPodSandbox(ctx, evt)
		}
	case api.Event_REMOVE_POD_SANDBOX:
		if b.Handlers.RemovePodSandbox != nil {
			err = b.Handlers.RemovePodSandbox(ctx, evt)
		}
	case api.Event_POST_CREATE_CONTAINER:
		if b.Handlers.PostCreateContainer != nil {
			err = b.Handlers.PostCreateContainer(ctx, evt)
		}
	case api.Event_START_CONTAINER:
		if b.Handlers.StartContainer != nil {
			err = b.Handlers.StartContainer(ctx, evt)
		}
	case api.Event_POST_START_CONTAINER:
		if b.Handlers.PostStartContainer != nil {
			err = b.Handlers.PostStartContainer(ctx, evt)
		}
	case api.Event_POST_UPDATE_CONTAINER:
		if b.Handlers.PostUpdateContainer != nil {
			err = b.Handlers.PostUpdateContainer(ctx, evt)
		}
	case api.Event_REMOVE_CONTAINER:
		if b.Handlers.RemoveContainer != nil {
			err = b.Handlers.RemoveContainer(ctx, evt)
		}
	}

	return &StateChangeResponse{}, err
}

func (b *BuiltinPlugin) UpdatePodSandbox(ctx context.Context, req *UpdatePodSandboxRequest) (*UpdatePodSandboxResponse, error) {
	if b.Handlers.UpdatePodSandbox != nil {
		return b.Handlers.UpdatePodSandbox(ctx, req)
	}
	return &UpdatePodSandboxResponse{}, nil
}

func (b *BuiltinPlugin) PostUpdatePodSandbox(ctx context.Context, req *PostUpdatePodSandboxRequest) error {
	if b.Handlers.PostUpdatePodSandbox != nil {
		return b.Handlers.PostUpdatePodSandbox(ctx, req)
	}
	return nil
}

func (b *BuiltinPlugin) ValidateContainerAdjustment(ctx context.Context, req *ValidateContainerAdjustmentRequest) (*ValidateContainerAdjustmentResponse, error) {
	if b.Handlers.ValidateContainerAdjustment != nil {
		return b.Handlers.ValidateContainerAdjustment(ctx, req)
	}
	return &ValidateContainerAdjustmentResponse{}, nil
}
