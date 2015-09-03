package osdn

import (
	"fmt"
	"strconv"
	"time"

	log "github.com/golang/glog"

	kapi "k8s.io/kubernetes/pkg/api"
	kclient "k8s.io/kubernetes/pkg/client"
	"k8s.io/kubernetes/pkg/client/cache"
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/labels"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/types"
	"k8s.io/kubernetes/pkg/watch"

	osdn "github.com/openshift/openshift-sdn/ovssubnet"
	osdnapi "github.com/openshift/openshift-sdn/ovssubnet/api"

	osclient "github.com/openshift/origin/pkg/client"
	oscache "github.com/openshift/origin/pkg/client/cache"
	"github.com/openshift/origin/pkg/sdn/api"
)

type OsdnRegistryInterface struct {
	oClient osclient.Interface
	kClient kclient.Interface
}

func NewOsdnRegistryInterface(osClient *osclient.Client, kClient *kclient.Client) OsdnRegistryInterface {
	return OsdnRegistryInterface{osClient, kClient}
}

func (oi *OsdnRegistryInterface) InitSubnets() error {
	return nil
}

func (oi *OsdnRegistryInterface) GetSubnets() ([]osdnapi.Subnet, string, error) {
	hostSubnetList, err := oi.oClient.HostSubnets().List()
	if err != nil {
		return nil, "", err
	}
	// convert HostSubnet to osdnapi.Subnet
	subList := make([]osdnapi.Subnet, 0, len(hostSubnetList.Items))
	for _, subnet := range hostSubnetList.Items {
		subList = append(subList, osdnapi.Subnet{NodeIP: subnet.HostIP, SubnetIP: subnet.Subnet})
	}
	return subList, hostSubnetList.ListMeta.ResourceVersion, nil
}

func (oi *OsdnRegistryInterface) GetSubnet(nodeName string) (*osdnapi.Subnet, error) {
	hs, err := oi.oClient.HostSubnets().Get(nodeName)
	if err != nil {
		return nil, err
	}
	return &osdnapi.Subnet{NodeIP: hs.HostIP, SubnetIP: hs.Subnet}, nil
}

func (oi *OsdnRegistryInterface) DeleteSubnet(nodeName string) error {
	return oi.oClient.HostSubnets().Delete(nodeName)
}

func (oi *OsdnRegistryInterface) CreateSubnet(nodeName string, sub *osdnapi.Subnet) error {
	hs := &api.HostSubnet{
		TypeMeta:   kapi.TypeMeta{Kind: "HostSubnet"},
		ObjectMeta: kapi.ObjectMeta{Name: nodeName},
		Host:       nodeName,
		HostIP:     sub.NodeIP,
		Subnet:     sub.SubnetIP,
	}
	_, err := oi.oClient.HostSubnets().Create(hs)
	return err
}

func (oi *OsdnRegistryInterface) WatchSubnets(receiver chan<- *osdnapi.SubnetEvent, ready chan<- bool, start <-chan string, stop <-chan bool) error {
	subnetEventQueue, reflector := oi.runEventQueue(&api.HostSubnet{}, nil)
	sendWatchReadiness(reflector, ready)
	startVersion := getStartVersion(start, "HostSubnet")

	checkCondition := true
	for {
		eventType, obj, err := subnetEventQueue.Pop()
		if err != nil {
			return err
		}
		hs := obj.(*api.HostSubnet)
		if checkCondition && skipObject(hs.ObjectMeta.ResourceVersion, startVersion) {
			continue
		}
		checkCondition = false

		switch eventType {
		case watch.Added, watch.Modified:
			receiver <- &osdnapi.SubnetEvent{Type: osdnapi.Added, NodeName: hs.Host, Subnet: osdnapi.Subnet{NodeIP: hs.HostIP, SubnetIP: hs.Subnet}}
		case watch.Deleted:
			receiver <- &osdnapi.SubnetEvent{Type: osdnapi.Deleted, NodeName: hs.Host, Subnet: osdnapi.Subnet{NodeIP: hs.HostIP, SubnetIP: hs.Subnet}}
		}
	}
}

func (oi *OsdnRegistryInterface) InitNodes() error {
	// return no error, as this gets initialized by apiserver
	return nil
}

