package network

// Set up test environment needed for network diagnostics
import (
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	kapi "k8s.io/kubernetes/pkg/api"
	kerrs "k8s.io/kubernetes/pkg/api/errors"
	kclientcmd "k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
	"k8s.io/kubernetes/pkg/util/wait"

	"github.com/openshift/origin/pkg/cmd/cli/config"
	"github.com/openshift/origin/pkg/diagnostics/networkpod/util"
	diagutil "github.com/openshift/origin/pkg/diagnostics/util"
	sdnapi "github.com/openshift/origin/pkg/sdn/api"
	sdnplugin "github.com/openshift/origin/pkg/sdn/plugin"
)

const (
	clientRetryCount    = 24
	clientRetryInterval = 5 * time.Second
	clientRetryFactor   = 1
)

func (d *NetworkDiagnostic) TestSetup() error {
	nsName := util.NetworkDiagnosticNamespace
	globalNsName := util.NetworkDiagnosticGlobalNamespace

	nsList := []string{nsName}
	if sdnplugin.IsOpenShiftMultitenantNetworkPlugin(d.pluginName) {
		nsList = append(nsList, globalNsName)
	}

	for _, name := range nsList {
		// Delete old network diagnostics namespace if exists
		d.KubeClient.Namespaces().Delete(name)

		// Create a new namespace for network diagnostics
		_, err := d.KubeClient.Namespaces().Create(&kapi.Namespace{ObjectMeta: kapi.ObjectMeta{Name: name}})
		if err != nil && !kerrs.IsAlreadyExists(err) {
			return fmt.Errorf("Creating namespace %q failed: %v", name, err)
		}
		if name == util.NetworkDiagnosticGlobalNamespace {
			if err := d.makeNamespaceGlobal(name); err != nil {
				return fmt.Errorf("Making namespace %q global failed: %v", name, err)
			}
		}
	}

	// Create service account for network diagnostics
	_, err := d.KubeClient.ServiceAccounts(nsName).Create(&kapi.ServiceAccount{ObjectMeta: kapi.ObjectMeta{Name: util.NetworkDiagnosticServiceAccountName}})
	if err != nil && !kerrs.IsAlreadyExists(err) {
		return fmt.Errorf("Creating service account %q failed: %v", util.NetworkDiagnosticServiceAccountName, err)
	}

	// Create SCC needed for network diagnostics
	// Need privileged scc + some more network capabilities
	scc, err := d.KubeClient.SecurityContextConstraints().Get("privileged")
	if err != nil {
		return fmt.Errorf("Fetching privileged scc failed: %v", err)
	}

	scc.ObjectMeta = kapi.ObjectMeta{Name: util.NetworkDiagnosticSCCName}
	scc.AllowedCapabilities = []kapi.Capability{"NET_ADMIN"}
	scc.Users = []string{fmt.Sprintf("system:serviceaccount:%s:%s", nsName, util.NetworkDiagnosticServiceAccountName)}
	if _, err = d.KubeClient.SecurityContextConstraints().Create(scc); err != nil && !kerrs.IsAlreadyExists(err) {
		return fmt.Errorf("Creating security context constraint %q failed: %v", util.NetworkDiagnosticSCCName, err)
	}

	// Store kubeconfig as secret, used by network diagnostic pod
	kconfigData, err := d.getKubeConfig()
	if err != nil {
		return fmt.Errorf("Fetching kube config for network pod failed: %v", err)
	}
	secret := &kapi.Secret{}
	secret.Name = util.NetworkDiagnosticSecretName
	secret.Data = map[string][]byte{strings.ToLower(kclientcmd.RecommendedConfigPathEnvVar): kconfigData}
	if _, err = d.KubeClient.Secrets(nsName).Create(secret); err != nil && !kerrs.IsAlreadyExists(err) {
		return fmt.Errorf("Creating secret %q failed: %v", util.NetworkDiagnosticSecretName, err)
	}

	// Create test pods and services on all valid nodes
	for _, node := range d.nodes {
		err := d.createTestPodAndService(&node, nsList)
		if err != nil {
			d.res.Error("DNet3001", err, fmt.Sprintf("Failed to create network diags test pod and service on node %q, %v", node.Name, err))
			continue
		}
	}
	// Wait for test pods and services to be up and running on all valid nodes
	err = d.waitForTestPodAndService(nsList)
	if err != nil {
		return fmt.Errorf("Failed to run network diags test pod and service: %v", err)
	}
	return nil
}

