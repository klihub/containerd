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

package plugin

import (
	"context"
	"fmt"

	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	"github.com/containerd/containerd/v2/pkg/nri"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

func init() {
	registry.Register(&plugin.Registration{
		Type:            plugins.NRIApiPlugin,
		ID:              "nri",
		Config:          nri.DefaultConfig(),
		ConfigMigration: migrateConfig,
		InitFn:          initFunc,
	})
}

func migrateConfig(ctx context.Context, version int, pluginConfigs map[string]interface{}) error {
	fmt.Printf("**** migrateConfig %d version... %+v\n", version, pluginConfigs)

	if version >= srvconfig.CurrentConfigVersion {
		return nil
	}

	orig, ok := pluginConfigs[string(plugins.NRIApiPlugin)+".nri"]
	if !ok {
		return nil
	}

	for key, value := range orig.(map[string]interface{}) {
		fmt.Printf("*** %s: %v\n", key, value)
	}

	/*
		src := orig.(map[string]interface{})
			updated, ok := pluginConfigs[string(plugins.NRIApiPlugin)+".nri"]
			var dst map[string]interface{}
			if ok {
				dst = updated.(map[string]interface{})
			} else {
				dst = map[string]interface{}{}
			}*/

	return nil
}

func initFunc(ic *plugin.InitContext) (interface{}, error) {
	l, err := nri.New(ic.Config.(*nri.Config))
	return l, err
}