func (oi *OsdnRegistryInterface) GetNodes() ([]osdnapi.Node, string, error) {
	knodes, err := oi.kClient.Nodes().List(labels.Everything(), fields.Everything())
	if err != nil {
		return nil, "", err
	}

	nodes := make([]osdnapi.Node, 0, len(knodes.Items))
	for _, node := range knodes.Items {
		var nodeIP string
		if len(node.Status.Addresses) > 0 {
			nodeIP = node.Status.Addresses[0].Address
		} else {
			var err error
			nodeIP, err = osdn.GetNodeIP(node.ObjectMeta.Name)
			if err != nil {
				return nil, "", err
			}
		}
		nodes = append(nodes, osdnapi.Node{Name: node.ObjectMeta.Name, IP: nodeIP})
	}
	return nodes, knodes.ListMeta.ResourceVersion, nil
}

func (oi *OsdnRegistryInterface) CreateNode(nodeName string, data string) error {
	return fmt.Errorf("Feature not supported in native mode. SDN cannot create/register nodes.")
}

func (oi *OsdnRegistryInterface) getNodeAddressMap() (map[types.UID]string, error) {
	nodeAddressMap := map[types.UID]string{}

	nodes, err := oi.kClient.Nodes().List(labels.Everything(), fields.Everything())
	if err != nil {
		return nodeAddressMap, err
	}
	for _, node := range nodes.Items {
		if len(node.Status.Addresses) > 0 {
			nodeAddressMap[node.ObjectMeta.UID] = node.Status.Addresses[0].Address
		}
	}
	return nodeAddressMap, nil
}

func (oi *OsdnRegistryInterface) WatchNodes(receiver chan<- *osdnapi.NodeEvent, ready chan<- bool, start <-chan string, stop <-chan bool) error {
	nodeEventQueue, reflector := oi.runEventQueue(&kapi.Node{}, nil)
	sendWatchReadiness(reflector, ready)
	startVersion := getStartVersion(start, "Node")

	nodeAddressMap, err := oi.getNodeAddressMap()
	if err != nil {
		return err
	}

	checkCondition := true
	for {
		eventType, obj, err := nodeEventQueue.Pop()
		if err != nil {
			return err
		}
		node := obj.(*kapi.Node)
		if checkCondition && skipObject(node.ObjectMeta.ResourceVersion, startVersion) {
			continue
		}
		checkCondition = false

		nodeIP := ""
		if len(node.Status.Addresses) > 0 {
			nodeIP = node.Status.Addresses[0].Address
		} else {
			nodeIP, err = osdn.GetNodeIP(node.ObjectMeta.Name)
			if err != nil {
				return err
			}
		}

		switch eventType {
		case watch.Added:
			receiver <- &osdnapi.NodeEvent{Type: osdnapi.Added, Node: osdnapi.Node{Name: node.ObjectMeta.Name, IP: nodeIP}}
			nodeAddressMap[node.ObjectMeta.UID] = nodeIP
		case watch.Modified:
			oldNodeIP, ok := nodeAddressMap[node.ObjectMeta.UID]
			if ok && oldNodeIP != nodeIP {
				// Node Added event will handle update subnet if there is ip mismatch
				receiver <- &osdnapi.NodeEvent{Type: osdnapi.Added, Node: osdnapi.Node{Name: node.ObjectMeta.Name, IP: nodeIP}}
				nodeAddressMap[node.ObjectMeta.UID] = nodeIP
			}
		case watch.Deleted:
			receiver <- &osdnapi.NodeEvent{Type: osdnapi.Deleted, Node: osdnapi.Node{Name: node.ObjectMeta.Name}}
			delete(nodeAddressMap, node.ObjectMeta.UID)
		}
	}
}

func (oi *OsdnRegistryInterface) WriteNetworkConfig(network string, subnetLength uint, serviceNetwork string) error {
	cn, err := oi.oClient.ClusterNetwork().Get("default")
	if err == nil {
		if cn.Network == network && cn.HostSubnetLength == int(subnetLength) && cn.ServiceNetwork == serviceNetwork {
			return nil
		} else {
			return fmt.Errorf("A network already exists and does not match the new network's parameters - Existing: (%s, %d, %s); New: (%s, %d, %s) ", cn.Network, cn.HostSubnetLength, cn.ServiceNetwork, network, subnetLength, serviceNetwork)
		}
	}
	cn = &api.ClusterNetwork{
		TypeMeta:         kapi.TypeMeta{Kind: "ClusterNetwork"},
		ObjectMeta:       kapi.ObjectMeta{Name: "default"},
		Network:          network,
		HostSubnetLength: int(subnetLength),
		ServiceNetwork:   serviceNetwork,
	}
	_, err = oi.oClient.ClusterNetwork().Create(cn)
	return err
}

