# NRI Support In Containerd

## Node Resource Interface

NRI, the Node Resource Interface, is a common framework for plugging
extensions into OCI-compatible container runtimes. It provides basic
mechanisms for plugins to track the state of containers and to make
limited changes to their configuration.

NRI itself is agnostic to the internal implementation details of any
container runtime. It provides an adaptation library which runtimes
use to integrate to and interact with NRI and plugins. In principle
any NRI plugin should be able to work with NRI-enabled runtimes.

For a detailed description of NRI and its capabilities please take a
look at the [NRI respository](https://github.com/containerd/nri).

## Containerd NRI Integration

<details>
<summary>see the containerd/NRI integration diagram</summary>
<img src="./containerd-nri-integration.png" title="Containerd/NRI Integration">
</details>

NRI support in containerd is split into two parts both logically and
physically. These parts are a common plugin (/nri/*) to integrate to
NRI and CRI-specific bits (/pkg/cri/server/nri-api) which convert
data between the runtime-agnostic NRI representation and the internal
representation of the CRI plugin.

### Containerd NRI Plugin

The containerd common NRI plugin implements the core logic of integrating
to and interacting with NRI. However, it does this without any knowledge
about the internal representation of containers or pods within containerd.
It defines an additional interface, Domain, which is used whenever the
internal representation of a container or pod needs to be translated to
the runtime agnostic NRI one, or when a configuration change requested by
an external NRI plugin needs to be applied to a container within containerd. `Domain` can be considered as a short-cut name for Domain-Namespace as Domain implements the functions the generic NRI interface needs to deal with pods and containers from a particular containerd namespace. As a reminder, containerd namespaces isolate state between clients of containerd. E.g. "k8s.io" for the kubernetes CRI clients, "moby" for docker clients, ... and "containerd" as the default for containerd/ctr.

### NRI Support for CRI Containers

The containerd CRI plugin registers itself as an above mentioned NRI
Domain for the "k8s.io" namespace, to allow container configuration to be customized by external
NRI plugins. Currently this Domain interface is only implemented for
the original CRI `pkg/cri/server` implementation. Implementing it for
the more recent experimental `pkg/cri/sbserver` implementation is on
the TODO list.

### NRI Support for Other Container 'Domains'

The main reason for this split of functionality is to allow
 NRI plugins for other types of sandboxes and for other container clients other than just for CRI containers in the "k8s.io" namespace.

## Enabling NRI Support in Containerd

Enabling and disabling NRI support in containerd happens by enabling or
disabling the common containerd NRI plugin. The plugin, and consequently
NRI functionality, is disabled by default. It can be enabled by adding
a configuration fragment like this to the containerd configuration:

```toml
  [plugins."io.containerd.nri.v1.nri"]
    disable = false
```

In addition to this, you need to put a runtime agnostic NRI configuration
file in place, to let NRI itself know that it is enabled. You can do this
by creating `/etc/nri/nri.conf` with the following content:

```yaml
disableConnections: false
```

This enables externally launched NRI plugins to connect and register
themselves.

There are two ways how an NRI plugin can be started. Plugins can be
pre-registered in which case they are automatically started when the NRI
adaptation is instantiated (or in our case when containerd is started).
Plugins can also be started by external means, for instance by systemd.

Pre-registering a plugin happens by placing a symbolic link to the plugin
executable into a well-known NRI-specific directory, `/opt/nri/plugins`
by default. A pre-registered plugin is started with a socket pre-connected
to NRI. Externally launched plugins connect to a well-known NRI-specific
socket, `/var/run/nri.sock` by default, to register themselves. The only
difference between pre-registered and externally launched plugins is how
they get started and connected to NRI. Once a connection is established
all plugins are identical.

NRI can be configured to disable connections from externally launched
plugins, in which case the well-known socket is not created at all. The
configuration fragment shown above ensures that external connections are
enabled regardless of the built-in NRI defaults. This is convenient for
testing as it allows one to connect, disconnect and reconnect plugins at
any time.

For more details about pre-registered and externally launched plugins

## Testing NRI Support in Containerd

You can verify that NRI integration is properly enabled and functional by
configuring containerd and NRI as described above, taking the NRI
logger plugin either from the NRI repository or
[this fork](https://github.com/klihub/nri/tree/pr/proto/draft/v2alpha1/plugins/logger)
on github, compiling it and starting it up.

```bash
git clone https://github.com/klihub/nri
git checkout pr/proto/draft
make
./build/bin/logger -idx 00
```

You should see the logger plugin synchronizing its state with containerd and
receiving a list of existing pods and containers. If you then create or remove
further pods and containers using crictl or kubectl you should see detailed logs
of the corresponding NRI events printed by the logger.
