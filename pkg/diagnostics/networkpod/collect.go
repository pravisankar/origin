package network

import (
	"errors"
	"fmt"
	"path/filepath"

	kclient "k8s.io/kubernetes/pkg/client/unversioned"

	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/diagnostics/networkpod/util"
	"github.com/openshift/origin/pkg/diagnostics/types"
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

	l := util.LogInterface{
		Result:    r,
		ChRootDir: util.NetworkDiagnosticContainerMountPath,
	}
	nodeName, _, err := util.GetLocalNode(d.KubeClient)
	if err != nil {
		r.Error("DColNet1001", err, fmt.Sprintf("Fetching local node info failed: %s", err))
		return r
	}
	logdir := filepath.Join("/tmp/nodes", nodeName)
	l.LogNode(logdir)
	return r
}