func (oi *OsdnRegistryInterface) GetContainerNetwork() (string, error) {
	cn, err := oi.oClient.ClusterNetwork().Get("default")
	return cn.Network, err
}

func (oi *OsdnRegistryInterface) GetSubnetLength() (uint64, error) {
	cn, err := oi.oClient.ClusterNetwork().Get("default")
	return uint64(cn.HostSubnetLength), err
}

func (oi *OsdnRegistryInterface) GetServicesNetwork() (string, error) {
	cn, err := oi.oClient.ClusterNetwork().Get("default")
	return cn.ServiceNetwork, err
}

func (oi *OsdnRegistryInterface) CheckEtcdIsAlive(seconds uint64) bool {
	// always assumed to be true as we run through the apiserver
	return true
}

func (oi *OsdnRegistryInterface) GetNamespaces() ([]string, string, error) {
	namespaceList, err := oi.kClient.Namespaces().List(labels.Everything(), fields.Everything())
	if err != nil {
		return nil, "", err
	}
	namespaces := make([]string, 0, len(namespaceList.Items))
	for _, ns := range namespaceList.Items {
		namespaces = append(namespaces, ns.Name)
	}
	return namespaces, namespaceList.ListMeta.ResourceVersion, nil
}

func (oi *OsdnRegistryInterface) WatchNamespaces(receiver chan<- *osdnapi.NamespaceEvent, ready chan<- bool, start <-chan string, stop <-chan bool) error {
	nsEventQueue, reflector := oi.runEventQueue(&kapi.Namespace{}, nil)
	sendWatchReadiness(reflector, ready)
	startVersion := getStartVersion(start, "Namespace")

	checkCondition := true
	for {
		eventType, obj, err := nsEventQueue.Pop()
		if err != nil {
			return err
		}
		ns := obj.(*kapi.Namespace)
		if checkCondition && skipObject(ns.ObjectMeta.ResourceVersion, startVersion) {
			continue
		}
		checkCondition = false

		switch eventType {
		case watch.Added:
			receiver <- &osdnapi.NamespaceEvent{Type: osdnapi.Added, Name: ns.ObjectMeta.Name}
		case watch.Deleted:
			receiver <- &osdnapi.NamespaceEvent{Type: osdnapi.Deleted, Name: ns.ObjectMeta.Name}
		case watch.Modified:
			// Ignore, we don't need to update SDN in case of namespace updates
		}
	}
}

func (oi *OsdnRegistryInterface) WatchNetNamespaces(receiver chan<- *osdnapi.NetNamespaceEvent, ready chan<- bool, start <-chan string, stop <-chan bool) error {
	netnsEventQueue, reflector := oi.runEventQueue(&api.NetNamespace{}, nil)
	sendWatchReadiness(reflector, ready)
	startVersion := getStartVersion(start, "NetNamespace")

	checkCondition := true
	for {
		eventType, obj, err := netnsEventQueue.Pop()
		if err != nil {
			return err
		}
		netns := obj.(*api.NetNamespace)
		if checkCondition && skipObject(netns.ObjectMeta.ResourceVersion, startVersion) {
			continue
		}
		checkCondition = false

		switch eventType {
		case watch.Added:
			receiver <- &osdnapi.NetNamespaceEvent{Type: osdnapi.Added, Name: netns.NetName, NetID: netns.NetID}
		case watch.Deleted:
			receiver <- &osdnapi.NetNamespaceEvent{Type: osdnapi.Deleted, Name: netns.NetName}
		case watch.Modified:
			// Ignore, we don't need to update SDN in case of network namespace updates
		}
	}
}

func (oi *OsdnRegistryInterface) GetNetNamespaces() ([]osdnapi.NetNamespace, string, error) {
	netNamespaceList, err := oi.oClient.NetNamespaces().List()
	if err != nil {
		return nil, "", err
	}
	// convert api.NetNamespace to osdnapi.NetNamespace
	nsList := make([]osdnapi.NetNamespace, 0, len(netNamespaceList.Items))
	for _, netns := range netNamespaceList.Items {
		nsList = append(nsList, osdnapi.NetNamespace{Name: netns.Name, NetID: netns.NetID})
	}
	return nsList, netNamespaceList.ListMeta.ResourceVersion, nil
}

