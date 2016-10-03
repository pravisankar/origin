package network

import (
	"errors"
	"fmt"
	"time"

	flag "github.com/spf13/pflag"

	kapi "k8s.io/kubernetes/pkg/api"
	kclient "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/util/wait"

	osclient "github.com/openshift/origin/pkg/client"
	osclientcmd "github.com/openshift/origin/pkg/cmd/util/clientcmd"
	"github.com/openshift/origin/pkg/diagnostics/networkpod/util"
	"github.com/openshift/origin/pkg/diagnostics/types"
)

const (
	NetworkDiagnosticName = "NetworkCheck"

	debugScript = "https://github.com/openshift/origin/blob/master/hack/debug-network.sh"
)

// NetworkDiagnostic is a diagnostic that runs a network diagnostic pod and relays the results.
type NetworkDiagnostic struct {
	KubeClient          *kclient.Client
	OSClient            *osclient.Client
	ClientFlags         *flag.FlagSet
	Level               int
	Factory             *osclientcmd.Factory
	PreventModification bool

	pluginName string
	nodes      []kapi.Node
	res        types.DiagnosticResult
}

// Name is part of the Diagnostic interface and just returns name.
func (d *NetworkDiagnostic) Name() string {
	return NetworkDiagnosticName
}

// Description is part of the Diagnostic interface and provides a user-focused description of what the diagnostic does.
func (d *NetworkDiagnostic) Description() string {
	return "Create a pod on all schedulable nodes and run network diagnostics from the application standpoint"
}

// CanRun is part of the Diagnostic interface; it determines if the conditions are right to run this diagnostic.
func (d *NetworkDiagnostic) CanRun() (bool, error) {
	if d.PreventModification {
		return false, errors.New("running the network diagnostic pod is an API change, which is prevented as you indicated")
	} else if d.KubeClient == nil {
		return false, errors.New("must have kube client")
	} else if d.OSClient == nil {
		return false, errors.New("must have openshift client")
	} else if _, err := d.getKubeConfig(); err != nil {
		return false, err
	}
	return true, nil
}

// Check is part of the Diagnostic interface; it runs the actual diagnostic logic
func (d *NetworkDiagnostic) Check() types.DiagnosticResult {
	d.res = types.NewDiagnosticResult(NetworkDiagnosticName)

	var err error
	var ok bool
	d.pluginName, ok, err = util.GetOpenShiftNetworkPlugin(d.OSClient)
	if err != nil {
		d.res.Error("DNet2001", err, fmt.Sprintf("Checking network plugin failed. Error: %s", err))
		return d.res
	}
	if !ok {
		d.res.Warn("DNet2002", nil, fmt.Sprintf("Skipping network diagnostics check. Reason: Not using openshift network plugin."))
		return d.res
	}

	d.nodes, err = util.GetSchedulableNodes(d.KubeClient)
	if err != nil {
		d.res.Error("DNet2003", err, fmt.Sprintf("Fetching schedulable nodes failed. Error: %s", err))
		return d.res
	}
	if len(d.nodes) == 0 {
		d.res.Warn("DNet2004", nil, fmt.Sprint("Skipping network checks. Reason: No schedulable/ready nodes found."))
		return d.res
	}

	d.runNetworkDiagnostic()
	return d.res
}

func (d *NetworkDiagnostic) runNetworkDiagnostic() {
	// Setup test environment
	if err := d.TestSetup(); err != nil {
		d.res.Error("DNet2005", err, fmt.Sprintf("Setting up test environment for network diagnostics failed: %v", err))
		return
	}
	defer func() {
		//		d.Cleanup() FIXME
	}()

	// Run network diagnostic pod on all valid nodes
	nerrs := 0
	for _, node := range d.nodes {
		podName, err := d.runNetworkPod(&node, util.NetworkDiagnosticPodName, busyboxImage)
		if err != nil {
			d.res.Error("DNet2006", err, err.Error())
			continue
		}
		d.res.Debug("DNet2007", fmt.Sprintf("Created network diagnostic pod with image %q on node %q.", busyboxImage, node.Name))

		// Gather logs from network diagnostic pod
		nerrs += d.CollectNetworkPodLogs(&node, podName)
	}

	nerrs = 1 //TODO(testing)
	if nerrs > 0 {
		nerrs = 0 // reset
		for _, node := range d.nodes {
			podName, err := d.runNetworkPod(&node, util.NetworkDiagnosticPausePodName, pauseImage)
			if err != nil {
				d.res.Error("DNet2008", err, err.Error())
				continue
			}
			d.res.Debug("DNet2009", fmt.Sprintf("Created network diagnostic pod with image %q on node %q.", pauseImage, node.Name))
			// Collect more details from the node for further analysis
			nerrs += d.CollectNetworkInfo(&node, podName)
		}
	}

	if nerrs > 0 {
		d.res.Info("DNet2017", fmt.Sprintf("Retry network diagnostics, if the errors persist then run %q for further analysis.", debugScript))
	}
}

func (d *NetworkDiagnostic) runNetworkPod(node *kapi.Node, podName, image string) (string, error) {
	podName = kapi.SimpleNameGenerator.GenerateName(fmt.Sprintf("%s-", podName))
	var pod *kapi.Pod
	if image == pauseImage {
		pod = GetNetworkDiagnosticsPausePod(podName, node.Name, image, d.Level)
	} else {
		pod = GetNetworkDiagnosticsPod(podName, node.Name, image, d.Level)
	}

	//	_, err := d.KubeClient.Pods(util.NetworkDiagnosticNamespace).Create(GetNetworkDiagnosticsPod(podName, node.Name, image, d.Level))
	_, err := d.KubeClient.Pods(util.NetworkDiagnosticNamespace).Create(pod)
	if err != nil {
		return podName, fmt.Errorf("Creating network diagnostic pod %q with image %q on node %q failed: %v", podName, image, node.Name, err)
	}

	if err := d.waitForNetworkPod(podName); err != nil {
		return "", err
	}
	return podName, nil
}

func (d *NetworkDiagnostic) waitForNetworkPod(podName string) error {
	backoff := wait.Backoff{
		Steps:    24,
		Duration: 5 * time.Second,
		Factor:   1,
	}
	status_err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		pod, err := d.KubeClient.Pods(util.NetworkDiagnosticNamespace).Get(podName)
		if err != nil {
			return false, err
		}

		if pod.Status.Phase == kapi.PodRunning || pod.Status.Phase == kapi.PodSucceeded {
			return true, nil
		} else if pod.Status.Phase == kapi.PodFailed {
			return false, fmt.Errorf("Pod %q failed to start on node with IP %q", pod.Name, pod.Status.HostIP)
		}
		return false, nil
	})
	if status_err != nil {
		return status_err
	}
	return nil
}