func (d *NetworkDiagnostic) Cleanup() {
	// Delete the setup created for network diags
	d.KubeClient.SecurityContextConstraints().Delete(util.NetworkDiagnosticSCCName)

	// Deleting namespaces will delete corresponding service accounts/pods in the namespace automatically.
	d.KubeClient.Namespaces().Delete(util.NetworkDiagnosticNamespace)
	d.KubeClient.Namespaces().Delete(util.NetworkDiagnosticGlobalNamespace)
}

func (d *NetworkDiagnostic) waitForTestPodAndService(nsList []string) error {
	backoff := wait.Backoff{
		Steps:    clientRetryCount,
		Duration: clientRetryInterval,
		Factor:   clientRetryFactor,
	}
	for _, nsName := range nsList {
		status_err := wait.ExponentialBackoff(backoff, func() (bool, error) {
			podList, err := d.KubeClient.Pods(nsName).List(kapi.ListOptions{})
			if err != nil {
				return false, err
			}

			for _, pod := range podList.Items {
				if pod.Status.Phase == kapi.PodRunning {
					continue
				} else if pod.Status.Phase == kapi.PodFailed {
					return false, fmt.Errorf("Pod %q failed to start on node with IP %q", pod.Name, pod.Status.HostIP)
				}
				return false, nil
			}
			return true, nil
		})
		if status_err != nil {
			return status_err
		}
	}
	return nil
}

func (d *NetworkDiagnostic) createTestPodAndService(node *kapi.Node, nsList []string) error {
	// Create 2 pods and 2 services in global and non-global network diagnostic namespaces
	for i := 0; i <= 2; i++ {
		testPodName := kapi.SimpleNameGenerator.GenerateName(fmt.Sprintf("%s-", util.NetworkDiagnosticTestPodName))
		testServiceName := kapi.SimpleNameGenerator.GenerateName(fmt.Sprintf("%s-", util.NetworkDiagnosticTestServiceName))
		for _, nsName := range nsList {
			// Create network diags test pod on the given node for the given namespace
			_, err := d.KubeClient.Pods(nsName).Create(GetTestPod(testPodName, node.Name))
			if err != nil {
				return fmt.Errorf("Creating network diagnostic test pod '%s/%s' on node %q failed: %v", nsName, testPodName, node.Name, err)
			}

			// Create network diags test service on the given node for the given namespace
			_, err = d.KubeClient.Services(nsName).Create(GetTestService(testServiceName, testPodName, node.Name))
			if err != nil {
				return fmt.Errorf("Creating network diagnostic test service '%s/%s' on node %q failed: %v", nsName, testServiceName, node.Name, err)
			}
		}
	}
	return nil
}

func (d *NetworkDiagnostic) makeNamespaceGlobal(nsName string) error {
	backoff := wait.Backoff{
		Steps:    clientRetryCount,
		Duration: clientRetryInterval,
		Factor:   clientRetryFactor,
	}
	var netns *sdnapi.NetNamespace
	err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		var err error
		netns, err = d.OSClient.NetNamespaces().Get(nsName)
		if kerrs.IsNotFound(err) {
			// NetNamespace not created yet
			return false, nil
		} else if err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil {
		return err
	}

	sdnapi.SetChangePodNetworkAnnotation(netns, sdnapi.GlobalPodNetwork, "")

	if _, err = d.OSClient.NetNamespaces().Update(netns); err != nil {
		return err
	}

	return wait.ExponentialBackoff(backoff, func() (bool, error) {
		updatedNetNs, err := d.OSClient.NetNamespaces().Get(netns.NetName)
		if err != nil {
			return false, err
		}

		_, _, err = sdnapi.GetChangePodNetworkAnnotation(updatedNetNs)
		if err == sdnapi.ErrorPodNetworkAnnotationNotFound {
			return true, nil
		}
		// Pod network change not applied yet
		return false, nil
	})
}

func (d *NetworkDiagnostic) getKubeConfig() ([]byte, error) {
	// KubeConfig path search order:
	// 1. User given config path
	// 2. Default admin config paths
	// 3. Default openshift client config search paths
	paths := []string{}
	paths = append(paths, d.ClientFlags.Lookup(config.OpenShiftConfigFlagName).Value.String())
	paths = append(paths, diagutil.AdminKubeConfigPaths...)
	paths = append(paths, config.NewOpenShiftClientConfigLoadingRules().Precedence...)

	for _, path := range paths {
		if configData, err := ioutil.ReadFile(path); err == nil {
			return configData, nil
		}
	}
	return nil, fmt.Errorf("Unable to find kube config")
}
