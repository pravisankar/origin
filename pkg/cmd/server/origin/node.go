package origin

import (
	"fmt"
	"time"

	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/cache"
	"k8s.io/kubernetes/pkg/fields"

	configapi "github.com/openshift/origin/pkg/cmd/server/api"
	"github.com/openshift/origin/pkg/cmd/server/kubernetes"
)

const (
	// MasterTrafficNodeIPAnnotation stores the node IP to be used by master for node communication
	MasterTrafficNodeIPAnnotation = "network.openshift.io/master-traffic-node-ip"
)

func (c *configapi.NodeConfig) assignMasterTrafficNodeIP(node *kapi.Node) {
	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}
	node.Annotations[MasterTrafficNodeIPAnnotation] = c.MasterTrafficNodeIP

	if _, err := c.Client.Core().Nodes().Update(node); err != nil {
		return err
	}
	return nil
}

// RunAssignMasterTrafficNodeIP assigns node IP to be used by master as an annotation on Node resource
func (c *configapi.NodeConfig) RunAssignMasterTrafficNodeIP() {
	if len(c.MasterTrafficNodeInterface) == 0 && len(c.MasterTrafficNodeIP) == 0 {
		return
	} else if len(c.MasterTrafficNodeInterface) > 0 && len(c.MasterTrafficNodeIP) == 0 {
		c.MasterTrafficNodeIP = kubernetes.GetIPAddrFromNetworkInterface(c.MasterTrafficNodeInterface)
	}

	fieldSelector := fields.SelectorFromSet(map[string]string{kapi.Node.Name: c.NodeName})
	lw := cache.NewListWatchFromClient(client, "nodes", kapi.NamespaceAll, fieldSelector)
	keyFunc := cache.MetaNamespaceKeyFunc
	nodeEventQueue := cache.NewDeltaFIFO(keyFunc, nil, cache.NewStore(keyFunc))
	cache.NewReflector(lw, &kapi.Node{}, nodeEventQueue, 30*time.Minute).Run()

	node, err := c.Client.Core().Nodes().Get(c.NodeName)
	if err == nil {
		err = c.assignMasterTrafficNodeIP(node)
		if err != nil {
			// TODO
		}
	}

	for {
		nodeEventQueue.Pop(func(obj interface{}) error {
			delta, ok := obj.(cache.Deltas)
			if !ok {
				fmt.Printf("Object %v not cache.Delta type", obj)
			}
			node = delta.Newest().Object.(*kapi.Node)
			switch delta.Newest().Type {
			case cache.Added, cache.Synced, cache.Updated:
				err = c.assignMasterTrafficNodeIP(node)
				if err != nil {
					// TODO
				}
			case cache.Delete:
				// Ignore
			}
		})
	}
}
