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
	"errors"
	"fmt"
	"strings"

	"github.com/containerd/nri/pkg/log"
	yaml "gopkg.in/yaml.v3"
)

type DefaultValidatorConfig struct {
	Enable         bool `toml:"enable"`           // enable default validator
	RejectOCIHooks bool `toml:"reject_oci_hooks"` // reject OCI hook injection
}

type DefaultValidator struct {
	cfg DefaultValidatorConfig
}

var (
	ErrValidation = errors.New("validation error")
)

const (
	RequiredPluginsAnnotation = "required-plugins.nri.io"
)

// GetDefaultValidator returns a configured instance of the default validator.
// If default validation is disabled nil is returned.
func GetDefaultValidator(cfg *DefaultValidatorConfig) *BuiltinPlugin {
	if cfg == nil || !cfg.Enable {
		log.Infof(noCtx, "built-in NRI default validator is disabled")
		return nil
	}

	v := NewDefaultValidator(cfg)
	return &BuiltinPlugin{
		Base:  "default-validator",
		Index: "00",
		Handlers: BuiltinHandlers{
			ValidateContainerAdjustment: v.ValidateContainerAdjustment,
		},
	}
}

// NewDefaultValidator creates a new instance of the default validator plugin.
func NewDefaultValidator(cfg *DefaultValidatorConfig) *DefaultValidator {
	return &DefaultValidator{cfg: *cfg}
}

func (v *DefaultValidator) ValidateContainerAdjustment(ctx context.Context, req *ValidateContainerAdjustmentRequest) (*ValidateContainerAdjustmentResponse, error) {
	if err := v.validateAdjustment(ctx, req); err != nil {
		log.Errorf(ctx, "rejecting adjusted container: %v", err)
		return &ValidateContainerAdjustmentResponse{
			Reject: true,
			Reason: err.Error(),
		}, nil
	}

	if err := v.validatePlugins(req); err != nil {
		log.Errorf(ctx, "rejecting adjusted container: %v", err)
		return &ValidateContainerAdjustmentResponse{
			Reject: true,
			Reason: err.Error(),
		}, nil
	}

	return &ValidateContainerAdjustmentResponse{}, nil
}

func (v *DefaultValidator) validateAdjustment(_ context.Context, req *ValidateContainerAdjustmentRequest) error {
	if !v.cfg.RejectOCIHooks || req.Adjust == nil {
		return nil
	}

	if err := v.validateOciHooks(req); err != nil {
		return err
	}

	return nil
}

func (v *DefaultValidator) validateOciHooks(req *ValidateContainerAdjustmentRequest) error {
	if req.Adjust == nil {
		return nil
	}

	if plugins, claimed := req.Owners.HooksOwner(req.Container.Id); claimed {
		what := "plugin"
		if strings.Contains(plugins, ",") {
			what = "plugins"
		}

		return fmt.Errorf("%w: %s %s attempted restricted hook injection",
			ErrValidation, what, plugins)
	}

	return nil
}

func (v *DefaultValidator) validatePlugins(req *ValidateContainerAdjustmentRequest) error {
	var (
		annotations = req.GetPod().GetAnnotations()
		container   = req.GetContainer().GetName()
		required    []string
	)

	value, ok := getEffectiveAnnotation(annotations, RequiredPluginsAnnotation, container)
	if !ok {
		return nil
	}

	if err := yaml.Unmarshal([]byte(value), &required); err != nil {
		return fmt.Errorf("invalid %s annotation %q: %w", RequiredPluginsAnnotation, value, err)
	}

	if len(required) == 0 {
		return nil
	}

	present := map[string]struct{}{}
	for _, p := range req.Plugins {
		if p != nil {
			present[p.Name] = struct{}{}
		}
	}

	for _, r := range required {
		if _, ok := present[r]; !ok {
			return fmt.Errorf("%w: required plugin %q not present", ErrValidation, r)
		}
	}

	return nil
}

func getEffectiveAnnotation(annotations map[string]string, key, container string) (string, bool) {
	if len(annotations) == 0 {
		return "", false
	}

	keys := []string{
		key + "/container." + container,
		key + ".pod",
		key,
	}

	for _, key := range keys {
		if value, ok := annotations[key]; ok {
			return value, true
		}
	}

	return "", false
}