func (oi *OsdnRegistryInterface) GetNetNamespace(name string) (osdnapi.NetNamespace, error) {
	netns, err := oi.oClient.NetNamespaces().Get(name)
	if err != nil {
		return osdnapi.NetNamespace{}, err
	}
	return osdnapi.NetNamespace{Name: netns.Name, NetID: netns.NetID}, nil
}

func (oi *OsdnRegistryInterface) WriteNetNamespace(name string, id uint) error {
	netns := &api.NetNamespace{
		TypeMeta:   kapi.TypeMeta{Kind: "NetNamespace"},
		ObjectMeta: kapi.ObjectMeta{Name: name},
		NetName:    name,
		NetID:      id,
	}
	_, err := oi.oClient.NetNamespaces().Create(netns)
	return err
}

func (oi *OsdnRegistryInterface) DeleteNetNamespace(name string) error {
	return oi.oClient.NetNamespaces().Delete(name)
}

func (oi *OsdnRegistryInterface) InitServices() error {
	return nil
}

func (oi *OsdnRegistryInterface) GetServices() ([]osdnapi.Service, string, error) {
	kNsList, err := oi.kClient.Namespaces().List(labels.Everything(), fields.Everything())
	if err != nil {
		return nil, "", err
	}
	oServList := make([]osdnapi.Service, 0)
	for _, ns := range kNsList.Items {
		kServList, err := oi.kClient.Services(ns.Name).List(labels.Everything())
		if err != nil {
			return nil, "", err
		}

		// convert kube ServiceList into []osdnapi.Service
		for _, kService := range kServList.Items {
			if kService.Spec.ClusterIP == "None" {
				continue
			}
			for _, port := range kService.Spec.Ports {
				oServList = append(oServList, getSDNService(&kService, ns.Name, port))
			}
		}
	}
	return oServList, kNsList.ListMeta.ResourceVersion, nil
}

func (oi *OsdnRegistryInterface) WatchServices(receiver chan<- *osdnapi.ServiceEvent, ready chan<- bool, start <-chan string, stop <-chan bool) error {
	// watch for namespaces, and launch a go func for each namespace that is new
	// kill the watch for each namespace that is deleted
	nsevent := make(chan *osdnapi.NamespaceEvent)
	namespaceTable := make(map[string]chan bool)
	go oi.WatchNamespaces(nsevent, ready, start, stop)
	for {
		select {
		case ev := <-nsevent:
			switch ev.Type {
			case osdnapi.Added:
				stopChannel := make(chan bool)
				namespaceTable[ev.Name] = stopChannel
				go oi.watchServicesForNamespace(ev.Name, receiver, stopChannel)
			case osdnapi.Deleted:
				stopChannel, ok := namespaceTable[ev.Name]
				if ok {
					close(stopChannel)
					delete(namespaceTable, ev.Name)
				}
			}
		case <-stop:
			// call stop on all namespace watching
			for _, stopChannel := range namespaceTable {
				close(stopChannel)
			}
			return nil
		}
	}
}

func (oi *OsdnRegistryInterface) watchServicesForNamespace(namespace string, receiver chan<- *osdnapi.ServiceEvent, stop chan bool) error {
	serviceEventQueue, _ := oi.runEventQueue(&kapi.Service{}, namespace)
	go func() {
		select {
		case <-stop:
			serviceEventQueue.Cancel()
		}
	}()

	for {
		eventType, obj, err := serviceEventQueue.Pop()
		if err != nil {
			if _, ok := err.(oscache.EventQueueStopped); ok {
				return nil
			}
			return err
		}
		kServ := obj.(*kapi.Service)
		// Ignore headless services
		if kServ.Spec.ClusterIP == "None" {
			continue
		}

		switch eventType {
		case watch.Added:
			for _, port := range kServ.Spec.Ports {
				oServ := getSDNService(kServ, namespace, port)
				receiver <- &osdnapi.ServiceEvent{Type: osdnapi.Added, Service: oServ}
			}
		case watch.Deleted:
			for _, port := range kServ.Spec.Ports {
				oServ := getSDNService(kServ, namespace, port)
				receiver <- &osdnapi.ServiceEvent{Type: osdnapi.Deleted, Service: oServ}
			}
		case watch.Modified:
			// Ignore, we don't need to update SDN in case of service updates
		case watch.Error:
			// Check if the namespace is dead, if so quit
			_, err = oi.kClient.Namespaces().Get(namespace)
			if err != nil {
				break
			}
		}
	}
}

