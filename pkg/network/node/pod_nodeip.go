package node

import (
	"fmt"
	"time"

	"github.com/golang/glog"

	kapierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilwait "k8s.io/apimachinery/pkg/util/wait"
	kapi "k8s.io/kubernetes/pkg/api"

	networkapi "github.com/openshift/origin/pkg/network/apis/network"
	"github.com/openshift/origin/pkg/network/common"
	"github.com/openshift/origin/pkg/util/netutils"
)

// AssignPodTrafficNodeIP assigns pod traffic node IP as annotation on the local node object.
// We use exponential back off polling on the local node object instead of node shared informers
// because kube informers will not be started until all the openshift controllers are started.
func (plugin *OsdnNode) AssignPodTrafficNodeIP() error {
	backoff := utilwait.Backoff{
		// A bit under 2 minutes total
		Duration: time.Second,
		Factor:   1.5,
		Steps:    11,
	}

	var node *kapi.Node
	err := utilwait.ExponentialBackoff(backoff, func() (bool, error) {
		var err error
		node, err = plugin.kClient.Core().Nodes().Get(plugin.hostName, metav1.GetOptions{})
		if err == nil {
			return true, nil
		} else if kapierrors.IsNotFound(err) {
			glog.Warningf("Could not find local node object: %s, Waiting...", plugin.hostName)
			return false, nil
		} else {
			return false, err
		}
	})
	if err != nil {
		return fmt.Errorf("failed to get node object for this host: %s, error: %v", plugin.hostName, err)
	}

	if err := plugin.handleLocalNode(node); err != nil {
		return err
	}
	return nil
}

func (plugin *OsdnNode) handleLocalNode(node *kapi.Node) error {
	if podTrafficNodeIP, err := common.GetPodTrafficNodeIPAnnotation(node); err == nil {
		if podTrafficNodeIP == plugin.localIP {
			return nil
		}
	}

	if !plugin.isNodeIPLocal(node) {
		return fmt.Errorf("found local node %q but node status IP %v is not local", node.Name, node.Status.Addresses)
	}

	if err := plugin.setPodTrafficNodeIPAnnotation(node); err != nil {
		return fmt.Errorf("unable to set pod traffic node IP annotation for node %q, %v", node.Name, err)
	}

	return nil
}

func (plugin *OsdnNode) isNodeIPLocal(node *kapi.Node) bool {
	if len(node.Status.Addresses) > 0 && node.Status.Addresses[0].Address != "" {
		_, hostIPs, err := netutils.GetHostIPNetworks([]string{Tun0})
		if err != nil {
			glog.Error(err)
		}

		for _, ip := range hostIPs {
			if node.Status.Addresses[0].Address == ip.String() {
				return true
			}
		}
		return false
	}
	return true
}

func (plugin *OsdnNode) setPodTrafficNodeIPAnnotation(node *kapi.Node) error {
	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}
	node.Annotations[networkapi.PodTrafficNodeIPAnnotation] = plugin.localIP

	// A bit over 1 minute total
	backoff := utilwait.Backoff{
		Duration: time.Second,
		Factor:   1.5,
		Steps:    10,
	}

	return utilwait.ExponentialBackoff(backoff, func() (bool, error) {
		_, err := plugin.kClient.Core().Nodes().Update(node)
		if err == nil {
			return true, nil
		} else if kapierrors.IsNotFound(err) {
			return false, fmt.Errorf("could not find local node for host: %s", plugin.hostName)
		} else {
			return false, err
		}
	})
}
