package util

import (
	"fmt"

	kapi "k8s.io/kubernetes/pkg/api"
	kerrors "k8s.io/kubernetes/pkg/api/errors"
	kclient "k8s.io/kubernetes/pkg/client/unversioned"

	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/sdn/api"
	sdnplugin "github.com/openshift/origin/pkg/sdn/plugin"
	"github.com/openshift/origin/pkg/util/netutils"
)

const (
	NetworkDiagnosticNamespace          = "network-diagnostic-ns"
	NetworkDiagnosticPodName            = "network-diagnostic-pod"
	NetworkDiagnosticPausePodName       = "network-diagnostic-pause-pod"
	NetworkDiagnosticServiceAccountName = "network-diagnostic-sa"
	NetworkDiagnosticSCCName            = "network-diagnostic-privileged"
	NetworkDiagnosticSecretName         = "network-diagnostic-secret"

	NetworkDiagnosticGlobalNamespace = "network-diagnostic-global-ns"
	NetworkDiagnosticTestPodName     = "network-diagnostic-test-pod"
	NetworkDiagnosticTestServiceName = "network-diagnostic-test-service"
	NetworkDiagnosticTestPodSelector = "network-diagnostic-pod-name"

	NetworkDiagnosticContainerMountPath = "/host"
)

func GetOpenShiftNetworkPlugin(osClient *osclient.Client) (string, bool, error) {
	cn, err := osClient.ClusterNetwork().Get(api.ClusterNetworkDefault)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return cn.PluginName, sdnplugin.IsOpenShiftNetworkPlugin(cn.PluginName), nil
}

func GetNodes(kubeClient *kclient.Client) ([]kapi.Node, error) {
	nodeList, err := kubeClient.Nodes().List(kapi.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("Listing nodes in the cluster failed. Error: %s", err)
	}
	return nodeList.Items, nil
}

func GetSchedulableNodes(kubeClient *kclient.Client) ([]kapi.Node, error) {
	filteredNodes := []kapi.Node{}
	nodes, err := GetNodes(kubeClient)
	if err != nil {
		return filteredNodes, err
	}

	for _, node := range nodes {
		// Skip if node is not schedulable
		if node.Spec.Unschedulable {
			continue
		}

		ready := kapi.ConditionUnknown
		// Get node ready status
		for _, condition := range node.Status.Conditions {
			if condition.Type == kapi.NodeReady {
				ready = condition.Status
				break
			}
		}

		// Skip if node is not ready
		if ready != kapi.ConditionTrue {
			continue
		}
		filteredNodes = append(filteredNodes, node)
	}
	return filteredNodes, nil
}

func GetLocalNode(kubeClient *kclient.Client) (string, string, error) {
	nodeList, err := kubeClient.Nodes().List(kapi.ListOptions{})
	if err != nil {
		return "", "", err
	}

	_, hostIPs, err := netutils.GetHostIPNetworks(nil)
	if err != nil {
		return "", "", err
	}
	for _, node := range nodeList.Items {
		if len(node.Status.Addresses) == 0 {
			continue
		}
		for _, ip := range hostIPs {
			for _, addr := range node.Status.Addresses {
				if addr.Type == kapi.NodeInternalIP && ip.String() == addr.Address {
					return node.Name, addr.Address, nil
				}
			}
		}
	}
	return "", "", fmt.Errorf("unable to find local node IP")
}
