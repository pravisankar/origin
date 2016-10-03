package network

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	kapi "k8s.io/kubernetes/pkg/api"
	kclient "k8s.io/kubernetes/pkg/client/unversioned"
	//	kexec "k8s.io/kubernetes/pkg/util/exec"

	osclient "github.com/openshift/origin/pkg/client"
	// "github.com/openshift/origin/pkg/diagnostics/client"
	"github.com/openshift/origin/pkg/diagnostics/networkpod/util"
	"github.com/openshift/origin/pkg/diagnostics/types"
	// "github.com/openshift/origin/pkg/sdn/api"
	sdnplugin "github.com/openshift/origin/pkg/sdn/plugin"
	// "github.com/openshift/origin/pkg/util/netutils"
)

const (
	CollectNetworkInfoName = "CollectNetworkInfo"
)

// CollectNetworkInfo is a Diagnostic to collect network information in the cluster.
type CollectNetworkInfo struct {
	KubeClient *kclient.Client
	OSClient   *osclient.Client
}

// Name is part of the Diagnostic interface and just returns name.
func (d CollectNetworkInfo) Name() string {
	return CollectNetworkInfoName
}

// Description is part of the Diagnostic interface and just returns the diagnostic description.
func (d CollectNetworkInfo) Description() string {
	return "Collect network information in the cluster."
}

// CanRun is part of the Diagnostic interface; it determines if the conditions are right to run this diagnostic.
func (d CollectNetworkInfo) CanRun() (bool, error) {
	if d.KubeClient == nil {
		return false, errors.New("must have kube client")
	} else if d.OSClient == nil {
		return false, errors.New("must have openshift client")
	}
	return true, nil
}

// Check is part of the Diagnostic interface; it runs the actual diagnostic logic
func (d CollectNetworkInfo) Check() types.DiagnosticResult {
	r := types.NewDiagnosticResult(CollectNetworkInfoName)

	//path := fmt.Sprintf("%s/%s", client.NetworkDiagnosticContainerMountPath, "/tmp/networklog.txt")
    if err := os.MkdirAll("/tmp/sample/empty", 0700); err != nil {
		r.Error("E", err, fmt.Sprintf("Failed to create dir /tmp/sample/empty"))
		return r
	}
	path := filepath.Join("/tmp/sample", "networklog.txt")

	//path := fmt.Sprintf("%s", "/tmp/networklog.txt")
	err := d.runAndLog(path, "echo", []string{"'Starting network collection...'"})
	if err != nil {
		r.Error("DColNet1001", err, fmt.Sprintf("Running cmd failed. Error: %s", err))
		return r
	}

	pluginName, ok, err := util.GetOpenShiftNetworkPlugin(d.OSClient)
	if err != nil {
		r.Error("DColNet1001", err, fmt.Sprintf("Checking network plugin failed. Error: %s", err))
		return r
	}
	if !ok {
		r.Warn("DColNet1002", nil, fmt.Sprintf("Skipping pod connectivity test. Reason: Not using openshift network plugin."))
		return r
	}

	var vnidMap map[string]uint32
	if sdnplugin.IsOpenShiftMultitenantNetworkPlugin(pluginName) {
		netnsList, err := d.OSClient.NetNamespaces().List(kapi.ListOptions{})
		if err != nil {
			r.Error("DColNet1004", err, fmt.Sprintf("Getting all network namespaces failed. Error: %s", err))
			return r
		}

		vnidMap = map[string]uint32{}
		for _, netns := range netnsList.Items {
			vnidMap[netns.NetName] = netns.NetID
		}
	}

	return r
}

func (d CollectNetworkInfo) runAndLog(outfile, cmdStr string, args []string) error {
	out, err := os.OpenFile(outfile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		return err
	}
	defer out.Close()

	cmd := exec.Command(cmdStr, args...)
	cmd.Stdout = out
	cmd.Stderr = out
	if err = cmd.Run(); err != nil {
		return err
	}
	return nil
}