func getSDNService(kServ *kapi.Service, namespace string, port kapi.ServicePort) osdnapi.Service {
	return osdnapi.Service{
		Name:      kServ.ObjectMeta.Name,
		Namespace: namespace,
		IP:        kServ.Spec.ClusterIP,
		Protocol:  osdnapi.ServiceProtocol(port.Protocol),
		Port:      uint(port.Port),
	}
}

// Create and run event queue for the given resource
func (oi *OsdnRegistryInterface) runEventQueue(expectedType interface{}, args interface{}) (*oscache.EventQueue, *cache.Reflector) {
	eventQueue := oscache.NewEventQueue(cache.MetaNamespaceKeyFunc)
	lw := &cache.ListWatch{}
	switch expectedType.(type) {
	case *api.HostSubnet:
		lw.ListFunc = func() (runtime.Object, error) {
			return oi.oClient.HostSubnets().List()
		}
		lw.WatchFunc = func(resourceVersion string) (watch.Interface, error) {
			return oi.oClient.HostSubnets().Watch(resourceVersion)
		}
	case *kapi.Node:
		lw.ListFunc = func() (runtime.Object, error) {
			return oi.kClient.Nodes().List(labels.Everything(), fields.Everything())
		}
		lw.WatchFunc = func(resourceVersion string) (watch.Interface, error) {
			return oi.kClient.Nodes().Watch(labels.Everything(), fields.Everything(), resourceVersion)
		}
	case *kapi.Namespace:
		lw.ListFunc = func() (runtime.Object, error) {
			return oi.kClient.Namespaces().List(labels.Everything(), fields.Everything())
		}
		lw.WatchFunc = func(resourceVersion string) (watch.Interface, error) {
			return oi.kClient.Namespaces().Watch(labels.Everything(), fields.Everything(), resourceVersion)
		}
	case *api.NetNamespace:
		lw.ListFunc = func() (runtime.Object, error) {
			return oi.oClient.NetNamespaces().List()
		}
		lw.WatchFunc = func(resourceVersion string) (watch.Interface, error) {
			return oi.oClient.NetNamespaces().Watch(resourceVersion)
		}
	case *kapi.Service:
		namespace := args.(string)
		lw.ListFunc = func() (runtime.Object, error) {
			return oi.kClient.Services(namespace).List(labels.Everything())
		}
		lw.WatchFunc = func(resourceVersion string) (watch.Interface, error) {
			return oi.kClient.Services(namespace).Watch(labels.Everything(), fields.Everything(), resourceVersion)
		}
	default:
		log.Fatalf("Unknown object type during initialization of event queue")
	}
	reflector := cache.NewReflector(lw, expectedType, eventQueue, 4*time.Minute)
	reflector.Run()
	return eventQueue, reflector
}

// Ensures given event queue is ready for watching new changes
// and unblock other end of the ready channel
func sendWatchReadiness(reflector *cache.Reflector, ready chan<- bool) {
	// timeout: 1min
	retries := 120
	retryInterval := 500 * time.Millisecond
	// Try every retryInterval and bail-out if it exceeds max retries
	for i := 0; i < retries; i++ {
		// Reflector does list and watch of the resource
		// when listing of the resource is done, resourceVersion will be populated
		// and the event queue will be ready to watch any new changes
		version := reflector.LastSyncResourceVersion()
		if len(version) > 0 {
			ready <- true
			return
		}
		time.Sleep(retryInterval)
	}
	log.Fatalf("SDN event queue is not ready for watching new changes(timeout: 1min)")
}

// Get resource version from start channel
// Watch interface for the resource will process any item after this version
func getStartVersion(start <-chan string, resourceName string) uint64 {
	var version uint64
	var err error

	timeout := time.Minute
	select {
	case rv := <-start:
		version, err = strconv.ParseUint(rv, 10, 64)
		if err != nil {
			log.Fatalf("Invalid start version %s for %s, error: %v", rv, resourceName, err)
		}
	case <-time.After(timeout):
		log.Fatalf("Error fetching resource version for %s (timeout: %v)", resourceName, timeout)
	}
	return version
}

// Compare new and old versions for the resource
// returns true if new version <= old version, otherwise returns false
func skipObject(newVersion string, oldVersion uint64) bool {
	version, err := strconv.ParseUint(newVersion, 10, 64)
	if err != nil {
		log.Errorf("Invalid ResourceVersion: %v", newVersion)
		return false
	}
	if version <= oldVersion {
		return true
	}
	return false
}
