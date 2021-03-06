package glue

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/containernetworking/cni/libcni"
	"github.com/containernetworking/cni/pkg/types"
)

var (
	CniDir  = "/etc/docker/cni/%s.d"
	CniPath = []string{
		"/opt/cni/bin",
		"/var/lib/cni/bin",
		"/usr/local/sbin",
		"/usr/sbin",
		"/sbin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	}
)

type CNIExec struct {
	confs       []*libcni.NetworkConfig
	runtimeConf libcni.RuntimeConf
	cninet      libcni.CNIConfig
}

func (c *CNIExec) Add(index int) (*types.Result, error) {
	return c.cninet.AddNetwork(c.confs[index], &c.runtimeConf)
}

func (c *CNIExec) Del(index int) error {
	rt := c.runtimeConf
	rt.NetNS = ""
	return c.cninet.DelNetwork(c.confs[index], &rt)
}

func NewCNIExec(state *DockerPluginState) (*CNIExec, error) {
	if state.HostConfig.NetworkMode.IsContainer() ||
		state.HostConfig.NetworkMode.IsHost() ||
		state.HostConfig.NetworkMode.IsNone() {
		return &CNIExec{}, nil
	}

	c := &CNIExec{
		runtimeConf: libcni.RuntimeConf{
			ContainerID: state.ContainerID,
			NetNS:       fmt.Sprintf("/proc/%d/ns/net", state.Pid),
			IfName:      "eth0",
			Args: [][2]string{
				{"IgnoreUnknown", "1"},
				{"DOCKER", "true"},
			},
		},
		cninet: libcni.CNIConfig{
			Path: CniPath,
		},
	}

	if uuid, ok := state.Config.Labels["io.rancher.container.uuid"]; ok {
		c.runtimeConf.Args = append(c.runtimeConf.Args, [2]string{"RancherContainerUUID", uuid})
	}

	if linkMTUOverhead, ok := state.Config.Labels["io.rancher.cni.link_mtu_overhead"]; ok {
		c.runtimeConf.Args = append(c.runtimeConf.Args, [2]string{"LinkMTUOverhead", linkMTUOverhead})
	}

	if MACAddress, ok := state.Config.Labels["io.rancher.container.mac_address"]; ok {
		c.runtimeConf.Args = append(c.runtimeConf.Args, [2]string{"MACAddress", MACAddress})
	}

	if ipAddress, ok := state.Config.Labels["io.rancher.container.ip"]; ok {
		c.runtimeConf.Args = append(c.runtimeConf.Args, [2]string{"IPAddress", ipAddress})
	}

	network := state.HostConfig.NetworkMode.NetworkName()
	if network == "" {
		network = "default"
	}

	dir := fmt.Sprintf(CniDir, network)
	files, err := libcni.ConfFiles(dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("couldn't find any CNI network configurations")
	}
	sort.Strings(files)

	os.Setenv("PATH", strings.Join(CniPath, ":"))

	for _, file := range files {
		netConf, err := libcni.ConfFromFile(file)
		if err != nil {
			return nil, err
		}
		c.confs = append(c.confs, netConf)
	}

	return c, nil
}

func CNIAdd(state *DockerPluginState) (*types.Result, error) {
	c, err := NewCNIExec(state)
	if err != nil {
		return nil, err
	}

	var result *types.Result
	for i := range c.confs {
		pluginResult, err := c.Add(i)
		if err != nil {
			return nil, err
		}
		if pluginResult.IP4 != nil {
			result = pluginResult
		}
	}

	return result, nil
}

func CNIDel(state *DockerPluginState) error {
	c, err := NewCNIExec(state)
	if err != nil {
		return err
	}

	var lastErr error
	for i := len(c.confs) - 1; i >= 0; i-- {
		if err := c.Del(i); err != nil {
			lastErr = err
		}
	}

	return lastErr
}
