package network

import (
	"strings"

	"github.com/golang/glog"

	kclientv1 "k8s.io/api/core/v1"
	kclientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	kv1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/apis/componentconfig"
	kinternalclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	kinternalinformers "k8s.io/kubernetes/pkg/client/informers/informers_generated/internalversion"

	configapi "github.com/openshift/origin/pkg/cmd/server/api"
	nodeoptions "github.com/openshift/origin/pkg/cmd/server/kubernetes/node/options"
	"github.com/openshift/origin/pkg/network"
	networkclient "github.com/openshift/origin/pkg/network/generated/internalclientset"
	sdnnode "github.com/openshift/origin/pkg/network/node"
	sdnproxy "github.com/openshift/origin/pkg/network/proxy"
	"github.com/openshift/origin/pkg/util/netutils"
)

func NewSDNInterfaces(options configapi.NodeConfig, networkClient networkclient.Interface, kubeClientset kclientset.Interface, kubeClient kinternalclientset.Interface, internalKubeInformers kinternalinformers.SharedInformerFactory, proxyconfig *componentconfig.KubeProxyConfiguration) (network.NodeInterface, network.ProxyInterface, error) {
	runtimeEndpoint := options.DockerConfig.DockerShimSocket
	runtime, ok := options.KubeletArguments["container-runtime"]
	if ok && len(runtime) == 1 && runtime[0] == "remote" {
		endpoint, ok := options.KubeletArguments["container-runtime-endpoint"]
		if ok && len(endpoint) == 1 {
			runtimeEndpoint = endpoint[0]
		}
	}

	hostName, err := nodeoptions.GetHostName(options)
	if err != nil {
		return nil, nil, err
	}
	nodeIP, err := GetPodTrafficNodeIP(options, hostName)
	if err != nil {
		return nil, nil, err
	}

	// dockershim + kube CNI driver delegates hostport handling to plugins,
	// while CRI-O handles hostports itself. Thus we need to disable the
	// SDN's hostport handling when run under CRI-O.
	enableHostports := !strings.Contains(runtimeEndpoint, "crio")

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&kv1core.EventSinkImpl{Interface: kubeClientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, kclientv1.EventSource{Component: "openshift-sdn", Host: options.NodeName})

	node, err := sdnnode.New(&sdnnode.OsdnNodeConfig{
		PluginName:         options.NetworkConfig.NetworkPluginName,
		Hostname:           hostName,
		SelfIP:             nodeIP,
		RuntimeEndpoint:    runtimeEndpoint,
		MTU:                options.NetworkConfig.MTU,
		NetworkClient:      networkClient,
		KClient:            kubeClient,
		KubeInformers:      internalKubeInformers,
		IPTablesSyncPeriod: proxyconfig.IPTables.SyncPeriod.Duration,
		ProxyMode:          proxyconfig.Mode,
		EnableHostports:    enableHostports,
		Recorder:           recorder,
	})
	if err != nil {
		return nil, nil, err
	}

	proxy, err := sdnproxy.New(options.NetworkConfig.NetworkPluginName, networkClient, kubeClient)
	if err != nil {
		return nil, nil, err
	}

	return node, proxy, nil
}

func GetPodTrafficNodeIP(options configapi.NodeConfig, hostName string) (string, error) {
	nodeIP := options.NetworkConfig.PodTrafficNodeIP
	nodeIface := options.NetworkConfig.PodTrafficNodeInterface

	if len(nodeIP) == 0 {
		if len(nodeIface) > 0 {
			ips, err := netutils.GetIPAddrsFromNetworkInterface(nodeIface)
			if err != nil {
				return "", err
			}
			nodeIP = ips[0].String()
		} else {
			var err error
			if nodeIP, err = nodeoptions.GetMasterTrafficNodeIP(options, hostName); err != nil {
				return "", err
			}
		}
		glog.Infof("Resolved pod traffic node IP address to %q", nodeIP)
	}

	return nodeIP, nil
}
